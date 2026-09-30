package gitlab

import (
	"context"
	"fmt"
	"sync"
)

// The finished-pipeline detail view (the Current tab's modal over a recently
// finished pipeline) shows that pipeline's whole tree: its jobs and its
// downstream child pipelines, recursively. It is fetched on demand rather than
// assembled from the retained store, because the store can't be trusted to hold
// a finished tree whole:
//
//   - The parent→child edges are learned only by the warm job-tree walk, i.e. for
//     pipelines glute watched while they ran. Anything that finished before the
//     current session (most of the finished panel, just after startup) has its
//     child pipelines' jobs in the store — the bulk path lists them — but nothing
//     connecting them to the parent.
//   - The store keeps only jobFetchScopes, so the skipped jobs behind a failure
//     (and canceled or manual ones) aren't there at all.
//   - Bulk-fetched jobs carry no needs:, so their order would be wrong.
//
// One GraphQL query per pipeline in the tree answers all three. A tree whose
// every pipeline has finished never changes again, so the Poller caches it.

// jobTreeFetcher has the shape of Client.FetchPipelineJobTree, so the tree walk
// is unit-tested against canned responses instead of a network.
type jobTreeFetcher func(ctx context.Context, projectPath string, iid int64, scopes []Status) (Pipeline, []Job, []childRef, error)

// PipelineTree returns root's full tree, fetched on demand (see above) and
// served from the cache once the whole tree has finished. It's called from the
// UI's own goroutine, concurrently with Refresh, so it touches none of the
// retained store — only the tree cache, under its own lock.
func (p *Poller) PipelineTree(ctx context.Context, root Pipeline) (ActivePipeline, error) {
	p.treeMu.Lock()
	cached, ok := p.trees[root.ID]
	p.treeMu.Unlock()
	if ok {
		return cached, nil
	}

	tree, err := walkPipelineTree(ctx, p.fetchTree, root, p.opts.Concurrency)
	if err != nil {
		return ActivePipeline{}, err
	}
	if treeFinished(tree) {
		p.treeMu.Lock()
		if p.trees == nil {
			p.trees = map[int64]ActivePipeline{}
		}
		p.trees[root.ID] = tree
		p.treeMu.Unlock()
	}
	return tree, nil
}

// pruneTrees drops cached trees whose root pipeline is no longer in the store
// (it aged out of the Top window), so the cache is bounded by what the finished
// panel could still show.
func (p *Poller) pruneTrees() {
	p.treeMu.Lock()
	defer p.treeMu.Unlock()
	for id := range p.trees {
		if _, ok := p.pipes[id]; !ok {
			delete(p.trees, id)
		}
	}
}

// treeFinished reports whether every pipeline in the tree has reached a terminal
// status — the condition for the tree never changing again. A root can finish
// with a child still running (a trigger without strategy: depend), and a
// pipeline parked on a manual job isn't finished either.
func treeFinished(a ActivePipeline) bool {
	if !a.Status.IsFinished() {
		return false
	}
	for _, c := range a.Children {
		if !treeFinished(c) {
			return false
		}
	}
	return true
}

// walkPipelineTree fetches root and, following its bridges level by level, every
// downstream child pipeline beneath it, then assembles the tree. It keeps every
// job status (no scope filter) but only the latest attempt of each job (the
// tree builder's latestAttempts), and it doesn't follow a retried bridge to the
// child pipeline it triggered: GitLab's own pipeline view leaves superseded runs
// out too, and a passed pipeline whose flaky job failed once shouldn't list that
// job as failed.
//
// Any failed fetch fails the whole walk: a tree silently missing a child would
// misreport where a failure was or where the time went. A child that no longer
// exists (a zero pipeline back from the fetch) is simply absent.
func walkPipelineTree(ctx context.Context, fetch jobTreeFetcher, root Pipeline, concurrency int) (ActivePipeline, error) {
	if concurrency <= 0 {
		concurrency = 1
	}
	type nodeKey struct {
		path string
		iid  int64
	}
	type node struct {
		key      nodeKey
		parentID int64 // 0 for the root
	}
	type result struct {
		pipe     Pipeline
		jobs     []Job
		children []childRef
		err      error
	}

	var (
		jobs        []Job
		childPipes  = map[int64]Pipeline{}
		childParent = map[int64]int64{}
		rootNode    Pipeline
	)
	visited := map[nodeKey]bool{{root.ProjectPath, root.IID}: true}
	frontier := []node{{key: nodeKey{root.ProjectPath, root.IID}}}

	for len(frontier) > 0 {
		results := make([]result, len(frontier))
		sem := make(chan struct{}, concurrency)
		var wg sync.WaitGroup
		for i := range frontier {
			wg.Add(1)
			sem <- struct{}{}
			go func(i int) {
				defer wg.Done()
				defer func() { <-sem }()
				n := frontier[i]
				pipe, js, kids, err := fetch(ctx, n.key.path, n.key.iid, nil)
				results[i] = result{pipe: pipe, jobs: js, children: kids, err: err}
			}(i)
		}
		wg.Wait()

		var next []node
		for i, r := range results {
			n := frontier[i]
			if r.err != nil {
				return ActivePipeline{}, r.err
			}
			if r.pipe.ID == 0 {
				if n.parentID == 0 {
					return ActivePipeline{}, fmt.Errorf("pipeline %s #%d not found (deleted, or no longer accessible)",
						root.ProjectPath, root.ID)
				}
				continue // a vanished child: nothing to show under its bridge
			}
			if n.parentID == 0 {
				rootNode = mergeRoot(root, r.pipe)
			} else {
				childPipes[r.pipe.ID] = r.pipe
				childParent[r.pipe.ID] = n.parentID
			}
			jobs = append(jobs, r.jobs...)
			for _, c := range r.children {
				k := nodeKey{c.projectPath, c.iid}
				if c.retried || c.iid == 0 || visited[k] {
					continue
				}
				visited[k] = true
				next = append(next, node{key: k, parentID: r.pipe.ID})
			}
		}
		frontier = next
	}

	return newTreeBuilder(jobs, childPipes, childParent).build(rootNode), nil
}

// mergeRoot overlays the freshly fetched root (GraphQL: status, timing, user) on
// the pipeline the caller passed in, which keeps what GraphQL doesn't return
// here — the project id, iid, source, SHA and web URL. The fetched values win
// where present: they're current, while the caller's copy may predate the
// pipeline's last update or have skipped its detail enrichment.
func mergeRoot(root, fetched Pipeline) Pipeline {
	out := root
	out.ID = fetched.ID
	out.Status = fetched.Status
	if out.WebURL == "" {
		out.WebURL = fetched.WebURL
	}
	if fetched.Ref != "" {
		out.Ref = fetched.Ref
	}
	if fetched.User != "" {
		out.User = fetched.User
	}
	if !fetched.Created.IsZero() {
		out.Created = fetched.Created
	}
	if !fetched.Started.IsZero() {
		out.Started = fetched.Started
	}
	if !fetched.Finished.IsZero() {
		out.Finished = fetched.Finished
	}
	if fetched.Duration > 0 {
		out.Duration = fetched.Duration
	}
	return out
}
