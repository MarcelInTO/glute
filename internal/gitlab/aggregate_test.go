package gitlab

import (
	"strings"
	"testing"
	"time"
)

func jobNames(jobs []Job) string {
	n := make([]string, len(jobs))
	for i, j := range jobs {
		n[i] = j.Name
	}
	return strings.Join(n, ",")
}

func TestSortPipelineJobsByNeeds(t *testing.T) {
	// The needs order (build → test → deploy) contradicts BOTH the alphabetical
	// stage order (build, deploy, test) AND the min-ID stage rank (deploy=10,
	// test=20, build=30 → deploy, test, build). Only honoring needs yields the
	// right order, proving dependencies win over stages.
	jobs := []Job{
		{ID: 10, Name: "deploy", Stage: "deploy", Needs: []string{"test"}},
		{ID: 20, Name: "test", Stage: "test", Needs: []string{"build"}},
		{ID: 30, Name: "build", Stage: "build"},
	}
	if got := jobNames(sortPipelineJobs(jobs)); got != "build,test,deploy" {
		t.Errorf("sortPipelineJobs = %q, want build,test,deploy", got)
	}
}

func TestSortPipelineJobsNeedsTieBreakByName(t *testing.T) {
	// lint and test both need setup, so they're ready together; the tie-break is
	// stage-then-name, so lint precedes test despite its higher ID.
	jobs := []Job{
		{ID: 21, Name: "test", Stage: "check", Needs: []string{"setup"}},
		{ID: 20, Name: "lint", Stage: "check", Needs: []string{"setup"}},
		{ID: 10, Name: "setup", Stage: "setup"},
	}
	if got := jobNames(sortPipelineJobs(jobs)); got != "setup,lint,test" {
		t.Errorf("sortPipelineJobs = %q, want setup,lint,test", got)
	}
}

func TestSortPipelineJobsIgnoresUnknownNeeds(t *testing.T) {
	// A need naming a job outside the set (optional/cross-pipeline) is ignored,
	// but the presence of needs still switches on the topo path.
	jobs := []Job{
		{ID: 2, Name: "b", Stage: "s", Needs: []string{"a", "ghost"}},
		{ID: 1, Name: "a", Stage: "s"},
	}
	if got := jobNames(sortPipelineJobs(jobs)); got != "a,b" {
		t.Errorf("sortPipelineJobs = %q, want a,b", got)
	}
}

func TestSortPipelineJobsCycleTerminates(t *testing.T) {
	// A malformed a↔b cycle must not hang; independent c leads, then the cycle is
	// broken deterministically by the tie-break key.
	jobs := []Job{
		{ID: 1, Name: "a", Stage: "s", Needs: []string{"b"}},
		{ID: 2, Name: "b", Stage: "s", Needs: []string{"a"}},
		{ID: 3, Name: "c", Stage: "s"},
	}
	got := sortPipelineJobs(jobs)
	if len(got) != 3 {
		t.Fatalf("want all 3 jobs, got %d: %s", len(got), jobNames(got))
	}
	// b and c end up at depth 0 (the a→b back-edge is broken), a at depth 1;
	// the exact order of a malformed cycle is best-effort but deterministic.
	if jobNames(got) != "b,c,a" {
		t.Errorf("sortPipelineJobs = %q, want b,c,a", jobNames(got))
	}
}

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

func TestPipelineStatsDropsRefAndDistributes(t *testing.T) {
	// Two refs of the same project collapse into one row (the historical analysis
	// is per project, not per ref). The five known durations 1..5m give min=1m,
	// max=5m, mean=3m, and p95 (nearest-rank of 5 samples) = the 5th = 5m. A
	// zero-duration (still-running) run counts toward Runs but not the distribution.
	pipes := []Pipeline{
		{ProjectPath: "a/x", Ref: "main", Status: StatusSuccess, Duration: 1 * time.Minute},
		{ProjectPath: "a/x", Ref: "main", Status: StatusSuccess, Duration: 2 * time.Minute},
		{ProjectPath: "a/x", Ref: "dev", Status: StatusFailed, Duration: 3 * time.Minute},
		{ProjectPath: "a/x", Ref: "dev", Status: StatusCanceled, Duration: 4 * time.Minute},
		{ProjectPath: "a/x", Ref: "main", Status: StatusSuccess, Duration: 5 * time.Minute},
		{ProjectPath: "a/x", Ref: "main", Status: StatusRunning, Duration: 0}, // unknown dur
	}
	got := pipelineStats(pipes)
	if len(got) != 1 {
		t.Fatalf("want 1 project row (ref dropped), got %d: %+v", len(got), got)
	}
	s := got[0]
	if s.ProjectPath != "a/x" {
		t.Fatalf("path = %q, want a/x", s.ProjectPath)
	}
	if s.Runs != 6 || s.Succeeded != 3 || s.Failed != 1 || s.Canceled != 1 {
		t.Errorf("tallies: runs=%d succ=%d fail=%d cancel=%d", s.Runs, s.Succeeded, s.Failed, s.Canceled)
	}
	if s.KnownDurations != 5 {
		t.Errorf("known durations: want 5, got %d", s.KnownDurations)
	}
	if s.DurMin != time.Minute || s.DurMean != 3*time.Minute || s.DurMax != 5*time.Minute {
		t.Errorf("dur min/mean/max: got %s / %s / %s, want 1m / 3m / 5m", s.DurMin, s.DurMean, s.DurMax)
	}
	if s.DurP95 != 5*time.Minute {
		t.Errorf("p95: want 5m, got %s", s.DurP95)
	}
	if s.FailRate() != 1.0/6.0 {
		t.Errorf("fail rate: want 1/6 (of all runs), got %v", s.FailRate())
	}
}

