package gitlab

import (
	"testing"
	"time"
)

func TestTopPipelinesGroupsAndAverages(t *testing.T) {
	end := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	pipes := []Pipeline{
		{ID: 1, ProjectPath: "a/x", Ref: "main", Status: StatusSuccess, Duration: 2 * time.Minute, Finished: end},
		{ID: 2, ProjectPath: "a/x", Ref: "main", Status: StatusFailed, Duration: 4 * time.Minute, Finished: end},
		{ID: 3, ProjectPath: "a/x", Ref: "dev", Status: StatusSuccess, Duration: time.Minute, Finished: end},
		{ID: 4, ProjectPath: "b/y", Ref: "main", Status: StatusSuccess, Duration: 0, Finished: end}, // unknown dur
	}

	got := topPipelines(pipes, 10)
	if len(got) != 3 {
		t.Fatalf("want 3 groups, got %d: %+v", len(got), got)
	}

	top := got[0]
	if top.ProjectPath != "a/x" || top.Ref != "main" {
		t.Fatalf("want a/x main first, got %s %s", top.ProjectPath, top.Ref)
	}
	if top.Count != 2 || top.Succeeded != 1 || top.Failed != 1 {
		t.Errorf("counts: got count=%d succ=%d fail=%d", top.Count, top.Succeeded, top.Failed)
	}
	if top.AvgDuration != 3*time.Minute {
		t.Errorf("avg duration: want 3m, got %s", top.AvgDuration)
	}
	if top.SuccessRate() != 0.5 {
		t.Errorf("success rate: want 0.5, got %v", top.SuccessRate())
	}

	// The group with only an unknown-duration run must report no average.
	var by struct{ found bool }
	for _, a := range got {
		if a.ProjectPath == "b/y" {
			by.found = true
			if a.KnownDurations != 0 || a.AvgDuration != 0 {
				t.Errorf("b/y should have no known durations, got known=%d avg=%s", a.KnownDurations, a.AvgDuration)
			}
		}
	}
	if !by.found {
		t.Error("expected a b/y group")
	}
}

func TestTopPipelinesRespectsLimit(t *testing.T) {
	pipes := []Pipeline{
		{ProjectPath: "a", Ref: "1", Status: StatusSuccess},
		{ProjectPath: "b", Ref: "1", Status: StatusSuccess},
		{ProjectPath: "c", Ref: "1", Status: StatusSuccess},
	}
	if got := topPipelines(pipes, 2); len(got) != 2 {
		t.Fatalf("want 2 rows with limit 2, got %d", len(got))
	}
}

func TestRunningAndRecentPipelines(t *testing.T) {
	now := time.Now()
	pipes := []Pipeline{
		{ID: 1, Status: StatusRunning, Started: now.Add(-time.Minute)},
		{ID: 2, Status: StatusSuccess, Finished: now.Add(-2 * time.Hour)},
		{ID: 3, Status: StatusFailed, Finished: now.Add(-90 * time.Minute)},
		{ID: 4, Status: StatusSuccess, Finished: now.Add(-48 * time.Hour)}, // outside recent window
	}

	if got := runningPipelines(pipes); len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("running: want [1], got %+v", got)
	}

	recent := recentPipelines(pipes, now.Add(-24*time.Hour))
	if len(recent) != 2 {
		t.Fatalf("recent: want 2 within 24h, got %d", len(recent))
	}
	// Newest-finished first: pipeline 3 (-90m) before pipeline 2 (-2h).
	if recent[0].ID != 3 || recent[1].ID != 2 {
		t.Errorf("recent order: want [3 2], got [%d %d]", recent[0].ID, recent[1].ID)
	}
}

func TestRunningJobsIncludesPending(t *testing.T) {
	now := time.Now()
	jobs := []Job{
		{ID: 1, Status: StatusRunning, Started: now.Add(-time.Minute)},
		{ID: 2, Status: StatusPending, Created: now.Add(-30 * time.Second)},
		{ID: 3, Status: StatusCreated, Created: now.Add(-10 * time.Second)},
		{ID: 4, Status: StatusSuccess, Finished: now.Add(-time.Minute)},
	}
	if got := runningJobs(jobs); len(got) != 3 {
		t.Fatalf("running jobs should include running+pending+created (3), got %d", len(got))
	}
}

