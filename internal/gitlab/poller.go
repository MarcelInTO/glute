package gitlab

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"
)

// pipeWatermarkOverlap is subtracted from the pipeline-delta watermark on each
// incremental refresh, so a pipeline whose updated_at lands near the boundary
// (or under client/server clock skew) is still re-fetched. Re-fetching a few
// already-known pipelines is cheap (the upsert is idempotent and finished ones
// skip the detail call), so the margin is generous.
const pipeWatermarkOverlap = 5 * time.Minute

// sourceParentPipeline is the GitLab pipeline `source` for a dynamically-created
// child pipeline. Such pipelines are reached only via their parent's bridges
// (they don't appear in the project pipeline list), so they're kept out of the
// aggregate store and would otherwise double-count against their parent's ref.
const sourceParentPipeline = "parent_pipeline"

// PollOptions tunes a Poller. Zero values fall back to sensible defaults.
type PollOptions struct {
	RecentWindow   time.Duration // "recent failures & successes" lookback
	RecentMin      int           // recent pipelines reach back past RecentWindow until there are this many
	TopWindow      time.Duration // "top … last month" lookback
	TopLimit       int           // max rows in the Top panels
	Concurrency    int           // max projects/detail-fetches in parallel
	MaxDetailFetch int           // cap on pipeline-detail calls per refresh
	ResyncInterval time.Duration // how often to re-list the full window as a reconcile
	ResolveTTL     time.Duration // how long a resolved project set is reused
}

func (o PollOptions) withDefaults() PollOptions {
	if o.RecentWindow <= 0 {
		o.RecentWindow = 24 * time.Hour
	}
	if o.RecentMin <= 0 {
		o.RecentMin = 10
	}
	if o.TopWindow <= 0 {
		o.TopWindow = 30 * 24 * time.Hour
	}
	if o.TopLimit <= 0 {
		o.TopLimit = 20
	}
	if o.Concurrency <= 0 {
		o.Concurrency = 16
	}
	if o.MaxDetailFetch <= 0 {
		o.MaxDetailFetch = 1000
	}
	if o.ResyncInterval <= 0 {
		o.ResyncInterval = 10 * time.Minute
	}
	if o.ResolveTTL <= 0 {
		o.ResolveTTL = 10 * time.Minute
	}
	return o
}

// Poller fetches CI data for the configured products and assembles Snapshots.
// It implements Service.
//
// Rather than re-download the whole Top window every refresh, the Poller keeps a
// retained in-memory store of the pipelines and jobs within that window and
// updates it incrementally: each refresh fetches only pipelines that changed
// (via updated_after) and the jobs of the few pipelines that are active or just
// changed. Terminal pipelines and jobs are immutable, so they're learned once
// and kept until they age out. A periodic full-window re-list reconciles any
// drift. See CLAUDE.md for the measurements that motivated this.
type Poller struct {
	client *Client
	specs  []ProductSpec
	opts   PollOptions
	// fetchTree is client.FetchPipelineJobTree, held as a function so both
	// job-tree walks (the warm path's fetchJobsTree, the detail view's
	// walkPipelineTree) run against canned responses in tests.
	fetchTree jobTreeFetcher

	// Retained window store and its bookkeeping. Accessed only from Refresh,
	// which the UI never calls concurrently, so no lock is needed here.
	pipes         map[int64]Pipeline // Top-window pipelines, keyed by pipeline ID
	jobs          map[int64]Job      // Top-window jobs, keyed by job ID
	jobsDone      map[int64]bool     // pipeline IDs whose terminal jobs are fully captured
	pending       map[int64]bool     // finished root pipelines with a still-active child subtree
	childPipes    map[int64]Pipeline // child pipeline metadata (id → pipeline), for the Current tree
	childParent   map[int64]int64    // child pipeline id → its immediate parent pipeline id
	pipeWatermark time.Time          // start time of the last pipeline fetch
	lastResync    time.Time          // when the last full-window re-list ran

	cachedProjects []Project // last resolved project set (reused within ResolveTTL)
	cachedErrs     []error   // resolve warnings paired with cachedProjects
	lastResolve    time.Time // when the project set was last resolved

	statsMu sync.Mutex
	stats   RefreshStats // cumulative per-phase timing across refreshes

	// Finished-pipeline trees fetched on demand for the detail view (see
	// tree.go), keyed by root pipeline id. PipelineTree runs concurrently with
	// Refresh, hence the lock — unlike the store above.
	treeMu sync.Mutex
	trees  map[int64]ActivePipeline
}