func TestPipelineStatsKeepsProjectWithNoKnownDurations(t *testing.T) {
	// A project whose runs have no finished duration still gets a row — with a
	// zeroed distribution — so the fails panel can surface it; the slowest panel
	// filters KnownDurations==0 itself.
	pipes := []Pipeline{
		{ProjectPath: "a/x", Status: StatusRunning},
		{ProjectPath: "a/x", Status: StatusFailed}, // failed, but duration unknown
	}
	got := pipelineStats(pipes)
	if len(got) != 1 || got[0].Runs != 2 || got[0].KnownDurations != 0 {
		t.Fatalf("want 1 row runs=2 known=0, got %+v", got)
	}
	if got[0].DurMean != 0 || got[0].DurP95 != 0 || got[0].DurMax != 0 {
		t.Errorf("distribution should be zero with no known durations, got %+v", got[0])
	}
}

func TestComputeByProject(t *testing.T) {
	// a/x consumes 6m across two jobs, b/y 2m — total 8m, so shares are 75/25 and
	// the busier project leads.
	jobs := []Job{
		{ProjectPath: "a/x", Duration: 3 * time.Minute},
		{ProjectPath: "a/x", Duration: 3 * time.Minute},
		{ProjectPath: "b/y", Duration: 2 * time.Minute},
	}
	got := computeByProject(jobs)
	if len(got) != 2 {
		t.Fatalf("want 2 rows, got %d: %+v", len(got), got)
	}
	if got[0].Key != "a/x" || got[0].Compute != 6*time.Minute || got[0].Runs != 2 {
		t.Errorf("top row: key=%s compute=%s runs=%d", got[0].Key, got[0].Compute, got[0].Runs)
	}
	if got[0].Pct != 75 || got[1].Pct != 25 {
		t.Errorf("shares: got %.0f / %.0f, want 75 / 25 (sum to 100)", got[0].Pct, got[1].Pct)
	}
}

func TestComputeByProductOverlapAndUnmapped(t *testing.T) {
	// a/x belongs to both Payments and Platform, so its 6m counts toward each —
	// the product shares then exceed 100% of the grand total. c/z maps to no
	// product (a downstream child outside the watchlist): it lifts the grand total
	// but appears in no product row.
	jobs := []Job{
		{ProjectPath: "a/x", Duration: 6 * time.Minute},
		{ProjectPath: "b/y", Duration: 2 * time.Minute},
		{ProjectPath: "c/z", Duration: 2 * time.Minute}, // unmapped
	}
	pp := map[string][]string{
		"a/x": {"Payments", "Platform"},
		"b/y": {"Platform"},
	}
	got := computeByProduct(jobs, pp)
	if len(got) != 2 {
		t.Fatalf("want 2 product rows (c/z unmapped), got %d: %+v", len(got), got)
	}
	byKey := map[string]ComputeAgg{}
	for _, a := range got {
		byKey[a.Key] = a
	}
	// Payments = a/x = 6m; Platform = a/x + b/y = 8m.
	if byKey["Payments"].Compute != 6*time.Minute || byKey["Platform"].Compute != 8*time.Minute {
		t.Errorf("compute: payments=%s platform=%s, want 6m / 8m", byKey["Payments"].Compute, byKey["Platform"].Compute)
	}
	// Pct is of the 10m grand total (c/z included), so 60 + 80 = 140 > 100.
	if byKey["Payments"].Pct != 60 || byKey["Platform"].Pct != 80 {
		t.Errorf("shares: payments=%.0f platform=%.0f, want 60 / 80", byKey["Payments"].Pct, byKey["Platform"].Pct)
	}
	if got[0].Key != "Platform" {
		t.Errorf("sort by compute desc: want Platform first, got %s", got[0].Key)
	}
}

