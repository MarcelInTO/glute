package gitlab

import (
	"math"
	"sort"
	"time"
)

// The functions below are pure: they derive the Snapshot panels from already
// fetched slices, so they're unit-tested without any network.

func pipeStart(p Pipeline) time.Time {
	if !p.Started.IsZero() {
		return p.Started
	}
	return p.Created
}

func pipeEnd(p Pipeline) time.Time {
	if !p.Finished.IsZero() {
		return p.Finished
	}
	return p.Updated
}

func jobStart(j Job) time.Time {
	if !j.Started.IsZero() {
		return j.Started
	}
	return j.Created
}

func jobEnd(j Job) time.Time {
	if !j.Finished.IsZero() {
		return j.Finished
	}
	return j.Started
}

func runningPipelines(pipes []Pipeline) []Pipeline {
	var out []Pipeline
	for _, p := range pipes {
		if p.Status.IsActive() {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return pipeStart(out[i]).After(pipeStart(out[j])) })
	return out
}

func recentPipelines(pipes []Pipeline, since time.Time) []Pipeline {
	var out []Pipeline
	for _, p := range pipes {
		if !p.Status.IsFinished() {
			continue
		}
		if pipeEnd(p).Before(since) {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return pipeEnd(out[i]).After(pipeEnd(out[j])) })
	return out
}

func topPipelines(pipes []Pipeline, limit int) []PipelineAgg {
	type key struct{ path, ref string }
	aggs := map[key]*PipelineAgg{}
	sums := map[key]time.Duration{}

	for _, p := range pipes {
		k := key{p.ProjectPath, p.Ref}
		agg := aggs[k]
		if agg == nil {
			agg = &PipelineAgg{ProjectPath: p.ProjectPath, Ref: p.Ref}
			aggs[k] = agg
		}
		agg.Count++
		switch p.Status {
		case StatusSuccess:
			agg.Succeeded++
		case StatusFailed:
			agg.Failed++
		}
		if p.Duration > 0 {
			sums[k] += p.Duration
			agg.KnownDurations++
		}
	}

	out := make([]PipelineAgg, 0, len(aggs))
	for k, agg := range aggs {
		if agg.KnownDurations > 0 {
			agg.AvgDuration = sums[k] / time.Duration(agg.KnownDurations)
		}
		out = append(out, *agg)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if out[i].ProjectPath != out[j].ProjectPath {
			return out[i].ProjectPath < out[j].ProjectPath
		}
		return out[i].Ref < out[j].Ref
	})
	return capAgg(out, limit)
}

// activePipelines builds the Current-tab tree: every active root pipeline with
// its own jobs and its downstream child pipelines, recursively. Roots come from
// the pipeline store (where child pipelines were dropped); child metadata and the
// parent→child edges are supplied from the retained tree that the job walk
// learns. Jobs attach to their pipeline by PipelineID; each node's jobs are
// ordered as the UI shows them (see sortPipelineJobs). Children are ordered by
// start time. A visited set guards against a malformed edge cycle.
func activePipelines(roots []Pipeline, jobs []Job, childPipes map[int64]Pipeline, childParent map[int64]int64) []ActivePipeline {
	jobsByPipe := map[int64][]Job{}
	for _, j := range jobs {
		jobsByPipe[j.PipelineID] = append(jobsByPipe[j.PipelineID], j)
	}
	kids := map[int64][]int64{}
	for child, parent := range childParent {
		kids[parent] = append(kids[parent], child)
	}

	seen := map[int64]bool{}
	var build func(pi Pipeline) ActivePipeline
	build = func(pi Pipeline) ActivePipeline {
		seen[pi.ID] = true
		node := ActivePipeline{Pipeline: pi, Jobs: sortPipelineJobs(jobsByPipe[pi.ID])}
		childIDs := append([]int64(nil), kids[pi.ID]...)
		sort.Slice(childIDs, func(i, j int) bool {
			ci, cj := childPipes[childIDs[i]], childPipes[childIDs[j]]
			if !pipeStart(ci).Equal(pipeStart(cj)) {
				return pipeStart(ci).Before(pipeStart(cj))
			}
			return childIDs[i] < childIDs[j]
		})
		for _, cid := range childIDs {
			cp, ok := childPipes[cid]
			if !ok || seen[cid] {
				continue
			}
			node.Children = append(node.Children, build(cp))
		}
		return node
	}

	var out []ActivePipeline
	for _, pi := range roots {
		if pi.Status.IsActive() {
			out = append(out, build(pi))
		}
	}
	sort.Slice(out, func(i, j int) bool { return pipeStart(out[i].Pipeline).After(pipeStart(out[j].Pipeline)) })
	return out
}

