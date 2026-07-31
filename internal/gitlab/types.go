package gitlab

import "time"

// Status is a GitLab CI status shared by pipelines and jobs.
type Status string

const (
	StatusCreated            Status = "created"
	StatusWaitingForResource Status = "waiting_for_resource"
	StatusPreparing          Status = "preparing"
	StatusPending            Status = "pending"
	StatusRunning            Status = "running"
	StatusSuccess            Status = "success"
	StatusFailed             Status = "failed"
	StatusCanceled           Status = "canceled"
	StatusSkipped            Status = "skipped"
	StatusManual             Status = "manual"
	StatusScheduled          Status = "scheduled"
)

// IsActive reports whether the status represents in-flight work.
func (s Status) IsActive() bool {
	switch s {
	case StatusCreated, StatusWaitingForResource, StatusPreparing,
		StatusPending, StatusRunning, StatusScheduled:
		return true
	}
	return false
}

// IsFinished reports whether the status represents a terminal outcome.
func (s Status) IsFinished() bool {
	switch s {
	case StatusSuccess, StatusFailed, StatusCanceled, StatusSkipped:
		return true
	}
	return false
}

// Project is a GitLab project (a single repository).
type Project struct {
	ID       int64
	Path     string // path_with_namespace, e.g. "org/team/repo"
	Name     string
	WebURL   string
	Archived bool
	// Products lists the configured product(s) whose watchlist matched this
	// project. It enables future per-product filtering; v1 aggregates ignore it.
	Products []string
}

// Pipeline is a CI pipeline run. Duration/Started/Finished are populated from
// the detail endpoint (the list endpoint omits them); Duration is 0 when
// unknown.
type Pipeline struct {
	ID          int64
	IID         int64 // per-project pipeline number, for GraphQL lookups (project + iid)
	ProjectID   int64
	ProjectPath string
	Ref         string
	SHA         string
	Status      Status
	Source      string
	User        string // username of whoever triggered the pipeline ("" if unknown)
	WebURL      string
	Created     time.Time
	Updated     time.Time
	Started     time.Time
	Finished    time.Time
	Duration    time.Duration
}

// Job is a single CI job within a pipeline.
type Job struct {
	ID            int64
	Name          string
	Stage         string
	Status        Status
	ProjectPath   string
	Ref           string
	PipelineID    int64
	WebURL        string
	FailureReason string
	Runner        string   // the runner the job ran on (its description, else name)
	Tags          []string // runner tags the job was invoked with (tags:), empty if none
	Needs         []string // names of the jobs this job depends on (needs:), empty if none/unknown
	Created       time.Time
	Started       time.Time
	Finished      time.Time
	Duration      time.Duration
	Queued        time.Duration
}

// PipelineAgg summarizes repeated runs of the same (project, ref) pipeline over
// the top-panel window.
type PipelineAgg struct {
	ProjectPath    string
	Ref            string
	Count          int
	Succeeded      int
	Failed         int
	AvgDuration    time.Duration // averaged over runs with a known duration
	KnownDurations int           // runs that contributed to AvgDuration
}

// SuccessRate is succeeded / (succeeded + failed); 0 when neither occurred.
func (a PipelineAgg) SuccessRate() float64 {
	if den := a.Succeeded + a.Failed; den > 0 {
		return float64(a.Succeeded) / float64(den)
	}
	return 0
}

// JobAgg summarizes repeated runs of the same (project, job name).
type JobAgg struct {
	ProjectPath    string
	Name           string
	Count          int
	Succeeded      int
	Failed         int
	AvgDuration    time.Duration
	KnownDurations int
}

// SuccessRate is succeeded / (succeeded + failed); 0 when neither occurred.
func (a JobAgg) SuccessRate() float64 {
	if den := a.Succeeded + a.Failed; den > 0 {
		return float64(a.Succeeded) / float64(den)
	}
	return 0
}