// RefreshStats reports the cumulative time each Refresh phase has cost across a
// Poller's lifetime, so the average per-phase cost can be surfaced on exit. This
// is diagnostic instrumentation to inform caching decisions: the group→project
// resolve and the data fetch are timed separately.
type RefreshStats struct {
	Count   int           // refreshes measured
	Resolve time.Duration // total group-scan / project-resolution time
	Fetch   time.Duration // total pipeline+job list-fetch time
	Enrich  time.Duration // total pipeline-detail (duration) enrichment time
}

// AvgResolve is the mean group-scan time per refresh (0 if none measured).
func (s RefreshStats) AvgResolve() time.Duration { return avgDuration(s.Resolve, s.Count) }

// AvgFetch is the mean pipeline+job list-fetch time per refresh.
func (s RefreshStats) AvgFetch() time.Duration { return avgDuration(s.Fetch, s.Count) }

// AvgEnrich is the mean pipeline-detail enrichment time per refresh.
func (s RefreshStats) AvgEnrich() time.Duration { return avgDuration(s.Enrich, s.Count) }

func avgDuration(total time.Duration, n int) time.Duration {
	if n <= 0 {
		return 0
	}
	return total / time.Duration(n)
}

// NewPoller builds a Poller for the given client and product specs.
func NewPoller(client *Client, specs []ProductSpec, opts PollOptions) *Poller {
	return &Poller{
		client:      client,
		specs:       specs,
		opts:        opts.withDefaults(),
		fetchTree:   client.FetchPipelineJobTree,
		pipes:       map[int64]Pipeline{},
		jobs:        map[int64]Job{},
		jobsDone:    map[int64]bool{},
		pending:     map[int64]bool{},
		childPipes:  map[int64]Pipeline{},
		childParent: map[int64]int64{},
		trees:       map[int64]ActivePipeline{},
	}
}

