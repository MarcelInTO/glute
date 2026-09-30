package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeTree is a canned GitLab: one FetchPipelineJobTree answer per (path, iid).
// It records the scopes each call asked for, so tests can check the detail walk
// keeps every status.
type fakeTree struct {
	mu     sync.Mutex
	nodes  map[string]fakeNode
	scopes [][]Status
}

type fakeNode struct {
	pipe     Pipeline
	jobs     []Job
	children []childRef
	err      error
}

func treeKey(path string, iid int64) string { return fmt.Sprintf("%s!%d", path, iid) }

func (f *fakeTree) fetch(_ context.Context, path string, iid int64, scopes []Status) (Pipeline, []Job, []childRef, error) {
	f.mu.Lock()
	f.scopes = append(f.scopes, scopes)
	f.mu.Unlock()
	n, ok := f.nodes[treeKey(path, iid)]
	if !ok {
		return Pipeline{}, nil, nil, nil // not found: zero values, no error (as the client does)
	}
	return n.pipe, n.jobs, n.children, n.err
}

// TestWalkPipelineTreeAssemblesLatestAttempts walks a finished root with a
// child (which has a grandchild in another project), a child triggered by a
// retried bridge, and a child that no longer exists. The tree keeps every job
// status — skipped included — but only the latest attempt: the retried job and
// the retried bridge's child are gone, and the vanished child is simply absent.
func TestWalkPipelineTreeAssemblesLatestAttempts(t *testing.T) {
	t0 := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	f := &fakeTree{nodes: map[string]fakeNode{
		treeKey("g/app", 4): {
			pipe: Pipeline{ID: 100, ProjectPath: "g/app", Ref: "main", Status: StatusFailed, User: "alice",
				Created: t0, Started: t0.Add(5 * time.Second), Finished: t0.Add(5 * time.Minute), Duration: 290 * time.Second},
			jobs: []Job{
				{ID: 11, PipelineID: 100, Name: "test", Stage: "test", Status: StatusSuccess},
				{ID: 10, PipelineID: 100, Name: "test", Stage: "test", Status: StatusFailed, Retried: true},
				{ID: 9, PipelineID: 100, Name: "build", Stage: "build", Status: StatusSuccess},
				{ID: 12, PipelineID: 100, Name: "deploy", Stage: "deploy", Status: StatusSkipped},
			},
			children: []childRef{
				{projectPath: "g/app", iid: 5},                // the live child
				{projectPath: "g/app", iid: 6, retried: true}, // superseded by the bridge's retry
				{projectPath: "g/gone", iid: 1},               // deleted since
			},
		},
		treeKey("g/app", 5): {
			pipe:     Pipeline{ID: 200, ProjectPath: "g/app", Status: StatusFailed, Started: t0.Add(time.Minute)},
			jobs:     []Job{{ID: 20, PipelineID: 200, Name: "e2e", Stage: "test", Status: StatusFailed}},
			children: []childRef{{projectPath: "g/lib", iid: 2}},
		},
		treeKey("g/app", 6): {
			pipe: Pipeline{ID: 300, ProjectPath: "g/app", Status: StatusFailed},
			jobs: []Job{{ID: 30, PipelineID: 300, Name: "e2e", Stage: "test", Status: StatusFailed}},
		},
		treeKey("g/lib", 2): {
			pipe: Pipeline{ID: 400, ProjectPath: "g/lib", Status: StatusSuccess},
			jobs: []Job{{ID: 40, PipelineID: 400, Name: "pkg", Stage: "build", Status: StatusSuccess, ProjectPath: "g/lib"}},
		},
	}}
	// The caller's copy (from the pipeline list): what GraphQL doesn't return
	// here must survive, and what it does return must win.
	root := Pipeline{ID: 100, IID: 4, ProjectID: 7, ProjectPath: "g/app", Ref: "main", Status: StatusRunning,
		Source: "push", WebURL: "https://example/g/app/-/pipelines/100"}

	tree, err := walkPipelineTree(context.Background(), f.fetch, root, 4)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if tree.ID != 100 || tree.IID != 4 || tree.ProjectID != 7 || tree.Source != "push" || tree.WebURL == "" {
		t.Errorf("root lost the caller's fields: %+v", tree.Pipeline)
	}
	if tree.Status != StatusFailed || tree.User != "alice" || tree.Duration != 290*time.Second || !tree.Created.Equal(t0) {
		t.Errorf("root didn't take the fetched status/user/timing: %+v", tree.Pipeline)
	}
	// Latest attempts only, in execution order; the skipped job stays.
	if got := jobNames(tree.Jobs); got != "build,test,deploy" {
		t.Errorf("root jobs = %q, want build,test,deploy", got)
	}
	for _, j := range tree.Jobs {
		if j.Retried {
			t.Errorf("retried attempt %d leaked into the tree", j.ID)
		}
	}
	if len(tree.Children) != 1 || tree.Children[0].ID != 200 {
		t.Fatalf("children = %+v, want only the live child 200", tree.Children)
	}
	child := tree.Children[0]
	if jobNames(child.Jobs) != "e2e" || len(child.Children) != 1 || child.Children[0].ID != 400 {
		t.Fatalf("child subtree wrong: %+v", child)
	}
	if gc := child.Children[0]; gc.ProjectPath != "g/lib" || jobNames(gc.Jobs) != "pkg" {
		t.Errorf("grandchild should keep its own project and jobs: %+v", gc)
	}
	// Every fetch asked for all statuses: the store's scopes would drop skipped.
	for i, s := range f.scopes {
		if s != nil {
			t.Errorf("fetch %d filtered to scopes %v, want none", i, s)
		}
	}
}