// sortPipelineJobs orders a pipeline's jobs the way they execute, matching the
// GitLab UI. When any job declares dependencies (needs:), it's a stable
// topological sort of the needs DAG — every job follows the jobs it needs, since
// dependencies drive execution order and override stages. With no needs anywhere
// it falls back to stage execution order.
//
// The order depends only on pipeline structure (names, needs, stage, id), never
// on job status or start time, so it does not reshuffle as jobs start.
//
// Stage fallback: the jobs API carries no stage index, but GitLab creates a
// pipeline's jobs stage by stage, so the lowest job ID in a stage tracks that
// stage's position; rank stages by that minimum, then order by name within a
// stage. This same key breaks ties between independent jobs in the topo sort.
func sortPipelineJobs(jobs []Job) []Job {
	out := append([]Job(nil), jobs...)

	stageRank := map[string]int64{}
	for _, j := range out {
		if r, ok := stageRank[j.Stage]; !ok || j.ID < r {
			stageRank[j.Stage] = j.ID
		}
	}
	// less is the structural order used both as the no-needs fallback and as the
	// tie-break among jobs that are ready together in the topo sort.
	less := func(a, b Job) bool {
		if ra, rb := stageRank[a.Stage], stageRank[b.Stage]; ra != rb {
			return ra < rb
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ID < b.ID
	}

	for _, j := range out {
		if len(j.Needs) > 0 {
			return topoSortJobs(out, less)
		}
	}
	sort.Slice(out, func(i, j int) bool { return less(out[i], out[j]) })
	return out
}

// topoSortJobs orders jobs by dependency depth, then by less. A job's depth is
// the longest needs-chain ending at it (0 for a job with no needs within the
// set); since a job's depth always exceeds each of its needs' depths, ordering
// by depth is a valid topological order — every job follows the jobs it needs —
// and it also groups each dependency layer together, matching the GitLab UI's
// layered DAG view. Within a layer (mutually independent jobs) less decides.
//
// A need naming a job outside this set is ignored (optional or cross-pipeline).
// A malformed cycle can't stall it: the depth walk treats a back-edge to an
// in-progress job as absent, so depths stay finite and every job is placed.
func topoSortJobs(jobs []Job, less func(a, b Job) bool) []Job {
	byName := make(map[string]int, len(jobs))
	for i, j := range jobs {
		byName[j.Name] = i
	}

	const (
		unvisited = iota
		inProgress
		done
	)
	depth := make([]int, len(jobs))
	state := make([]int8, len(jobs))
	var compute func(i int) int
	compute = func(i int) int {
		if state[i] == done {
			return depth[i]
		}
		state[i] = inProgress // a need pointing back to an in-progress job is a cycle edge, skipped
		d := 0
		for _, need := range jobs[i].Needs {
			n, ok := byName[need]
			if !ok || n == i || state[n] == inProgress {
				continue
			}
			if nd := compute(n) + 1; nd > d {
				d = nd
			}
		}
		depth[i] = d
		state[i] = done
		return d
	}
	for i := range jobs {
		compute(i)
	}

	idx := make([]int, len(jobs))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		ia, ib := idx[a], idx[b]
		if depth[ia] != depth[ib] {
			return depth[ia] < depth[ib]
		}
		return less(jobs[ia], jobs[ib])
	})
	out := make([]Job, len(jobs))
	for k, i := range idx {
		out[k] = jobs[i]
	}
	return out
}

func runningJobs(jobs []Job) []Job {
	var out []Job
	for _, j := range jobs {
		if j.Status.IsActive() {
			out = append(out, j)
		}
	}
	sort.Slice(out, func(i, k int) bool { return jobStart(out[i]).After(jobStart(out[k])) })
	return out
}

