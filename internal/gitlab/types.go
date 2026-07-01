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
	ProjectID   int64
	ProjectPath string
	Ref         string
	SHA         string
	Status      Status
	Source      string
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

// Snapshot is an immutable, point-in-time view of the watched products' CI
// state. One Refresh produces all six panels from a single fetch pass, so the
// UI renders whatever the latest Snapshot holds.
type Snapshot struct {
	Projects int // count of resolved projects polled

	RunningPipelines []Pipeline
	RecentPipelines  []Pipeline
	TopPipelines     []PipelineAgg

	RunningJobs []Job
	RecentJobs  []Job
	TopJobs     []JobAgg

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
