package gitlab

import (
	"context"
	"fmt"
	"time"
)

// Service produces point-in-time Snapshots of the watched products' CI state.
// The UI depends on this interface, so it can render against a fake without a
// live GitLab instance.
type Service interface {
	Refresh(ctx context.Context) (Snapshot, error)
}

var (
	_ Service = (*Poller)(nil)
	_ Service = FakeService{}
)

// FakeService returns a fixed Snapshot; useful for UI development and tests.
type FakeService struct {
	Snap Snapshot
	Err  error
}

// Refresh returns the canned snapshot and error.
func (f FakeService) Refresh(context.Context) (Snapshot, error) {
	return f.Snap, f.Err
}

// SampleSnapshot returns realistic fixture data anchored to the current time,
// so the UI (and `glute refresh --sample`) can be exercised without a live
// instance.
func SampleSnapshot() Snapshot {
	now := time.Now()
	url := func(path string, id int64) string {
		return fmt.Sprintf("https://gitlab.example.com/%s/-/pipelines/%d", path, id)
	}

	running := []Pipeline{
		{ID: 101, ProjectPath: "acme/payments/api", Ref: "main", Status: StatusRunning,
			Source: "push", WebURL: url("acme/payments/api", 101),
			Started: now.Add(-90 * time.Second), Created: now.Add(-95 * time.Second)},
		{ID: 102, ProjectPath: "acme/payments/web", Ref: "release/2.1", Status: StatusRunning,
			Source: "merge_request_event", WebURL: url("acme/payments/web", 102),
			Started: now.Add(-6 * time.Minute), Created: now.Add(-6 * time.Minute)},
		{ID: 103, ProjectPath: "acme/platform/gateway", Ref: "main", Status: StatusPending,
			Source: "schedule", WebURL: url("acme/platform/gateway", 103),
			Created: now.Add(-20 * time.Second)},
	}

	recent := []Pipeline{
		{ID: 98, ProjectPath: "acme/payments/api", Ref: "main", Status: StatusSuccess,
			Source: "push", Finished: now.Add(-12 * time.Minute), Duration: 5*time.Minute + 40*time.Second},
		{ID: 97, ProjectPath: "acme/platform/gateway", Ref: "feat/rate-limit", Status: StatusFailed,
			Source: "merge_request_event", Finished: now.Add(-38 * time.Minute), Duration: 3*time.Minute + 12*time.Second},
		{ID: 96, ProjectPath: "acme/payments/web", Ref: "main", Status: StatusSuccess,
			Source: "push", Finished: now.Add(-2 * time.Hour), Duration: 7 * time.Minute},
	}

	topPipe := []PipelineAgg{
		{ProjectPath: "acme/payments/api", Ref: "main", Count: 214, Succeeded: 198, Failed: 16,
			AvgDuration: 6*time.Minute + 12*time.Second, KnownDurations: 214},
		{ProjectPath: "acme/platform/gateway", Ref: "main", Count: 141, Succeeded: 120, Failed: 21,
			AvgDuration: 9*time.Minute + 3*time.Second, KnownDurations: 141},
		{ProjectPath: "acme/payments/web", Ref: "main", Count: 88, Succeeded: 85, Failed: 3,
			AvgDuration: 7*time.Minute + 30*time.Second, KnownDurations: 88},
	}

	runningJob := []Job{
		{ID: 5101, Name: "integration-tests", Stage: "test", Status: StatusRunning,
			ProjectPath: "acme/payments/api", Ref: "main", PipelineID: 101, Started: now.Add(-70 * time.Second)},
		{ID: 5102, Name: "build", Stage: "build", Status: StatusRunning,
			ProjectPath: "acme/payments/web", Ref: "release/2.1", PipelineID: 102, Started: now.Add(-4 * time.Minute)},
	}

	recentJob := []Job{
		{ID: 5098, Name: "unit-tests", Stage: "test", Status: StatusSuccess,
			ProjectPath: "acme/payments/api", Ref: "main", PipelineID: 98,
			Finished: now.Add(-13 * time.Minute), Duration: 2*time.Minute + 5*time.Second},
		{ID: 5097, Name: "deploy-staging", Stage: "deploy", Status: StatusFailed,
			ProjectPath: "acme/platform/gateway", Ref: "feat/rate-limit", PipelineID: 97,
			FailureReason: "script_failure", Finished: now.Add(-39 * time.Minute), Duration: 44 * time.Second},
	}

	topJob := []JobAgg{
		{ProjectPath: "acme/payments/api", Name: "unit-tests", Count: 214, Succeeded: 210, Failed: 4,
			AvgDuration: 2*time.Minute + 1*time.Second, KnownDurations: 214},
		{ProjectPath: "acme/payments/api", Name: "integration-tests", Count: 214, Succeeded: 190, Failed: 24,
			AvgDuration: 4*time.Minute + 20*time.Second, KnownDurations: 214},
		{ProjectPath: "acme/platform/gateway", Name: "build", Count: 141, Succeeded: 139, Failed: 2,
			AvgDuration: 1*time.Minute + 50*time.Second, KnownDurations: 141},
	}

	return Snapshot{
		Projects:         3,
		RunningPipelines: running,
		RecentPipelines:  recent,
		TopPipelines:     topPipe,
		RunningJobs:      runningJob,
		RecentJobs:       recentJob,
		TopJobs:          topJob,
		UpdatedAt:        now,
	}
}
