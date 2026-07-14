package gitlab

import (
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