// Refresh updates the retained store from GitLab and assembles a Snapshot from
// it. Per-project failures are folded into Snapshot.Errors rather than aborting,
// so the UI always gets whatever data succeeded (and whatever the store already
// held from prior refreshes).
func (p *Poller) Refresh(ctx context.Context) (Snapshot, error) {
	now := time.Now()
	windowStart := now.Add(-p.opts.TopWindow)
	recentSince := now.Add(-p.opts.RecentWindow)

	// 1. Resolve the project set (cached; re-resolved only past ResolveTTL).
	resolveStart := time.Now()
	projects, errs := p.resolveCached(ctx, now)
	resolveDur := time.Since(resolveStart)
	if len(projects) == 0 {
		p.recordTiming(resolveDur, 0, 0)
		return Snapshot{UpdatedAt: now, Errors: errs}, nil
	}

	// 2. Incremental delta vs. a full-window reconcile. The store is empty on
	//    the first refresh (cold) — that path naturally backfills everything.
	cold := p.pipeWatermark.IsZero()
	fullResync := cold || now.Sub(p.lastResync) >= p.opts.ResyncInterval
	pipeSince := windowStart
	if !fullResync {
		if s := p.pipeWatermark.Add(-pipeWatermarkOverlap); s.After(windowStart) {
			pipeSince = s
		}
	}

	// 3. Fetch pipelines that changed since pipeSince (list endpoint; no duration).
	//    Child pipelines are dropped here: they're reached via their parent's
	//    bridges (below), not aggregated as pipelines of their own.
	fetchStart := time.Now()
	pipeResults := p.fetchPipelines(ctx, projects, pipeSince)
	changed, pipeErrs := flattenPipelines(pipeResults)
	changed = dropChildPipelines(changed)
	pipeDur := time.Since(fetchStart)
	errs = append(errs, pipeErrs...)

	// 4. Enrich durations for the changed pipelines that need it.
	enrichStart := time.Now()
	skipped := p.enrichDurations(ctx, changed)
	enrichDur := time.Since(enrichStart)
	if skipped > 0 {
		errs = append(errs, fmt.Errorf(
			"skipped duration lookups for %d pipelines (MaxDetailFetch=%d); Top averages are partial",
			skipped, p.opts.MaxDetailFetch))
	}

	// 5. Merge the changed pipelines into the store.
	for _, pi := range changed {
		p.upsertPipe(pi)
	}

	// 6. Fetch jobs. A full-window pass uses the cheap project-wide bulk list
	//    (which already includes child-pipeline jobs) — but only for projects
	//    that had any pipeline in the window; the full-window pipeline list we
	//    just did tells us which (bulkJobProjects), and on a typical watchlist
	//    most projects are idle, so this is where the resync's cost went. A warm
	//    delta walks only the active/changed pipeline trees — following bridges
	//    into child pipelines — so live jobs stay fresh without re-listing
	//    history. A root is marked done only once its whole subtree is terminal,
	//    so a child outliving its parent is still polled.
	jobStart := time.Now()
	var (
		jobs        []Job
		jobErrs     []error
		jobRoots    int
		jobProjects int
	)
	if fullResync {
		active := bulkJobProjects(pipeResults)
		jobProjects = len(active)
		jobs, jobErrs = p.fetchJobsBulk(ctx, active, windowStart)
	} else {
		rootPipes := p.jobRoots(changed)
		jobRoots = len(rootPipes)
		var settled, stillPending []int64
		jobs, settled, stillPending, jobErrs = p.fetchJobsTree(ctx, rootPipes)
		for _, id := range settled {
			p.jobsDone[id] = true // subtree terminal: its jobs won't change again
			delete(p.pending, id)
		}
		for _, id := range stillPending {
			p.pending[id] = true
		}
	}
	jobDur := time.Since(jobStart)
	errs = append(errs, jobErrs...)
	for _, j := range jobs {
		p.jobs[j.ID] = j
	}

	// 7. Age out anything now beyond the Top window, then advance watermarks.
	p.evict(windowStart)
	p.pruneTrees()
	p.pipeWatermark = fetchStart
	if fullResync {
		p.lastResync = now
	}

	p.recordTiming(resolveDur, pipeDur+jobDur, enrichDur)
	mode := "incremental"
	if cold {
		mode = "cold"
	} else if fullResync {
		mode = "resync"
	}
	log.Printf("refresh timing: resolve=%s pipes=%s jobs=%s enrich=%s (%s, changed=%d, jobRoots=%d, jobProjects=%d/%d, pending=%d, store: %d pipes / %d jobs)",
		resolveDur.Round(time.Millisecond), pipeDur.Round(time.Millisecond), jobDur.Round(time.Millisecond),
		enrichDur.Round(time.Millisecond), mode, len(changed), jobRoots, jobProjects, len(projects), len(p.pending), len(p.pipes), len(p.jobs))

	// 8. Derive the panels from the whole retained store — the live/recent views
	//    once, the history aggregates at every selectable window (the last one,
	//    the full Top window, doubles as the Snapshot's flat default).
	pipeSlice := p.pipeSlice()
	jobSlice := p.jobSlice()
	windows := historyWindows(now, p.opts.TopWindow, pipeSlice, jobSlice, projectProductMap(projects), p.opts.TopLimit)
	return Snapshot{
		Projects:         len(projects),
		Current:          activePipelines(pipeSlice, jobSlice, p.childPipes, p.childParent),
		RunningPipelines: runningPipelines(pipeSlice),
		RecentPipelines:  recentPipelines(pipeSlice, recentSince, p.opts.RecentMin),
		FailingRefs:      failingRefs(pipeSlice),
		RunningJobs:      runningJobs(jobSlice),
		RecentJobs:       recentJobs(jobSlice, recentSince),
		WindowStats:      windows[len(windows)-1],
		Windows:          windows,
		UpdatedAt:        now,
		Errors:           errs,
	}, nil
}

// projectProductMap indexes each project's path to the product(s) that watch it,
// for attributing job runner-time to products. A project can match more than one
// product (see resolveProjects), so the value is a slice.
func projectProductMap(projects []Project) map[string][]string {
	m := make(map[string][]string, len(projects))
	for _, p := range projects {
		if len(p.Products) > 0 {
			m[p.Path] = p.Products
		}
	}
	return m
}

