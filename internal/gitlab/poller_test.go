package gitlab

import (
	"errors"
	"testing"
	"time"
)

func TestRefreshStatsAccumulatesAverages(t *testing.T) {
	p := &Poller{}
	p.recordTiming(100*time.Millisecond, 300*time.Millisecond, 200*time.Millisecond)
	p.recordTiming(300*time.Millisecond, 500*time.Millisecond, 400*time.Millisecond)

	s := p.RefreshStats()
	if s.Count != 2 {
		t.Fatalf("Count = %d, want 2", s.Count)
	}
	if got, want := s.AvgResolve(), 200*time.Millisecond; got != want {
		t.Errorf("AvgResolve = %s, want %s", got, want)
	}
	if got, want := s.AvgFetch(), 400*time.Millisecond; got != want {
		t.Errorf("AvgFetch = %s, want %s", got, want)
	}
	if got, want := s.AvgEnrich(), 300*time.Millisecond; got != want {
		t.Errorf("AvgEnrich = %s, want %s", got, want)
	}
}

func TestRefreshStatsAveragesEmpty(t *testing.T) {
	var s RefreshStats
	if got := s.AvgResolve(); got != 0 {
		t.Errorf("AvgResolve on empty = %s, want 0", got)
	}
	if got := s.AvgFetch(); got != 0 {
		t.Errorf("AvgFetch on empty = %s, want 0", got)
	}
	if got := s.AvgEnrich(); got != 0 {
		t.Errorf("AvgEnrich on empty = %s, want 0", got)
	}
}

func TestDropChildPipelines(t *testing.T) {
	in := []Pipeline{
		{ID: 1, Source: "push"},
		{ID: 2, Source: sourceParentPipeline}, // child: handled via bridges, not the store
		{ID: 3, Source: "schedule"},
	}
	out := dropChildPipelines(in)
	if len(out) != 2 {
		t.Fatalf("want 2 non-child pipelines, got %d: %+v", len(out), out)
	}
	for _, p := range out {
		if p.Source == sourceParentPipeline {
			t.Errorf("child pipeline %d leaked through", p.ID)
		}
	}
}

func TestJobRootsSelection(t *testing.T) {
	p := &Poller{
		pipes: map[int64]Pipeline{
			1: {ID: 1, Status: StatusRunning}, // active -> root
			2: {ID: 2, Status: StatusSuccess}, // finished, untouched -> not a root
			3: {ID: 3, Status: StatusSuccess}, // finished but child subtree pending -> root
			4: {ID: 4, Status: StatusFailed},  // finished, job-complete -> never a root
		},
		jobsDone: map[int64]bool{4: true},
		pending:  map[int64]bool{3: true},
	}
	changed := []Pipeline{
		{ID: 4, Status: StatusFailed},  // in delta but jobsDone -> excluded
		{ID: 5, Status: StatusSuccess}, // freshly changed -> root
	}

	got := map[int64]bool{}
	for _, r := range p.jobRoots(changed) {
		got[r.ID] = true
	}

	for _, id := range []int64{1, 3, 5} {
		if !got[id] {
			t.Errorf("expected pipeline %d to be a job root; got %v", id, got)
		}
	}
	if got[2] {
		t.Error("untouched finished pipeline 2 should not be a root")
	}
	if got[4] {
		t.Error("job-complete pipeline 4 should not be a root")
	}
	if len(got) != 3 {
		t.Fatalf("want exactly 3 roots, got %d: %v", len(got), got)
	}
}

// TestBulkJobProjectsSkipsOnlyPositivelyQuiet checks the full-window job
// re-list skips exactly the projects whose pipeline list succeeded and came back
// empty. A project alive only through a scheduled pipeline (a forgotten nightly
// build), or only as a cross-project downstream child, or whose list call failed,
// is still re-listed — the decision comes from GitLab's own pipeline list, not
// from a proxy that CI-only activity wouldn't move.
func TestBulkJobProjectsSkipsOnlyPositivelyQuiet(t *testing.T) {
	results := []projectPipelines{
		{project: Project{ID: 1, Path: "g/pushed"}, pipes: []Pipeline{{ID: 10, Source: "push"}}},
		{project: Project{ID: 2, Path: "g/quiet"}}, // ok, empty → the only skip
		{project: Project{ID: 3, Path: "g/nightly-only"}, pipes: []Pipeline{{ID: 30, Source: "schedule"}}},
		{project: Project{ID: 4, Path: "g/child-only"}, pipes: []Pipeline{{ID: 40, Source: sourceParentPipeline}}},
		{project: Project{ID: 5, Path: "g/forbidden"}, err: errForbidden},
	}

	got := bulkJobProjects(results)
	var ids []int64
	for _, p := range got {
		ids = append(ids, p.ID)
	}
	want := []int64{1, 3, 4, 5}
	if len(ids) != len(want) {
		t.Fatalf("bulkJobProjects = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("bulkJobProjects = %v, want %v (order preserved)", ids, want)
		}
	}

	// The same results flatten to every pipeline (children included — those are
	// dropped later, by dropChildPipelines) plus the one error.
	pipes, errs := flattenPipelines(results)
	if len(pipes) != 3 {
		t.Errorf("flattenPipelines returned %d pipelines, want 3", len(pipes))
	}
	if len(errs) != 1 || errs[0] != errForbidden {
		t.Errorf("flattenPipelines errors = %v, want [%v]", errs, errForbidden)
	}
}

var errForbidden = errors.New("403 Forbidden")