func recentJobs(jobs []Job, since time.Time) []Job {
	var out []Job
	for _, j := range jobs {
		if !j.Status.IsFinished() {
			continue
		}
		if jobEnd(j).Before(since) {
			continue
		}
		out = append(out, j)
	}
	sort.Slice(out, func(i, k int) bool { return jobEnd(out[i]).After(jobEnd(out[k])) })
	return out
}

func topJobs(jobs []Job, limit int) []JobAgg {
	type key struct{ path, name string }
	aggs := map[key]*JobAgg{}
	sums := map[key]time.Duration{}

	for _, j := range jobs {
		k := key{j.ProjectPath, j.Name}
		agg := aggs[k]
		if agg == nil {
			agg = &JobAgg{ProjectPath: j.ProjectPath, Name: j.Name}
			aggs[k] = agg
		}
		agg.Count++
		switch j.Status {
		case StatusSuccess:
			agg.Succeeded++
		case StatusFailed:
			agg.Failed++
		}
		if j.Duration > 0 {
			sums[k] += j.Duration
			agg.KnownDurations++
		}
	}

	out := make([]JobAgg, 0, len(aggs))
	for k, agg := range aggs {
		if agg.KnownDurations > 0 {
			agg.AvgDuration = sums[k] / time.Duration(agg.KnownDurations)
		}
		out = append(out, *agg)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if out[i].ProjectPath != out[j].ProjectPath {
			return out[i].ProjectPath < out[j].ProjectPath
		}
		return out[i].Name < out[j].Name
	})
	return capAggJobs(out, limit)
}

// pipelineStats aggregates pipeline runs by project (ref dropped) over the whole
// slice, computing each project's run/outcome tallies and its duration
// distribution across runs with a known duration. It's the single source for the
// Pipelines tab's "fails most often" and "slowest" panels — each sorts this its
// own way — so it returns every project ordered only by path, for a stable base.
func pipelineStats(pipes []Pipeline) []PipelineStats {
	type acc struct {
		stats PipelineStats
		durs  []time.Duration
	}
	byPath := map[string]*acc{}
	for _, p := range pipes {
		a := byPath[p.ProjectPath]
		if a == nil {
			a = &acc{stats: PipelineStats{ProjectPath: p.ProjectPath}}
			byPath[p.ProjectPath] = a
		}
		a.stats.Runs++
		switch p.Status {
		case StatusSuccess:
			a.stats.Succeeded++
		case StatusFailed:
			a.stats.Failed++
		case StatusCanceled:
			a.stats.Canceled++
		}
		if p.Duration > 0 {
			a.durs = append(a.durs, p.Duration)
		}
	}

	out := make([]PipelineStats, 0, len(byPath))
	for _, a := range byPath {
		sort.Slice(a.durs, func(i, j int) bool { return a.durs[i] < a.durs[j] })
		a.stats.KnownDurations = len(a.durs)
		if n := len(a.durs); n > 0 {
			var sum time.Duration
			for _, d := range a.durs {
				sum += d
			}
			a.stats.DurMin = a.durs[0]
			a.stats.DurMax = a.durs[n-1]
			a.stats.DurMean = sum / time.Duration(n)
			a.stats.DurP95 = percentile(a.durs, 0.95)
		}
		out = append(out, a.stats)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ProjectPath < out[j].ProjectPath })
	return out
}

// computeByProject sums job runner-time by project over the whole slice. Every
// job maps to exactly one project, so the Pct column sums to 100%.
func computeByProject(jobs []Job) []ComputeAgg {
	return computeAgg(jobs, func(j Job) []string {
		if j.ProjectPath == "" {
			return nil
		}
		return []string{j.ProjectPath}
	})
}

// computeByProduct sums job runner-time by product, mapping each job's project to
// the product(s) that watch it. A project may belong to more than one product, so
// a shared project's jobs count toward each — which means the Pct column can
// exceed 100% under overlap (each row's share is still of the one true grand
// total). Jobs whose project maps to no product (e.g. a cross-project downstream
// child outside the watchlist) contribute to the grand total but to no row.
func computeByProduct(jobs []Job, projectProducts map[string][]string) []ComputeAgg {
	return computeAgg(jobs, func(j Job) []string {
		return projectProducts[j.ProjectPath]
	})
}