func TestWalkPipelineTreeRootMissing(t *testing.T) {
	f := &fakeTree{nodes: map[string]fakeNode{}}
	_, err := walkPipelineTree(context.Background(), f.fetch, Pipeline{ID: 1, IID: 1, ProjectPath: "g/app"}, 2)
	if err == nil {
		t.Fatal("want an error for a root that no longer exists")
	}
}

// A failed fetch anywhere fails the walk: a tree quietly missing a child would
// misplace a failure, or the time.
func TestWalkPipelineTreeFailsOnAnyFetchError(t *testing.T) {
	boom := errors.New("gql http 502")
	f := &fakeTree{nodes: map[string]fakeNode{
		treeKey("g/app", 1): {
			pipe:     Pipeline{ID: 1, ProjectPath: "g/app", Status: StatusSuccess},
			children: []childRef{{projectPath: "g/app", iid: 2}},
		},
		treeKey("g/app", 2): {err: boom},
	}}
	_, err := walkPipelineTree(context.Background(), f.fetch, Pipeline{ID: 1, IID: 1, ProjectPath: "g/app"}, 2)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the child's fetch error", err)
	}
}

func TestTreeFinished(t *testing.T) {
	done := ActivePipeline{Pipeline: Pipeline{Status: StatusSuccess},
		Children: []ActivePipeline{{Pipeline: Pipeline{Status: StatusFailed}}}}
	if !treeFinished(done) {
		t.Error("a tree of finished pipelines should count as finished")
	}
	// A root can finish with a fire-and-forget child still running, and a
	// pipeline parked on a manual job hasn't finished — neither may be cached.
	running := done
	running.Children = []ActivePipeline{{Pipeline: Pipeline{Status: StatusRunning}}}
	if treeFinished(running) {
		t.Error("a finished root with a running child must not count as finished")
	}
	if treeFinished(ActivePipeline{Pipeline: Pipeline{Status: StatusManual}}) {
		t.Error("a manual-blocked pipeline must not count as finished")
	}
}

// The tree cache is bounded by the store: a tree whose root aged out goes.
func TestPruneTreesFollowsStore(t *testing.T) {
	p := &Poller{
		pipes: map[int64]Pipeline{1: {ID: 1}},
		trees: map[int64]ActivePipeline{1: {}, 2: {}},
	}
	p.pruneTrees()
	if _, ok := p.trees[1]; !ok {
		t.Error("tree for a pipeline still in the store was pruned")
	}
	if _, ok := p.trees[2]; ok {
		t.Error("tree for an aged-out pipeline was kept")
	}
}

// GraphQL returns retried attempts alongside the latest ones; the flag must
// reach both the job and the child ref a retried bridge yields.
func TestCollectPipelineNodeMapsRetried(t *testing.T) {
	const payload = `{"project":{"pipeline":{"id":"gid://gitlab/Ci::Pipeline/5","status":"SUCCESS","jobs":{
	  "pageInfo":{"hasNextPage":false,"endCursor":""},
	  "nodes":[
	    {"id":"gid://gitlab/Ci::Build/1","name":"t","kind":"BUILD","status":"FAILED","retried":true,"needs":{"nodes":[]}},
	    {"id":"gid://gitlab/Ci::Build/2","name":"t","kind":"BUILD","status":"SUCCESS","retried":false,"needs":{"nodes":[]}},
	    {"id":"gid://gitlab/Ci::Bridge/3","name":"trig","kind":"BRIDGE","status":"FAILED","retried":true,"needs":{"nodes":[]},
	     "downstreamPipeline":{"iid":"8","project":{"fullPath":"g/app"}}}
	  ]}}}}`
	var resp gqlPipelineResp
	if err := json.Unmarshal([]byte(payload), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	jobs, children := collectPipelineNode(resp.Project.Pipeline, "g/app", nil)
	if len(jobs) != 2 || !jobs[0].Retried || jobs[1].Retried {
		t.Errorf("jobs' retried flags = %+v, want [true false]", jobs)
	}
	if len(children) != 1 || !children[0].retried {
		t.Errorf("children = %+v, want one retried child ref", children)
	}
}

// The sample trees back the detail view under --sample, so each must agree
// with the finished panel's pipeline and exercise what the view is for.
func TestSampleTreesCoverRecentPipelines(t *testing.T) {
	s := SampleSnapshot()
	trees := SampleTrees(s)
	for _, p := range s.RecentPipelines {
		tree, ok := trees[p.ID]
		if !ok {
			t.Fatalf("no sample tree for recent pipeline %d", p.ID)
		}
		if tree.Status != p.Status || tree.Duration != p.Duration || !tree.Finished.Equal(p.Finished) {
			t.Errorf("tree %d disagrees with the finished panel: %+v vs %+v", p.ID, tree.Pipeline, p)
		}
		for _, j := range tree.Jobs {
			if !j.Finished.IsZero() && j.Finished.After(p.Finished) {
				t.Errorf("tree %d: job %s finishes after its pipeline", p.ID, j.Name)
			}
		}
	}
	var failed, skipped, child bool
	for _, tree := range trees {
		child = child || len(tree.Children) > 0
		for _, j := range tree.Jobs {
			failed = failed || j.Status == StatusFailed
			skipped = skipped || j.Status == StatusSkipped
		}
	}
	if !failed || !skipped || !child {
		t.Errorf("sample trees should include a failed job, a skipped job and a child (got %v %v %v)", failed, skipped, child)
	}
}