// resolveCached returns the resolved project set, re-resolving only when the
// cached set is older than ResolveTTL (group membership changes rarely, so this
// keeps the ~second-long group scan off most refreshes). A re-resolve that turns
// up nothing is treated as a transient failure: the last known-good set is kept
// and retried next refresh, so a network blip doesn't blank the dashboard (and
// discard the retained store) when we already have projects to poll.
func (p *Poller) resolveCached(ctx context.Context, now time.Time) ([]Project, []error) {
	if len(p.cachedProjects) > 0 && now.Sub(p.lastResolve) < p.opts.ResolveTTL {
		return p.cachedProjects, p.cachedErrs
	}
	projects, errs := resolveProjects(ctx, p.client, p.specs)
	if len(projects) > 0 {
		p.cachedProjects = projects
		p.cachedErrs = errs
		p.lastResolve = now
		return projects, errs
	}
	if len(p.cachedProjects) > 0 {
		return p.cachedProjects, append(errs, p.cachedErrs...)
	}
	return projects, errs
}

// jobFetchScopes lists the job statuses glute pulls (bulk and per-pipeline
// alike): the not-yet-finished states (created, pending, running) so the Running
// panels include queued work, plus success and failed for the Recent and Top
// panels. Keeping both fetch paths on the same scopes keeps the panels
// consistent regardless of which path surfaced a job.
var jobFetchScopes = []Status{
	StatusCreated,
	StatusPending,
	StatusRunning,
	StatusSuccess,
	StatusFailed,
}

// fetchJobsBulk fans out per-project project-wide job lists with bounded
// concurrency, tagging each job with its project path. Used for the full-window
// backfill; the project-wide endpoint already includes child-pipeline jobs.
func (p *Poller) fetchJobsBulk(ctx context.Context, projects []Project, since time.Time) ([]Job, []error) {
	type result struct {
		jobs []Job
		err  error
	}
	results := make([]result, len(projects))

	sem := make(chan struct{}, p.opts.Concurrency)
	var wg sync.WaitGroup
	for i := range projects {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			jobs, err := p.client.ListJobs(ctx, projects[i].ID, jobFetchScopes, since)
			for j := range jobs {
				jobs[j].ProjectPath = projects[i].Path
			}
			results[i] = result{jobs: jobs, err: err}
		}(i)
	}
	wg.Wait()

	var all []Job
	var errs []error
	for _, r := range results {
		if r.err != nil {
			errs = append(errs, r.err)
		}
		all = append(all, r.jobs...)
	}
	return all, errs
}

// jobRoots is the set of top-level pipelines to traverse for jobs this refresh:
// everything currently active in the store (re-polled so the Running panel stays
// live), plus changed pipelines whose jobs we haven't yet fully captured, plus
// pending roots (finished but with a child subtree still running). Child
// pipelines are never roots — they're reached by following bridges from these.
// On the cold refresh the store holds the whole window and nothing is
// job-complete, so this expands to every pipeline: the one-time job backfill.
func (p *Poller) jobRoots(changed []Pipeline) []Pipeline {
	roots := make(map[int64]Pipeline)
	for _, pi := range p.pipes {
		if pi.Status.IsActive() {
			roots[pi.ID] = pi
		}
	}
	for _, pi := range changed {
		if !p.jobsDone[pi.ID] {
			roots[pi.ID] = pi
		}
	}
	for id := range p.pending {
		if pi, ok := p.pipes[id]; ok && !p.jobsDone[id] {
			roots[id] = pi
		}
	}
	out := make([]Pipeline, 0, len(roots))
	for _, pi := range roots {
		out = append(out, pi)
	}
	return out
}

// dropChildPipelines removes dynamically-created child pipelines from a
// list-derived slice. They're handled via bridge traversal, and counting them
// as pipelines would double-count against their parent's ref in Top Pipelines.
func dropChildPipelines(pipes []Pipeline) []Pipeline {
	out := pipes[:0]
	for _, pi := range pipes {
		if pi.Source == sourceParentPipeline {
			continue
		}
		out = append(out, pi)
	}
	return out
}

