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
