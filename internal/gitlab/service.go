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
	// PipelineTree returns one pipeline's full tree — its jobs and downstream
	// child pipelines, recursively — for the finished-pipeline detail view. It
	// is the one thing not served from a Snapshot: it's fetched on demand (see
	// tree.go for why the retained store can't supply it), and it may be called
	// concurrently with Refresh.
	PipelineTree(ctx context.Context, root Pipeline) (ActivePipeline, error)
}

var (
	_ Service = (*Poller)(nil)
	_ Service = FakeService{}
)

// FakeService returns a fixed Snapshot; useful for UI development and tests.
type FakeService struct {
	Snap Snapshot
	Err  error
	// Trees are the canned PipelineTree answers, keyed by root pipeline id.
	Trees map[int64]ActivePipeline
}

// Refresh returns the canned snapshot and error.
func (f FakeService) Refresh(context.Context) (Snapshot, error) {
	return f.Snap, f.Err
}

// PipelineTree returns the canned tree for root, or failing that the canned
// error, or else root alone — a pipeline with no jobs to show.
func (f FakeService) PipelineTree(_ context.Context, root Pipeline) (ActivePipeline, error) {
	if t, ok := f.Trees[root.ID]; ok {
		return t, nil
	}
	if f.Err != nil {
		return ActivePipeline{}, f.Err
	}
	return ActivePipeline{Pipeline: root}, nil
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
		{ID: 98, ProjectPath: "acme/payments/api", Ref: "main", Status: StatusSuccess, WebURL: url("acme/payments/api", 98),
			Source: "push", User: "jchen", Finished: now.Add(-12 * time.Minute), Duration: 5*time.Minute + 40*time.Second},
		{ID: 97, ProjectPath: "acme/platform/gateway", Ref: "feat/rate-limit", Status: StatusFailed, WebURL: url("acme/platform/gateway", 97),
			Source: "merge_request_event", User: "priya", Finished: now.Add(-38 * time.Minute), Duration: 3*time.Minute + 12*time.Second},
		{ID: 96, ProjectPath: "acme/payments/web", Ref: "main", Status: StatusSuccess, WebURL: url("acme/payments/web", 96),
			Source: "push", User: "amir", Finished: now.Add(-2 * time.Hour), Duration: 7 * time.Minute},
		// Only three finished in the last day, so the list reaches back past it
		// to the ten that RecentMin asks for.
		{ID: 95, ProjectPath: "acme/payments/api", Ref: "refs/merge-requests/412/head", Status: StatusSuccess, WebURL: url("acme/payments/api", 95),
			Source: "merge_request_event", User: "amir", Finished: now.Add(-26 * time.Hour), Duration: 5*time.Minute + 52*time.Second},
		{ID: 94, ProjectPath: "acme/infra/nightly-backup", Ref: "main", Status: StatusFailed, WebURL: url("acme/infra/nightly-backup", 94),
			Source: "schedule", User: "release-bot", Finished: now.Add(-50 * time.Hour), Duration: 12*time.Minute + 30*time.Second},
		{ID: 93, ProjectPath: "acme/payments/api", Ref: "main", Status: StatusSuccess, WebURL: url("acme/payments/api", 93),
			Source: "push", User: "jchen", Finished: now.Add(-53 * time.Hour), Duration: 6*time.Minute + 5*time.Second},
		{ID: 92, ProjectPath: "acme/platform/gateway", Ref: "main", Status: StatusSuccess, WebURL: url("acme/platform/gateway", 92),
			Source: "push", User: "priya", Finished: now.Add(-70 * time.Hour), Duration: 8*time.Minute + 47*time.Second},
		{ID: 91, ProjectPath: "acme/payments/web", Ref: "feat/checkout-v2", Status: StatusCanceled, WebURL: url("acme/payments/web", 91),
			Source: "push", User: "priya", Finished: now.Add(-75 * time.Hour), Duration: 1*time.Minute + 20*time.Second},
		{ID: 89, ProjectPath: "acme/infra/nightly-backup", Ref: "main", Status: StatusFailed, WebURL: url("acme/infra/nightly-backup", 89),
			Source: "schedule", User: "release-bot", Finished: now.Add(-98 * time.Hour), Duration: 12*time.Minute + 2*time.Second},
		{ID: 88, ProjectPath: "acme/payments/web", Ref: "main", Status: StatusSuccess, WebURL: url("acme/payments/web", 88),
			Source: "push", User: "amir", Finished: now.Add(-120 * time.Hour), Duration: 7*time.Minute + 12*time.Second},
	}

	// The refs still red, newest first by when each went red: the gateway's
	// fresh failure on a branch, the scheduled backup failing every night
	// since #89, then two that have gone quiet — a feature branch nobody came
	// back to, and a main with no success anywhere in the window.
	recentByID := func(id int64) Pipeline {
		for _, p := range recent {
			if p.ID == id {
				return p
			}
		}
		panic(fmt.Sprintf("no sample pipeline #%d", id))
	}
	stale := func(id int64, path, ref, user string, ago time.Duration) Pipeline {
		return Pipeline{ID: id, ProjectPath: path, Ref: ref, Status: StatusFailed, Source: "push", User: user,
			WebURL: url(path, id), Finished: now.Add(-ago), Duration: 4*time.Minute + 9*time.Second}
	}
	failing := []FailingRef{
		{ProjectPath: "acme/platform/gateway", Ref: "feat/rate-limit", Since: recentByID(97).Finished, Latest: recentByID(97)},
		{ProjectPath: "acme/infra/nightly-backup", Ref: "main", Since: recentByID(89).Finished, Latest: recentByID(94)},
		{ProjectPath: "acme/payments/web", Ref: "feat/old-checkout", Since: now.Add(-12 * 24 * time.Hour),
			Latest: stale(70, "acme/payments/web", "feat/old-checkout", "amir", 12*24*time.Hour)},
		{ProjectPath: "acme/legacy/reports", Ref: "main", Since: now.Add(-26 * 24 * time.Hour),
			Latest: stale(61, "acme/legacy/reports", "main", "release-bot", 19*24*time.Hour)},
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
					Pipeline: Pipeline{ID: 201, ProjectPath: "acme/payments/deploy", Ref: "main", WebURL: url("acme/payments/deploy", 201),
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

	full := WindowStats{
		Window:           30 * 24 * time.Hour,
		TopPipelines:     topPipe,
		TopJobs:          topJob,
		PipelineStats:    pipeStats,
		ComputeByProduct: computeByProduct,
		ComputeByProject: computeByProject,
		RunnerStats:      runnerStats,
		TagStats:         tagStats,
	}

	// Every sample job links to its page, as real ones do (both fetch paths
	// carry it), so `o` on a job row has somewhere to go under --sample.
	var linkJobs func(aps []ActivePipeline)
	linkJobs = func(aps []ActivePipeline) {
		for i := range aps {
			for j := range aps[i].Jobs {
				aps[i].Jobs[j].WebURL = sampleJobURL(aps[i].Jobs[j])
			}
			linkJobs(aps[i].Children)
		}
	}
	linkJobs(current)

	return Snapshot{
		Projects:         3,
		Current:          current,
		RunningPipelines: running,
		RecentPipelines:  recent,
		FailingRefs:      failing,
		RunningJobs:      runningJob,
		RecentJobs:       recentJob,
		WindowStats:      full,
		// The shorter windows are scaled down from the full one so the window key
		// visibly changes the history panels under --sample.
		Windows: []WindowStats{
			scaleWindowStats(full, 24*time.Hour),
			scaleWindowStats(full, 7*24*time.Hour),
			full,
		},
		UpdatedAt: now,
	}
}

// SampleTrees returns the detail view's tree for each sample pipeline it can
// open — the recent (finished) ones, and each failing ref's latest
// failure: jobs timed so the timeline reads true — parallel jobs overlapping,
// queue waits, jobs that never ran — with a downstream child pipeline under
// one and a failure with a skipped job behind it in another. The rest are a
// plain build-then-test run. Times are anchored to each pipeline's own
// Finished and Duration, so the tree agrees with the panels. Sample data only.
func SampleTrees(s Snapshot) map[int64]ActivePipeline {
	ms := func(m, sec int) time.Duration { return time.Duration(m)*time.Minute + time.Duration(sec)*time.Second }
	trees := map[int64]ActivePipeline{}
	pipes := append([]Pipeline(nil), s.RecentPipelines...)
	for _, f := range s.FailingRefs {
		pipes = append(pipes, f.Latest)
	}
	for _, p := range pipes {
		if _, done := trees[p.ID]; done {
			continue
		}
		start := p.Finished.Add(-p.Duration)
		p.Started, p.Created = start, start.Add(-12*time.Second) // time to the first runner pickup
		// job builds one of p's own jobs, run from offset from (into the
		// pipeline) for dur; a negative from is a job that never ran.
		job := func(id int64, stage, name string, st Status, runner string, queued, from, dur time.Duration, needs ...string) Job {
			j := Job{ID: id, Name: name, Stage: stage, Status: st, ProjectPath: p.ProjectPath, Ref: p.Ref,
				PipelineID: p.ID, Runner: runner, Needs: needs, Created: p.Created}
			j.WebURL = sampleJobURL(j)
			if from >= 0 {
				j.Queued, j.Started, j.Finished, j.Duration = queued, start.Add(from), start.Add(from+dur), dur
			}
			return j
		}

		tree := ActivePipeline{Pipeline: p}
		switch p.ID {
		case 98: // acme/payments/api · main · success: a needs: DAG plus a deploy child that waits for a runner
			tree.Jobs = sortPipelineJobs([]Job{
				job(9801, "build", "compile", StatusSuccess, "shared-linux-01", ms(0, 4), 0, ms(1, 10)),
				job(9802, "test", "lint", StatusSuccess, "shared-linux-02", ms(0, 3), ms(0, 2), ms(0, 35)),
				job(9803, "test", "unit-tests", StatusSuccess, "shared-linux-02", ms(0, 6), ms(1, 16), ms(2, 5), "compile"),
				job(9804, "test", "integration-tests", StatusSuccess, "shared-linux-01", ms(0, 8), ms(1, 18), ms(3, 40), "compile"),
			})
			// The child renders its manifests at once, then its deploy waits
			// for shared-linux-01, which integration-tests holds: nothing in
			// the child runs for a minute and a half, so its bar shows the wait.
			// Its Duration is GitLab's, which leaves that wait out.
			child := Pipeline{ID: 202, ProjectPath: "acme/payments/deploy", Ref: "main", Status: StatusSuccess,
				WebURL: "https://gitlab.example.com/acme/payments/deploy/-/pipelines/202",
				Source: sourceParentPipeline, User: p.User, Created: start.Add(ms(3, 25)), Started: start.Add(ms(3, 27)),
				Finished: p.Finished, Duration: ms(0, 42)}
			childJob := func(id int64, stage, name, runner string, queued, from, dur time.Duration) Job {
				return Job{ID: id, Name: name, Stage: stage, Status: StatusSuccess,
					WebURL:      fmt.Sprintf("https://gitlab.example.com/acme/payments/deploy/-/jobs/%d", id),
					ProjectPath: child.ProjectPath, Ref: child.Ref, PipelineID: child.ID, Runner: runner,
					Created: child.Created, Queued: queued, Started: start.Add(from), Finished: start.Add(from + dur), Duration: dur}
			}
			tree.Children = []ActivePipeline{{
				Pipeline: child,
				Jobs: []Job{
					childJob(9900, "prepare", "render-manifests", "shared-linux-02", ms(0, 2), ms(3, 27), ms(0, 5)),
					childJob(9901, "deploy", "deploy-staging", "shared-linux-01", ms(1, 31), ms(5, 3), ms(0, 37)),
				},
			}}
		case 97: // acme/platform/gateway · feat/rate-limit · failed: stage order, a skipped job behind the failure
			tree.Jobs = sortPipelineJobs([]Job{
				job(9701, "build", "build", StatusSuccess, "docker-builder", ms(0, 5), 0, ms(1, 5)),
				job(9702, "test", "unit-tests", StatusSuccess, "shared-linux-01", ms(0, 2), ms(1, 7), ms(0, 52)),
				job(9703, "test", "contract-tests", StatusSuccess, "shared-linux-02", ms(0, 20), ms(1, 27), ms(0, 40)),
				job(9704, "deploy", "deploy-staging", StatusFailed, "shared-linux-01", ms(0, 4), ms(2, 28), ms(0, 44)),
				job(9705, "deploy", "smoke-test", StatusSkipped, "", 0, -1, 0),
			})
		case 96: // acme/payments/web · main · success: a long queue wait, and a manual job never played
			tree.Jobs = sortPipelineJobs([]Job{
				job(9601, "build", "build", StatusSuccess, "docker-builder", ms(0, 12), 0, ms(4, 10)),
				job(9602, "test", "e2e", StatusSuccess, "shared-linux-02", ms(0, 5), ms(4, 17), ms(2, 43)),
				job(9603, "deploy", "deploy-prod", StatusManual, "", 0, -1, 0),
			})
		default: // build, then test, which ends the way the pipeline did
			half := p.Duration / 2
			tree.Jobs = sortPipelineJobs([]Job{
				job(p.ID*100+1, "build", "build", StatusSuccess, "shared-linux-01", ms(0, 6), 0, half),
				job(p.ID*100+2, "test", "test", p.Status, "shared-linux-02", ms(0, 3), half, p.Duration-half),
			})
		}
		trees[p.ID] = tree
	}
	return trees
}

// sampleJobURL is a sample job's page, shaped like GitLab's.
func sampleJobURL(j Job) string {
	return fmt.Sprintf("https://gitlab.example.com/%s/-/jobs/%d", j.ProjectPath, j.ID)
}

// scaleWindowStats derives sample history for a shorter window from the full
// one by scaling every count and compute total by the windows' ratio (a
// pipeline that ran 214 times in 30d ran ~50 in 7d), leaving the per-run
// durations, queue waits and shares alone. Counts truncate rather than round so
// the parts never exceed their whole (⌊a⌋+⌊b⌋ ≤ ⌊a+b⌋). Sample data only.
func scaleWindowStats(full WindowStats, w time.Duration) WindowStats {
	frac := float64(w) / float64(full.Window)
	n := func(v int) int { return int(float64(v) * frac) }
	d := func(v time.Duration) time.Duration { return time.Duration(float64(v) * frac) }

	out := WindowStats{Window: w}
	for _, a := range full.TopPipelines {
		a.Count, a.Succeeded, a.Failed, a.KnownDurations = n(a.Count), n(a.Succeeded), n(a.Failed), n(a.KnownDurations)
		out.TopPipelines = append(out.TopPipelines, a)
	}
	for _, a := range full.TopJobs {
		a.Count, a.Succeeded, a.Failed, a.KnownDurations = n(a.Count), n(a.Succeeded), n(a.Failed), n(a.KnownDurations)
		out.TopJobs = append(out.TopJobs, a)
	}
	for _, s := range full.PipelineStats {
		s.Runs, s.Succeeded, s.Failed, s.Canceled, s.KnownDurations = n(s.Runs), n(s.Succeeded), n(s.Failed), n(s.Canceled), n(s.KnownDurations)
		out.PipelineStats = append(out.PipelineStats, s)
	}
	for _, a := range full.ComputeByProduct {
		a.Runs, a.Compute = n(a.Runs), d(a.Compute)
		out.ComputeByProduct = append(out.ComputeByProduct, a)
	}
	for _, a := range full.ComputeByProject {
		a.Runs, a.Compute = n(a.Runs), d(a.Compute)
		out.ComputeByProject = append(out.ComputeByProject, a)
	}
	for _, s := range full.RunnerStats {
		s.Jobs, s.Failed, s.Compute = n(s.Jobs), n(s.Failed), d(s.Compute)
		out.RunnerStats = append(out.RunnerStats, s)
	}
	for _, s := range full.TagStats {
		s.Jobs, s.Failed, s.Compute = n(s.Jobs), n(s.Failed), d(s.Compute)
		out.TagStats = append(out.TagStats, s)
	}
	return out
}