func TestRunnerStats(t *testing.T) {
	// Jobs with no runner are skipped. r1 runs three jobs (durations 2/4/6m →
	// mean 4m, compute 12m) with one failure; its queue waits 10/20/30s give mean
	// 20s and p95 (nearest-rank of 3) = 30s. Ordered by compute, r1 leads r2.
	jobs := []Job{
		{Runner: "r1", Status: StatusSuccess, Duration: 2 * time.Minute, Queued: 10 * time.Second},
		{Runner: "r1", Status: StatusFailed, Duration: 4 * time.Minute, Queued: 20 * time.Second},
		{Runner: "r1", Status: StatusSuccess, Duration: 6 * time.Minute, Queued: 30 * time.Second},
		{Runner: "r2", Status: StatusSuccess, Duration: 1 * time.Minute, Queued: 5 * time.Second},
		{Runner: "", Status: StatusSuccess, Duration: 9 * time.Minute}, // unassigned → skipped
	}
	got := runnerStats(jobs)
	if len(got) != 2 {
		t.Fatalf("want 2 runners (empty skipped), got %d: %+v", len(got), got)
	}
	r1 := got[0]
	if r1.Key != "r1" || r1.Jobs != 3 || r1.Failed != 1 || r1.Compute != 12*time.Minute {
		t.Errorf("r1: key=%s jobs=%d failed=%d compute=%s", r1.Key, r1.Jobs, r1.Failed, r1.Compute)
	}
	if r1.MeanDuration != 4*time.Minute {
		t.Errorf("r1 mean duration: want 4m, got %s", r1.MeanDuration)
	}
	if r1.MeanQueue != 20*time.Second || r1.P95Queue != 30*time.Second {
		t.Errorf("r1 queue mean/p95: got %s / %s, want 20s / 30s", r1.MeanQueue, r1.P95Queue)
	}
}

func TestTagStats(t *testing.T) {
	// A multi-tagged job counts toward each of its tags (linux gets both jobs,
	// docker only the first); a tagless job lands in the untagged bucket; a job no
	// runner picked up is skipped even when tagged.
	jobs := []Job{
		{Runner: "r1", Tags: []string{"linux", "docker"}, Status: StatusFailed, Duration: 4 * time.Minute, Queued: 20 * time.Second},
		{Runner: "r2", Tags: []string{"linux"}, Status: StatusSuccess, Duration: 2 * time.Minute, Queued: 10 * time.Second},
		{Runner: "r1", Status: StatusSuccess, Duration: time.Minute, Queued: 5 * time.Second},
		{Runner: "", Tags: []string{"linux"}, Status: StatusPending},
	}
	got := tagStats(jobs)
	if len(got) != 3 {
		t.Fatalf("want 3 rows (linux, docker, untagged), got %d: %+v", len(got), got)
	}
	if got[0].Key != "linux" {
		t.Errorf("sort by compute desc: want linux first, got %s", got[0].Key)
	}
	byKey := map[string]JobStats{}
	for _, s := range got {
		byKey[s.Key] = s
	}
	linux := byKey["linux"]
	if linux.Jobs != 2 || linux.Failed != 1 || linux.Compute != 6*time.Minute {
		t.Errorf("linux: jobs=%d failed=%d compute=%s, want 2/1/6m", linux.Jobs, linux.Failed, linux.Compute)
	}
	if linux.MeanQueue != 15*time.Second {
		t.Errorf("linux mean queue: want 15s, got %s", linux.MeanQueue)
	}
	if docker := byKey["docker"]; docker.Jobs != 1 || docker.Compute != 4*time.Minute {
		t.Errorf("docker: jobs=%d compute=%s, want 1/4m", docker.Jobs, docker.Compute)
	}
	if un := byKey[untaggedKey]; un.Jobs != 1 || un.Compute != time.Minute {
		t.Errorf("untagged: jobs=%d compute=%s, want 1/1m", un.Jobs, un.Compute)
	}
}

func TestPercentileNearestRank(t *testing.T) {
	if got := percentile(nil, 0.95); got != 0 {
		t.Errorf("empty slice: want 0, got %s", got)
	}
	secs := func(n ...int) []time.Duration {
		out := make([]time.Duration, len(n))
		for i, v := range n {
			out[i] = time.Duration(v) * time.Second
		}
		return out
	}
	// Ascending-sorted input. p95 of 1..10 is the max (ceil(0.95*10)=10 → 10th);
	// p50 is the 5th (ceil(0.5*10)=5); p95 of a single sample is that sample.
	ten := secs(1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	if got := percentile(ten, 0.95); got != 10*time.Second {
		t.Errorf("p95 of 1..10: want 10s, got %s", got)
	}
	if got := percentile(ten, 0.50); got != 5*time.Second {
		t.Errorf("p50 of 1..10: want 5s, got %s", got)
	}
	if got := percentile(secs(7), 0.95); got != 7*time.Second {
		t.Errorf("p95 of a single sample: want 7s, got %s", got)
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