// computeAgg sums each job's duration (its runner-time) into every key that
// keysOf maps it to, and computes each key's Pct of the grand total — the sum of
// every job's duration counted once, independent of keying. Grouping the same
// jobs by a partition (one key each) therefore yields Pcts summing to 100%;
// grouping by an overlapping mapping can exceed it. A job with no known duration
// still counts toward Runs but adds no time.
func computeAgg(jobs []Job, keysOf func(Job) []string) []ComputeAgg {
	byKey := map[string]*ComputeAgg{}
	var total time.Duration
	for _, j := range jobs {
		total += j.Duration
		for _, k := range keysOf(j) {
			a := byKey[k]
			if a == nil {
				a = &ComputeAgg{Key: k}
				byKey[k] = a
			}
			a.Runs++
			a.Compute += j.Duration
		}
	}

	out := make([]ComputeAgg, 0, len(byKey))
	for _, a := range byKey {
		if total > 0 {
			a.Pct = float64(a.Compute) / float64(total) * 100
		}
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Compute != out[j].Compute {
			return out[i].Compute > out[j].Compute
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// untaggedKey buckets jobs invoked with no runner tags in tagStats, so untagged
// load stays visible next to the tagged kinds rather than silently vanishing.
const untaggedKey = "(untagged)"

// runnerStats groups job load by the runner that ran each job.
func runnerStats(jobs []Job) []JobStats {
	return jobStats(jobs, func(j Job) []string { return []string{j.Runner} })
}

// tagStats groups job load by the runner tags jobs were invoked with — the tags
// decide which runners may pick a job up, so this shows what each requested
// kind of capacity costs and how saturated it is. A multi-tagged job counts
// toward each of its tags (rows overlap, like computeByProduct); a job with no
// tags lands in the untaggedKey bucket. Same job population as runnerStats, so
// the two panels are two groupings of the same work.
func tagStats(jobs []Job) []JobStats {
	return jobStats(jobs, func(j Job) []string {
		if len(j.Tags) == 0 {
			return []string{untaggedKey}
		}
		return j.Tags
	})
}

// jobStats aggregates jobs into per-key load stats, over the whole slice:
// throughput (Jobs), runner-time consumed (Compute), mean job duration, and the
// queue-wait distribution (mean + p95 of how long jobs waited to be picked up —
// a saturation signal). keysOf maps a job to every key it counts toward. Only
// jobs a runner has picked up count (Runner != ""): a still-queued job has no
// duration or queue wait to measure yet. Means/percentiles are over jobs with a
// known value. Ordered by Compute descending (the biggest consumers first).
func jobStats(jobs []Job, keysOf func(Job) []string) []JobStats {
	type acc struct {
		stats    JobStats
		durSum   time.Duration
		durN     int
		queues   []time.Duration
		queueSum time.Duration
	}
	byKey := map[string]*acc{}
	for _, j := range jobs {
		if j.Runner == "" {
			continue
		}
		for _, k := range keysOf(j) {
			a := byKey[k]
			if a == nil {
				a = &acc{stats: JobStats{Key: k}}
				byKey[k] = a
			}
			a.stats.Jobs++
			if j.Status == StatusFailed {
				a.stats.Failed++
			}
			a.stats.Compute += j.Duration
			if j.Duration > 0 {
				a.durSum += j.Duration
				a.durN++
			}
			if j.Queued > 0 {
				a.queues = append(a.queues, j.Queued)
				a.queueSum += j.Queued
			}
		}
	}

	out := make([]JobStats, 0, len(byKey))
	for _, a := range byKey {
		if a.durN > 0 {
			a.stats.MeanDuration = a.durSum / time.Duration(a.durN)
		}
		if n := len(a.queues); n > 0 {
			sort.Slice(a.queues, func(i, j int) bool { return a.queues[i] < a.queues[j] })
			a.stats.MeanQueue = a.queueSum / time.Duration(n)
			a.stats.P95Queue = percentile(a.queues, 0.95)
		}
		out = append(out, a.stats)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Compute != out[j].Compute {
			return out[i].Compute > out[j].Compute
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// windowSteps are the standard history lookbacks the TUI's window key cycles
// through; selectableWindows caps them at the configured Top window, since the
// store holds nothing older.
var windowSteps = []time.Duration{24 * time.Hour, 7 * 24 * time.Hour, 30 * 24 * time.Hour}

// selectableWindows returns the lookbacks a Snapshot carries history for: every
// windowStep shorter than top, ascending, then top itself — so the full window
// is always the last (and the default) choice. With the default 30d Top window
// that's 1d / 7d / 30d; a 14d window yields 1d / 7d / 14d, and a window of a day
// or less yields just itself.
func selectableWindows(top time.Duration) []time.Duration {
	var out []time.Duration
	for _, w := range windowSteps {
		if w < top {
			out = append(out, w)
		}
	}
	return append(out, top)
}

// windowSlices narrows the store to the entries within a lookback starting at
// since, keyed on the same timestamps eviction uses (pipeline updated_at, job
// created_at; a job with no created_at is kept, as evict keeps it). A shorter
// window is thus exactly what the store would hold had it been configured that
// wide, so its panels are consistent with the full window's.
func windowSlices(pipes []Pipeline, jobs []Job, since time.Time) ([]Pipeline, []Job) {
	ps := make([]Pipeline, 0, len(pipes))
	for _, p := range pipes {
		if !p.Updated.Before(since) {
			ps = append(ps, p)
		}
	}
	js := make([]Job, 0, len(jobs))
	for _, j := range jobs {
		if j.Created.IsZero() || !j.Created.Before(since) {
			js = append(js, j)
		}
	}
	return ps, js
}

// windowStats derives every history aggregate over one window's slices. It is
// the single place the Work/Infrastructure panels and the preview's rollups are
// computed from, so each selectable window gets the identical set.
func windowStats(w time.Duration, pipes []Pipeline, jobs []Job, projectProducts map[string][]string, limit int) WindowStats {
	return WindowStats{
		Window:           w,
		TopPipelines:     topPipelines(pipes, limit),
		TopJobs:          topJobs(jobs, limit),
		PipelineStats:    pipelineStats(pipes),
		ComputeByProduct: computeByProduct(jobs, projectProducts),
		ComputeByProject: computeByProject(jobs),
		RunnerStats:      runnerStats(jobs),
		TagStats:         tagStats(jobs),
	}
}

// historyWindows computes windowStats at each selectable lookback up to top
// (ascending; the last covers the whole store). pipes and jobs are the full
// Top-window store, already evicted to top, so only the shorter windows filter.
// Aggregation is milliseconds even on a busy 30d store, so computing every
// window per refresh is what lets the window key be an instant view switch.
func historyWindows(now time.Time, top time.Duration, pipes []Pipeline, jobs []Job, projectProducts map[string][]string, limit int) []WindowStats {
	windows := selectableWindows(top)
	out := make([]WindowStats, 0, len(windows))
	for _, w := range windows {
		ps, js := pipes, jobs
		if w < top {
			ps, js = windowSlices(pipes, jobs, now.Add(-w))
		}
		out = append(out, windowStats(w, ps, js, projectProducts, limit))
	}
	return out
}

// percentile returns the p-th percentile (p in [0,1]) of an ascending-sorted
// duration slice by the nearest-rank method, or 0 for an empty slice. p95 of a
// small sample is its max — deliberately, since the panels want the worst run a
// user actually feels, not an interpolated estimate.
func percentile(sorted []time.Duration, p float64) time.Duration {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	rank := int(math.Ceil(p * float64(n)))
	switch {
	case rank < 1:
		rank = 1
	case rank > n:
		rank = n
	}
	return sorted[rank-1]
}

func capAgg(s []PipelineAgg, limit int) []PipelineAgg {
	if limit > 0 && len(s) > limit {
		return s[:limit]
	}
	return s
}

func capAggJobs(s []JobAgg, limit int) []JobAgg {
	if limit > 0 && len(s) > limit {
		return s[:limit]
	}
	return s
}
