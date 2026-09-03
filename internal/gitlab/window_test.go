package gitlab

import (
	"testing"
	"time"
)

const day = 24 * time.Hour

func TestSelectableWindows(t *testing.T) {
	cases := []struct {
		top  time.Duration
		want []time.Duration
	}{
		{30 * day, []time.Duration{day, 7 * day, 30 * day}}, // the default config
		{14 * day, []time.Duration{day, 7 * day, 14 * day}}, // a step is capped to the configured window
		{90 * day, []time.Duration{day, 7 * day, 30 * day, 90 * day}},
		{3 * day, []time.Duration{day, 3 * day}},
		{day, []time.Duration{day}},                       // equal to a step: no duplicate
		{12 * time.Hour, []time.Duration{12 * time.Hour}}, // shorter than every step
	}
	for _, c := range cases {
		got := selectableWindows(c.top)
		if len(got) != len(c.want) {
			t.Errorf("selectableWindows(%v) = %v, want %v", c.top, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("selectableWindows(%v) = %v, want %v", c.top, got, c.want)
				break
			}
		}
	}
}

// TestWindowSlicesMirrorsEviction checks the sub-window filter keys on the same
// timestamps evict does — pipeline updated_at, job created_at — and keeps a job
// with no created_at, as evict does.
func TestWindowSlicesMirrorsEviction(t *testing.T) {
	now := time.Now()
	since := now.Add(-7 * day)
	pipes := []Pipeline{
		{ID: 1, Updated: now.Add(-time.Hour)},
		{ID: 2, Updated: since}, // exactly on the boundary: kept (not Before)
		{ID: 3, Updated: now.Add(-8 * day)},
		{ID: 4, Finished: now.Add(-time.Hour)}, // zero Updated: evict drops it, so do we
	}
	jobs := []Job{
		{ID: 10, Created: now.Add(-time.Hour)},
		{ID: 11, Created: now.Add(-8 * day)},
		{ID: 12}, // unknown created_at: kept, like evict
	}
	ps, js := windowSlices(pipes, jobs, since)
	if len(ps) != 2 || ps[0].ID != 1 || ps[1].ID != 2 {
		t.Errorf("pipelines in window = %v, want IDs 1, 2", ps)
	}
	if len(js) != 2 || js[0].ID != 10 || js[1].ID != 12 {
		t.Errorf("jobs in window = %v, want IDs 10, 12", js)
	}
}

// TestHistoryWindowsNarrowProgressively builds a 30d store with one pipeline in
// each band and checks each window's stats cover only its own lookback, the
// windows come back ascending, and the last one spans the whole store.
func TestHistoryWindowsNarrowProgressively(t *testing.T) {
	now := time.Now()
	mk := func(id int64, ago time.Duration) Pipeline {
		return Pipeline{ID: id, ProjectPath: "acme/api", Status: StatusSuccess,
			Updated: now.Add(-ago), Duration: time.Minute}
	}
	pipes := []Pipeline{mk(1, 2*time.Hour), mk(2, 3*day), mk(3, 20*day)}
	jobs := []Job{
		{ID: 1, PipelineID: 1, ProjectPath: "acme/api", Name: "build", Runner: "r1", Status: StatusSuccess, Created: now.Add(-2 * time.Hour), Duration: time.Minute},
		{ID: 2, PipelineID: 2, ProjectPath: "acme/api", Name: "build", Runner: "r1", Status: StatusSuccess, Created: now.Add(-3 * day), Duration: time.Minute},
		{ID: 3, PipelineID: 3, ProjectPath: "acme/api", Name: "build", Runner: "r1", Status: StatusSuccess, Created: now.Add(-20 * day), Duration: time.Minute},
	}

	ws := historyWindows(now, 30*day, pipes, jobs, nil, 20)
	if len(ws) != 3 {
		t.Fatalf("got %d windows, want 3", len(ws))
	}
	wantRuns := map[time.Duration]int{day: 1, 7 * day: 2, 30 * day: 3}
	for i, h := range ws {
		if i > 0 && h.Window <= ws[i-1].Window {
			t.Errorf("windows not ascending: %v then %v", ws[i-1].Window, h.Window)
		}
		want, ok := wantRuns[h.Window]
		if !ok {
			t.Errorf("unexpected window %v", h.Window)
			continue
		}
		if len(h.PipelineStats) != 1 || h.PipelineStats[0].Runs != want {
			t.Errorf("%v window: pipeline runs = %+v, want %d", h.Window, h.PipelineStats, want)
		}
		if len(h.TopJobs) != 1 || h.TopJobs[0].Count != want {
			t.Errorf("%v window: top-job count = %+v, want %d", h.Window, h.TopJobs, want)
		}
		if len(h.RunnerStats) != 1 || h.RunnerStats[0].Jobs != want {
			t.Errorf("%v window: runner jobs = %+v, want %d", h.Window, h.RunnerStats, want)
		}
	}
	if last := ws[len(ws)-1]; last.Window != 30*day {
		t.Errorf("last window = %v, want the full 30d", last.Window)
	}
}

