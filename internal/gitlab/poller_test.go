package gitlab

import (
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