// upsertPipe stores a pipeline, preserving a previously-known duration when the
// incoming (list-derived) copy lacks one, so a detail lookup skipped by the
// MaxDetailFetch cap never drops timing we already had.
func (p *Poller) upsertPipe(pi Pipeline) {
	if old, ok := p.pipes[pi.ID]; ok {
		if pi.Duration == 0 && old.Duration > 0 {
			applyDetail(&pi, old)
		}
		// The triggering user is immutable, but the list endpoint (and a skipped
		// or failed detail fetch) leaves it blank; keep a value we already learned.
		if pi.User == "" && old.User != "" {
			pi.User = old.User
		}
	}
	p.pipes[pi.ID] = pi
}

// evict drops store entries that have aged out of the Top window, keying
// pipelines on updated_at (mirroring the updated_after fetch boundary) and jobs
// on created_at (mirroring the old job-window filter). It then prunes the
// jobsDone/pending bookkeeping down to pipelines still represented in the store,
// so child-pipeline IDs (which never live in p.pipes) don't accumulate forever.
func (p *Poller) evict(windowStart time.Time) {
	for id, pi := range p.pipes {
		if pi.Updated.Before(windowStart) {
			delete(p.pipes, id)
		}
	}
	for id, j := range p.jobs {
		if !j.Created.IsZero() && j.Created.Before(windowStart) {
			delete(p.jobs, id)
		}
	}

	live := make(map[int64]bool, len(p.pipes))
	for id := range p.pipes {
		live[id] = true
	}
	for _, j := range p.jobs {
		live[j.PipelineID] = true
	}
	for id := range p.jobsDone {
		if !live[id] {
			delete(p.jobsDone, id)
		}
	}
	for id := range p.pending {
		if _, ok := p.pipes[id]; !ok {
			delete(p.pending, id) // roots always live in p.pipes; drop if aged out
		}
	}

	// Prune the retained tree to child pipelines still represented in the store
	// (those with a live job, via live[j.PipelineID]); a child whose jobs have
	// aged out no longer belongs to any visible active tree.
	for id := range p.childPipes {
		if !live[id] {
			delete(p.childPipes, id)
			delete(p.childParent, id)
		}
	}
}

func (p *Poller) pipeSlice() []Pipeline {
	out := make([]Pipeline, 0, len(p.pipes))
	for _, pi := range p.pipes {
		out = append(out, pi)
	}
	return out
}

func (p *Poller) jobSlice() []Job {
	out := make([]Job, 0, len(p.jobs))
	for _, j := range p.jobs {
		out = append(out, j)
	}
	return out
}

// recordTiming accumulates one refresh's per-phase durations.
func (p *Poller) recordTiming(resolve, fetch, enrich time.Duration) {
	p.statsMu.Lock()
	defer p.statsMu.Unlock()
	p.stats.Count++
	p.stats.Resolve += resolve
	p.stats.Fetch += fetch
	p.stats.Enrich += enrich
}

// RefreshStats returns a snapshot of the cumulative per-phase timing collected
// so far. It is safe to call concurrently with an in-flight Refresh (e.g. from
// the exit path while the background refresh goroutine is winding down).
func (p *Poller) RefreshStats() RefreshStats {
	p.statsMu.Lock()
	defer p.statsMu.Unlock()
	return p.stats
}

// projectPipelines is one project's pipeline-list result: the pipelines in the
// requested range — raw, child pipelines included — or the error that prevented
// listing them. Results are kept per project rather than flattened so that a
// full-window pass can tell which projects positively had no pipeline in the
// window and skip their job re-list (see bulkJobProjects).
type projectPipelines struct {
	project Project
	pipes   []Pipeline
	err     error
}

// fetchPipelines fans out per-project pipeline-list calls with bounded
// concurrency, returning each project's pipelines updated since `since` (tagged
// with the project path) or its error, in projects order.
func (p *Poller) fetchPipelines(ctx context.Context, projects []Project, since time.Time) []projectPipelines {
	results := make([]projectPipelines, len(projects))

	sem := make(chan struct{}, p.opts.Concurrency)
	var wg sync.WaitGroup
	for i := range projects {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			pipes, err := p.client.ListPipelines(ctx, projects[i].ID, since)
			for j := range pipes {
				pipes[j].ProjectPath = projects[i].Path
			}
			results[i] = projectPipelines{project: projects[i], pipes: pipes, err: err}
		}(i)
	}
	wg.Wait()
	return results
}