func TestActivePipelinesBuildsTree(t *testing.T) {
	now := time.Now()
	roots := []Pipeline{
		{ID: 1, ProjectPath: "a/x", Ref: "main", Status: StatusRunning, Started: now.Add(-2 * time.Minute)},
		{ID: 2, ProjectPath: "a/y", Ref: "main", Status: StatusSuccess}, // finished root -> excluded
	}
	jobs := []Job{
		// Root 1's own jobs, shuffled. Stages build/test/deploy do NOT sort
		// alphabetically into execution order (that would be build, deploy,
		// test), so the sort must rank stages by their creation-order IDs, not
		// their names. Two test-stage jobs also check within-stage name order.
		{ID: 11, PipelineID: 1, Name: "unit", Stage: "test", Status: StatusRunning, Started: now.Add(-time.Minute)},
		{ID: 10, PipelineID: 1, Name: "compile", Stage: "build", Status: StatusSuccess, Duration: 30 * time.Second},
		{ID: 12, PipelineID: 1, Name: "publish", Stage: "deploy", Status: StatusPending},
		{ID: 13, PipelineID: 1, Name: "integration", Stage: "test", Status: StatusPending},
		// Child pipeline 3's job.
		{ID: 30, PipelineID: 3, Name: "deploy", Stage: "deploy", Status: StatusPending},
		// A job of the excluded finished root, which must not surface.
		{ID: 20, PipelineID: 2, Name: "noop", Stage: "test", Status: StatusSuccess},
	}
	childPipes := map[int64]Pipeline{
		3: {ID: 3, ProjectPath: "a/deploy", Ref: "main", Status: StatusPending, Source: sourceParentPipeline},
	}
	childParent := map[int64]int64{3: 1}

	got := activePipelines(roots, jobs, childPipes, childParent)
	if len(got) != 1 {
		t.Fatalf("want 1 active root, got %d: %+v", len(got), got)
	}
	root := got[0]
	if root.ID != 1 {
		t.Fatalf("want root pipeline 1, got %d", root.ID)
	}
	// Jobs come out in the UI's order: stages in execution order (build, test,
	// deploy — not the alphabetical build, deploy, test), and by name within a
	// stage (integration before unit).
	wantOrder := []string{"compile", "integration", "unit", "publish"}
	if len(root.Jobs) != len(wantOrder) {
		t.Fatalf("want %d jobs, got %d: %+v", len(wantOrder), len(root.Jobs), root.Jobs)
	}
	for i, name := range wantOrder {
		if root.Jobs[i].Name != name {
			t.Errorf("job %d = %q, want %q (execution order): %+v", i, root.Jobs[i].Name, name, root.Jobs)
		}
	}
	// Child nested with its own job, tagged with its own project path.
	if len(root.Children) != 1 || root.Children[0].ID != 3 || root.Children[0].ProjectPath != "a/deploy" {
		t.Fatalf("child pipeline not nested correctly: %+v", root.Children)
	}
	if len(root.Children[0].Jobs) != 1 || root.Children[0].Jobs[0].Name != "deploy" {
		t.Errorf("child job missing: %+v", root.Children[0].Jobs)
	}
	// Progress spans the whole subtree: 1 of 5 jobs finished (compile) — root 1's
	// four own jobs plus child pipeline 3's one.
	if done, total := root.Progress(); done != 1 || total != 5 {
		t.Errorf("subtree progress: want 1/5, got %d/%d", done, total)
	}
}

func TestActivePipelinesToleratesEdgeCycle(t *testing.T) {
	// A malformed parent↔child cycle must not spin forever.
	roots := []Pipeline{{ID: 1, ProjectPath: "a/x", Ref: "main", Status: StatusRunning}}
	childPipes := map[int64]Pipeline{
		1: {ID: 1, ProjectPath: "a/x", Ref: "main", Status: StatusRunning},
		2: {ID: 2, ProjectPath: "a/x", Ref: "main", Status: StatusRunning},
	}
	childParent := map[int64]int64{2: 1, 1: 2} // 1 -> 2 -> 1

	got := activePipelines(roots, nil, childPipes, childParent)
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("want single root 1, got %+v", got)
	}
	// 1 has child 2; 2's only child is 1, already visited, so recursion stops.
	if len(got[0].Children) != 1 || got[0].Children[0].ID != 2 {
		t.Fatalf("want child 2 under root 1, got %+v", got[0].Children)
	}
	if len(got[0].Children[0].Children) != 0 {
		t.Errorf("cycle should be broken at the second visit, got %+v", got[0].Children[0].Children)
	}
}

func TestTopJobsGroupsByProjectAndName(t *testing.T) {
	jobs := []Job{
		{ProjectPath: "a/x", Name: "test", Status: StatusSuccess, Duration: time.Minute},
		{ProjectPath: "a/x", Name: "test", Status: StatusSuccess, Duration: 3 * time.Minute},
		{ProjectPath: "a/x", Name: "build", Status: StatusFailed, Duration: 30 * time.Second},
	}
	got := topJobs(jobs, 10)
	if len(got) != 2 {
		t.Fatalf("want 2 job groups, got %d", len(got))
	}
	if got[0].Name != "test" || got[0].Count != 2 || got[0].AvgDuration != 2*time.Minute {
		t.Errorf("top job: got name=%s count=%d avg=%s", got[0].Name, got[0].Count, got[0].AvgDuration)
	}
}
