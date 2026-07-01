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