// PipelineStats summarizes a project's pipeline runs over the Top window,
// aggregated across all refs (the historical Pipelines tab analyses per project,
// not per ref). It powers both the "fails most often" and "slowest" panels — one
// grouping, two sorts. The duration fields describe the distribution over runs
// with a known duration (KnownDurations); DurMean is the arithmetic mean and
// DurP95 the 95th-percentile (nearest-rank) run.
type PipelineStats struct {
	ProjectPath    string
	Runs           int
	Succeeded      int
	Failed         int
	Canceled       int
	KnownDurations int
	DurMin         time.Duration
	DurMean        time.Duration
	DurP95         time.Duration
	DurMax         time.Duration
}

// FailRate is failed / runs (0 when there were no runs). Unlike SuccessRate it's
// over all runs, not just success+failed, so canceled/skipped runs dilute it —
// the "fails most often" panel wants "of everything that ran, how much failed".
func (s PipelineStats) FailRate() float64 {
	if s.Runs > 0 {
		return float64(s.Failed) / float64(s.Runs)
	}
	return 0
}

// ComputeAgg is total runner time consumed by a key (a product or a project)
// over the Top window: the sum of job durations, the honest non-admin proxy for
// "compute used" (each job occupies one runner for its duration). Pct is this
// key's share of the grand total across all keys.
type ComputeAgg struct {
	Key     string
	Runs    int // jobs that contributed
	Compute time.Duration
	Pct     float64
}

// JobStats summarizes the jobs grouped under one key — a runner, or a runner
// tag jobs were invoked with — over the Top window: how many ran, the runner
// time they consumed, their mean duration, and their queue-wait distribution
// (how long they waited to be picked up — a saturation signal).
// Means/percentiles are over jobs with a known value.
type JobStats struct {
	Key          string
	Jobs         int
	Failed       int
	Compute      time.Duration
	MeanDuration time.Duration
	MeanQueue    time.Duration
	P95Queue     time.Duration
}

// ActivePipeline is one node of the Current tab's tree: a pipeline together with
// the jobs that belong directly to it and its downstream child pipelines
// (recursively). Roots are the currently-active top-level pipelines; a root's
// subtree may include already-finished jobs and children, so the tab can show
// progress and context while the root is still running.
type ActivePipeline struct {
	Pipeline
	Jobs     []Job            // jobs of this pipeline itself (not its children)
	Children []ActivePipeline // downstream child pipelines, recursively
}

// Progress counts finished vs. total jobs across this node's whole subtree
// (its own jobs plus every descendant's), for the Current tab's progress column.
func (a ActivePipeline) Progress() (done, total int) {
	for _, j := range a.Jobs {
		total++
		if j.Status.IsFinished() {
			done++
		}
	}
	for _, c := range a.Children {
		cd, ct := c.Progress()
		done += cd
		total += ct
	}
	return done, total
}

// Snapshot is an immutable, point-in-time view of the watched products' CI
// state. One Refresh produces all panels from a single fetch pass, so the UI
// renders whatever the latest Snapshot holds.
type Snapshot struct {
	Projects int // count of resolved projects polled

	// Current is the tree of currently-active pipelines with their jobs and
	// child pipelines, powering the Current tab's live monitoring view.
	Current []ActivePipeline

	RunningPipelines []Pipeline
	RecentPipelines  []Pipeline
	TopPipelines     []PipelineAgg

	RunningJobs []Job
	RecentJobs  []Job
	TopJobs     []JobAgg

	// Historical Pipelines-tab analytics over the Top window, aggregated by
	// project/tag/runner (ref is deliberately dropped — the analysis is
	// per project, not per ref). PipelineStats feeds both the "fails most often"
	// and "slowest" panels; TagStats/RunnerStats compare job load (throughput,
	// compute, queue wait) grouped by runner tag resp. runner. The
	// ComputeByProduct/Project rollups feed only the `refresh` text preview.
	PipelineStats    []PipelineStats
	ComputeByProduct []ComputeAgg
	ComputeByProject []ComputeAgg
	RunnerStats      []JobStats
	TagStats         []JobStats

	UpdatedAt time.Time
	// Errors holds non-fatal, per-project failures from the refresh; the
	// Snapshot is still usable (partial data).
	Errors []error
}

// ProductSpec is the data-layer view of a configured product: a named set of
// GitLab groups and/or projects to watch. Callers map config.Product to this so
// the data layer stays decoupled from the config file format.
type ProductSpec struct {
	Name     string
	Groups   []string
	Projects []string
}