// flattenPipelines merges per-project list results into one pipeline slice plus
// the per-project errors, for the store merge.
func flattenPipelines(results []projectPipelines) ([]Pipeline, []error) {
	var all []Pipeline
	var errs []error
	for _, r := range results {
		if r.err != nil {
			errs = append(errs, r.err)
		}
		all = append(all, r.pipes...)
	}
	return all, errs
}

// bulkJobProjects picks the projects a full-window pass must re-list jobs for:
// every project except those positively known to have had no pipeline in the
// window — a successful pipeline list that came back empty. Any job created in
// the window belongs to a pipeline updated in the window, so such a project has
// no jobs in the window either and skipping it loses nothing. results must come
// from the full-window listing (since = window start), not a warm delta.
//
// The test is on the raw list, before child pipelines are dropped and blind to
// source, so a project that's alive only through a forgotten scheduled pipeline
// (or a trigger/API one, or only as a cross-project downstream child) is still
// re-listed: it is GitLab's own pipeline list that decides, never a proxy like
// last_activity_at, which CI-only activity doesn't move. A project whose list
// call failed is kept — we can't tell — so a 403 or a blip degrades to the old
// behaviour for that project rather than hiding its jobs.
func bulkJobProjects(results []projectPipelines) []Project {
	out := make([]Project, 0, len(results))
	for _, r := range results {
		if r.err == nil && len(r.pipes) == 0 {
			continue
		}
		out = append(out, r.project)
	}
	return out
}

// treeNode is one pipeline to fetch while walking a root's parent→child tree,
// identified by (project path, iid) for the GraphQL lookup.
type treeNode struct {
	rootID      int64  // the top-level pipeline this subtree belongs to
	parentID    int64  // immediate parent pipeline id; 0 for a root
	projectPath string // this pipeline's project (a child may differ from its root)
	iid         int64  // this pipeline's per-project number
	// superseded marks a child a retried bridge triggered (or any pipeline
	// beneath one): a run its parent has since replaced.
	superseded bool
}

