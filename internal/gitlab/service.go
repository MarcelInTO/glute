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
			Source: "push", User: "jchen", WebURL: url("acme/payments/api", 101),
			Started: now.Add(-90 * time.Second), Created: now.Add(-95 * time.Second)},
		{ID: 102, ProjectPath: "acme/payments/web", Ref: "release/2.1", Status: StatusRunning,
			Source: "merge_request_event", User: "priya", WebURL: url("acme/payments/web", 102),
			Started: now.Add(-6 * time.Minute), Created: now.Add(-6 * time.Minute)},
		{ID: 103, ProjectPath: "acme/platform/gateway", Ref: "main", Status: StatusPending,
			Source: "schedule", User: "release-bot", WebURL: url("acme/platform/gateway", 103),
			Created: now.Add(-20 * time.Second)},
	}

	recent := []Pipeline{
		{ID: 98, ProjectPath: "acme/payments/api", Ref: "main", Status: StatusSuccess,
			Source: "push", User: "jchen", Finished: now.Add(-12 * time.Minute), Duration: 5*time.Minute + 40*time.Second},
		{ID: 97, ProjectPath: "acme/platform/gateway", Ref: "feat/rate-limit", Status: StatusFailed,
			Source: "merge_request_event", User: "priya", Finished: now.Add(-38 * time.Minute), Duration: 3*time.Minute + 12*time.Second},
		{ID: 96, ProjectPath: "acme/payments/web", Ref: "main", Status: StatusSuccess,
			Source: "push", User: "amir", Finished: now.Add(-2 * time.Hour), Duration: 7 * time.Minute},
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
		{ID: 5103, Name: "deploy-prod", Stage: "deploy", Status: StatusPending,
			ProjectPath: "acme/platform/gateway", Ref: "main", PipelineID: 103, Created: now.Add(-15 * time.Second)},
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

	// Current mirrors the running pipelines above as a tree: the API root has a
	// finished build job, two live test jobs, and a downstream deploy pipeline
	// (a child in another project) that's still pending.
	current := []ActivePipeline{
		{
			Pipeline: running[0], // acme/payments/api · main · running
			Jobs: []Job{
				// Job IDs ascend with stage/creation order (build < test), as real
				// GitLab assigns them, so the Current tree lists stages in
				// execution order (see sortPipelineJobs).
				{ID: 5111, Name: "compile", Stage: "build", Status: StatusSuccess,
					ProjectPath: "acme/payments/api", PipelineID: 101, Runner: "shared-linux-01",
					Started: now.Add(-90 * time.Second), Finished: now.Add(-45 * time.Second), Duration: 45 * time.Second},
				{ID: 5112, Name: "integration-tests", Stage: "test", Status: StatusPending,
					ProjectPath: "acme/payments/api", PipelineID: 101, Created: now.Add(-42 * time.Second)},
				{ID: 5113, Name: "unit-tests", Stage: "test", Status: StatusRunning,
					ProjectPath: "acme/payments/api", PipelineID: 101, Runner: "shared-linux-02", Started: now.Add(-42 * time.Second)},
			},
			Children: []ActivePipeline{
				{
					Pipeline: Pipeline{ID: 201, ProjectPath: "acme/payments/deploy", Ref: "main",
						Status: StatusPending, Source: sourceParentPipeline, User: "jchen", Created: now.Add(-30 * time.Second)},
					Jobs: []Job{
						{ID: 5201, Name: "deploy-staging", Stage: "deploy", Status: StatusPending,
							ProjectPath: "acme/payments/deploy", PipelineID: 201, Created: now.Add(-30 * time.Second)},
					},
				},
			},
		},
		{
			Pipeline: running[1], // acme/payments/web · release/2.1 · running
			Jobs: []Job{
				{ID: 5102, Name: "build", Stage: "build", Status: StatusRunning,
					ProjectPath: "acme/payments/web", PipelineID: 102, Runner: "docker-builder", Started: now.Add(-4 * time.Minute)},
			},
		},
		{
			Pipeline: running[2], // acme/platform/gateway · main · pending
		},
	}

	// Historical Pipelines-tab analytics: per-project pipeline stats (durations
	// stay min ≤ mean ≤ p95 ≤ max), product/project compute rollups, and per-runner
	// and per-tag load. Hand-authored (not derived) so the tab has realistic
	// content under `--sample` without a window of history to aggregate.
	pipeStats := []PipelineStats{
		{ProjectPath: "acme/payments/api", Runs: 214, Succeeded: 198, Failed: 16, KnownDurations: 214,
			DurMin: 3 * time.Minute, DurMean: 6*time.Minute + 12*time.Second, DurP95: 9 * time.Minute, DurMax: 12*time.Minute + 30*time.Second},
		{ProjectPath: "acme/platform/gateway", Runs: 141, Succeeded: 120, Failed: 21, KnownDurations: 141,
			DurMin: 4 * time.Minute, DurMean: 9*time.Minute + 3*time.Second, DurP95: 14 * time.Minute, DurMax: 22 * time.Minute},
		{ProjectPath: "acme/payments/web", Runs: 88, Succeeded: 85, Failed: 3, KnownDurations: 88,
			DurMin: 5 * time.Minute, DurMean: 7*time.Minute + 30*time.Second, DurP95: 11 * time.Minute, DurMax: 15 * time.Minute},
	}

	computeByProduct := []ComputeAgg{
		{Key: "Payments", Runs: 6200, Compute: 212 * time.Hour, Pct: 71},
		{Key: "Platform", Runs: 2100, Compute: 88 * time.Hour, Pct: 29},
	}
	computeByProject := []ComputeAgg{
		{Key: "acme/payments/api", Runs: 3900, Compute: 138 * time.Hour, Pct: 46},
		{Key: "acme/platform/gateway", Runs: 2100, Compute: 88 * time.Hour, Pct: 29},
		{Key: "acme/payments/web", Runs: 2300, Compute: 74 * time.Hour, Pct: 25},
	}

	runnerStats := []JobStats{
		{Key: "shared-linux-01", Jobs: 3400, Failed: 40, Compute: 120 * time.Hour,
			MeanDuration: 2*time.Minute + 6*time.Second, MeanQueue: 4 * time.Second, P95Queue: 30 * time.Second},
		{Key: "shared-linux-02", Jobs: 2600, Failed: 30, Compute: 95 * time.Hour,
			MeanDuration: 2*time.Minute + 11*time.Second, MeanQueue: 6 * time.Second, P95Queue: 45 * time.Second},
		{Key: "docker-builder", Jobs: 800, Failed: 12, Compute: 60 * time.Hour,
			MeanDuration: 4*time.Minute + 30*time.Second, MeanQueue: 12 * time.Second, P95Queue: 90 * time.Second},
	}

	tagStats := []JobStats{
		{Key: "linux", Jobs: 5200, Failed: 58, Compute: 185 * time.Hour,
			MeanDuration: 2*time.Minute + 8*time.Second, MeanQueue: 5 * time.Second, P95Queue: 35 * time.Second},
		{Key: "docker", Jobs: 1900, Failed: 21, Compute: 82 * time.Hour,
			MeanDuration: 2*time.Minute + 35*time.Second, MeanQueue: 9 * time.Second, P95Queue: 60 * time.Second},
		{Key: "windows", Jobs: 600, Failed: 9, Compute: 55 * time.Hour,
			MeanDuration: 5*time.Minute + 30*time.Second, MeanQueue: 40 * time.Second, P95Queue: 4 * time.Minute},
		{Key: "(untagged)", Jobs: 1100, Failed: 14, Compute: 33 * time.Hour,
			MeanDuration: 1*time.Minute + 48*time.Second, MeanQueue: 3 * time.Second, P95Queue: 20 * time.Second},
	}

	return Snapshot{
		Projects:         3,
		Current:          current,
		RunningPipelines: running,
		RecentPipelines:  recent,
		TopPipelines:     topPipe,
		RunningJobs:      runningJob,
		RecentJobs:       recentJob,
		TopJobs:          topJob,
		PipelineStats:    pipeStats,
		ComputeByProduct: computeByProduct,
		ComputeByProject: computeByProject,
		RunnerStats:      runnerStats,
		TagStats:         tagStats,
		UpdatedAt:        now,
	}
}