func TestSnapshotWindowAt(t *testing.T) {
	full := WindowStats{Window: 30 * day, PipelineStats: []PipelineStats{{ProjectPath: "full"}}}
	week := WindowStats{Window: 7 * day, PipelineStats: []PipelineStats{{ProjectPath: "week"}}}
	s := Snapshot{WindowStats: full, Windows: []WindowStats{week, full}}

	if got := s.WindowAt(7 * day); got.PipelineStats[0].ProjectPath != "week" {
		t.Errorf("WindowAt(7d) = %v, want the week stats", got.Window)
	}
	for _, w := range []time.Duration{0, 30 * day, 3 * day} { // default, exact full, unknown
		if got := s.WindowAt(w); got.PipelineStats[0].ProjectPath != "full" {
			t.Errorf("WindowAt(%v) = %v, want the full-window fallback", w, got.Window)
		}
	}
	// A snapshot with no Windows at all (an empty early return) still answers.
	if got := (Snapshot{}).WindowAt(day); got.Window != 0 || len(got.PipelineStats) != 0 {
		t.Errorf("WindowAt on an empty snapshot = %+v, want zero stats", got)
	}
}

// TestSampleSnapshotWindows checks the fixture carries the three default
// windows, ascending, ending in the flat full-window stats, with the shorter
// windows scaled down so the window key visibly changes the panels.
func TestSampleSnapshotWindows(t *testing.T) {
	s := SampleSnapshot()
	if len(s.Windows) != 3 {
		t.Fatalf("sample has %d windows, want 3", len(s.Windows))
	}
	for i, want := range []time.Duration{day, 7 * day, 30 * day} {
		if s.Windows[i].Window != want {
			t.Errorf("sample window %d = %v, want %v", i, s.Windows[i].Window, want)
		}
	}
	if s.Windows[2].Window != s.Window || len(s.Windows[2].PipelineStats) != len(s.PipelineStats) {
		t.Errorf("last sample window should be the embedded full-window stats")
	}
	full, week := s.Windows[2].PipelineStats[0], s.Windows[1].PipelineStats[0]
	if week.Runs >= full.Runs || week.Runs == 0 {
		t.Errorf("7d runs = %d, want scaled below the 30d %d but non-zero", week.Runs, full.Runs)
	}
	if week.DurMean != full.DurMean {
		t.Errorf("scaling must leave durations alone: 7d mean %v vs 30d %v", week.DurMean, full.DurMean)
	}
	if week.Succeeded+week.Failed+week.Canceled > week.Runs {
		t.Errorf("scaled outcomes (%d+%d+%d) exceed scaled runs %d", week.Succeeded, week.Failed, week.Canceled, week.Runs)
	}
}