// fetchJobsTree fetches, via GraphQL, jobs for each root pipeline and — following
// each pipeline's downstream (bridge) children recursively — for its child
// pipelines too. Child pipelines don't appear in the project pipeline list, so
// their jobs are only reachable this way; each GraphQL call returns a pipeline's
// jobs (with needs:) plus its child refs in one round-trip.
//
// It returns all jobs plus, for the finished roots, which are settled (whole
// subtree terminal → mark done and stop) versus still pending (a descendant is
// active, or a fetch failed → keep polling next refresh). Active roots are
// omitted from both: they're re-polled via the store while they run.
//
// As a side effect it records the parent→child pipeline edges it discovers into
// p.childPipes/p.childParent (with each child's real project path and status from
// its own fetch), so the Current tab can reconstruct the active tree. Edges are
// recorded for every discovered child, even a finished one, so a finished child
// under a still-running root is retained — except a superseded one: the child a
// retried bridge triggered is still walked (its jobs ran on runners, so the
// store wants them) but gets no edge, and one recorded before the retry is
// removed, so the tree shows only the run that replaced it.
func (p *Poller) fetchJobsTree(ctx context.Context, roots []Pipeline) (jobs []Job, settled, pending []int64, errs []error) {
	type nodeKey struct {
		path string
		iid  int64
	}
	visited := map[nodeKey]bool{}
	rootFinished := map[int64]bool{}
	subtreeActive := map[int64]bool{} // rootID → a node in its subtree is active (or unfetched)

	var frontier []treeNode
	for _, pi := range roots {
		k := nodeKey{pi.ProjectPath, pi.IID}
		if visited[k] {
			continue
		}
		visited[k] = true
		rootFinished[pi.ID] = pi.Status.IsFinished()
		if !pi.Status.IsFinished() {
			subtreeActive[pi.ID] = true
		}
		frontier = append(frontier, treeNode{rootID: pi.ID, projectPath: pi.ProjectPath, iid: pi.IID})
	}

	for len(frontier) > 0 {
		type result struct {
			node     treeNode
			pipe     Pipeline
			jobs     []Job
			children []childRef
			ok       bool
			errs     []error
		}
		results := make([]result, len(frontier))

		sem := make(chan struct{}, p.opts.Concurrency)
		var wg sync.WaitGroup
		for i := range frontier {
			wg.Add(1)
			sem <- struct{}{}
			go func(i int) {
				defer wg.Done()
				defer func() { <-sem }()
				n := frontier[i]
				r := result{node: n}
				pipe, js, kids, err := p.fetchTree(ctx, n.projectPath, n.iid, jobFetchScopes)
				if err != nil {
					r.errs = append(r.errs, err)
				} else {
					r.ok = true
					r.pipe = pipe
					r.jobs = js
					r.children = kids
				}
				results[i] = r
			}(i)
		}
		wg.Wait()

		var next []treeNode
		for _, r := range results {
			jobs = append(jobs, r.jobs...)
			errs = append(errs, r.errs...)
			// A node we couldn't read leaves its subtree unsettled, so we retry.
			if !r.ok {
				subtreeActive[r.node.rootID] = true
				continue
			}
			// This node's pipeline id: the root's own id, or the id from the
			// fetched pipe for a discovered child.
			nodeID := r.node.rootID
			switch {
			case r.node.parentID != 0 && r.node.superseded:
				nodeID = r.pipe.ID
				delete(p.childPipes, nodeID)
				delete(p.childParent, nodeID)
			case r.node.parentID != 0:
				nodeID = r.pipe.ID
				// Record the child edge + metadata regardless of whether it's
				// finished, so the Current tree keeps a finished child.
				p.childPipes[nodeID] = r.pipe
				p.childParent[nodeID] = r.node.parentID
			}
			if !r.pipe.Status.IsFinished() {
				subtreeActive[r.node.rootID] = true
			}
			for _, cr := range r.children {
				if cr.iid == 0 {
					continue
				}
				k := nodeKey{cr.projectPath, cr.iid}
				if visited[k] {
					continue
				}
				visited[k] = true
				next = append(next, treeNode{
					rootID:      r.node.rootID,
					parentID:    nodeID,
					projectPath: cr.projectPath,
					iid:         cr.iid,
					superseded:  r.node.superseded || cr.retried,
				})
			}
		}
		frontier = next
	}

	for id, finished := range rootFinished {
		if !finished {
			continue // active root: re-polled via the store, not tracked here
		}
		if subtreeActive[id] {
			pending = append(pending, id)
		} else {
			settled = append(settled, id)
		}
	}
	return jobs, settled, pending, errs
}

// enrichDurations fills in pipeline durations from the detail endpoint, serving
// pipelines already stored as finished-with-duration from the retained store and
// bounding network work by MaxDetailFetch. It returns the number of pipelines
// left unenriched due to the cap (newest are enriched first). Distinct slice
// indices are written from separate goroutines, which is data-race-free.
func (p *Poller) enrichDurations(ctx context.Context, pipes []Pipeline) int {
	sort.Slice(pipes, func(i, j int) bool { return pipes[i].Updated.After(pipes[j].Updated) })

	var toFetch []int
	for i := range pipes {
		if cached, ok := p.pipes[pipes[i].ID]; ok && cached.Status.IsFinished() && cached.Duration > 0 {
			applyDetail(&pipes[i], cached)
			continue
		}
		toFetch = append(toFetch, i)
	}

	skipped := 0
	if len(toFetch) > p.opts.MaxDetailFetch {
		skipped = len(toFetch) - p.opts.MaxDetailFetch
		toFetch = toFetch[:p.opts.MaxDetailFetch]
	}

	sem := make(chan struct{}, p.opts.Concurrency)
	var wg sync.WaitGroup
	for _, idx := range toFetch {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()
			detail, err := p.client.GetPipeline(ctx, pipes[idx].ProjectID, pipes[idx].ID)
			if err != nil {
				return // leave unenriched; non-fatal
			}
			applyDetail(&pipes[idx], detail)
		}(idx)
	}
	wg.Wait()
	return skipped
}

func applyDetail(dst *Pipeline, detail Pipeline) {
	dst.Duration = detail.Duration
	dst.Started = detail.Started
	dst.Finished = detail.Finished
	dst.User = detail.User // the list endpoint omits the user; the detail one carries it
}
