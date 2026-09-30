package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/MarcelInTO/glute/internal/gitlab"
)

// TestFinishedPanelShowsNewestWhenOverflowing builds more recently-finished
// pipelines than the bottom panel can show and checks the NEWEST (row 0) is
// visible — i.e. the panel isn't parked at the end of an overflowing table.
func TestFinishedPanelShowsNewestWhenOverflowing(t *testing.T) {
	now := time.Now()
	var recent []gitlab.Pipeline
	for i := 0; i < 22; i++ {
		recent = append(recent, gitlab.Pipeline{
			ProjectPath: "acme/proj",
			Ref:         "newest-is-r0", // r0 is the most recent
			Status:      gitlab.StatusSuccess,
			User:        "u",
			Finished:    now.Add(-time.Duration(i+1) * 10 * time.Minute),
			Duration:    time.Minute,
		})
	}
	// Tag each ref so we can tell which rows rendered.
	for i := range recent {
		recent[i].Ref = refTag(i)
	}
	snap := gitlab.Snapshot{RecentPipelines: recent, UpdatedAt: now}

	// Mimic the real lifecycle: the panel first renders empty (before the initial
	// refresh completes), then gets populated. tview's Table sticks to the end
	// (trackEnd) once its content fits, so the empty first render can park the
	// view at the bottom of the later, overflowing list.
	d := NewDashboard(gitlab.FakeService{Snap: gitlab.Snapshot{UpdatedAt: now}}, Options{Title: "t"})
	d.current.update(gitlab.Snapshot{UpdatedAt: now})
	_ = renderToText(t, d, 130, 46)

	d.snapshot = snap
	d.current.update(snap)
	out := renderToText(t, d, 130, 46)
	t.Logf("render:\n%s", out)

	if !strings.Contains(out, refTag(0)) {
		t.Errorf("newest finished row %q is not visible — panel is parked at the end", refTag(0))
	}
}

func refTag(i int) string {
	return "R" + string(rune('a'+i)) // Ra=newest .. distinct per row
}

func TestDisplayRef(t *testing.T) {
	cases := map[string]string{
		"refs/merge-requests/123/head": "MR 123",
		"refs/merge-requests/7/merge":  "MR 7",
		"refs/merge-requests/42":       "MR 42",
		"main":                         "main",
		"release/2.1":                  "release/2.1",
		"feat/rate-limit":              "feat/rate-limit",
		"refs/merge-requests/abc/head": "refs/merge-requests/abc/head", // non-numeric: unchanged
		"refs/merge-requests//head":    "refs/merge-requests//head",    // empty iid: unchanged
	}
	for in, want := range cases {
		if got := displayRef(in); got != want {
			t.Errorf("displayRef(%q) = %q, want %q", in, got, want)
		}
	}
}

// finishedFollowDashboard has 30 finished pipelines (#1000 newest … #971),
// more than the finished panel shows at 130×32, with the panel holding the
// keyboard — the state any `f`, click or opened pipeline leaves it in.
func finishedFollowDashboard(t *testing.T) (*Dashboard, *gitlab.Snapshot) {
	t.Helper()
	now := time.Now()
	snap := &gitlab.Snapshot{UpdatedAt: now}
	for i := range 30 {
		snap.RecentPipelines = append(snap.RecentPipelines, gitlab.Pipeline{ID: int64(1000 - i),
			ProjectPath: "acme/p" + refTag(i), Ref: "main", Status: gitlab.StatusSuccess,
			Finished: now.Add(-time.Duration(i) * time.Minute)})
	}
	d := NewDashboard(gitlab.FakeService{Snap: *snap}, Options{})
	d.snapshot = *snap
	d.current.update(*snap)
	pressKey(d, 'f')
	_ = renderToText(t, d, 130, 32)
	return d, snap
}

// finishArrives prepends a newly finished pipeline, refreshes, and redraws, so
// tview's own scroll logic (clamping to the selection, sticking to the end) has
// its say before the test looks.
func finishArrives(t *testing.T, d *Dashboard, snap *gitlab.Snapshot, id int64) {
	t.Helper()
	snap.RecentPipelines = append([]gitlab.Pipeline{{ID: id, ProjectPath: "acme/new", Ref: "main",
		Status: gitlab.StatusSuccess, Finished: time.Now()}}, snap.RecentPipelines...)
	d.current.update(*snap)
	_ = renderToText(t, d, 130, 32)
}

func selectedFinishedID(d *Dashboard) int64 {
	row, _ := d.current.finished.table.GetSelection()
	p, _ := d.current.finishedAt(row)
	return p.ID
}

// TestFinishedFollowsNewestAtTop: with the view at the top, each newly finished
// pipeline shows up at the top as it arrives, the panel holding the keyboard
// or not, and the selection stays on the pipeline it was on. (It once held the
// view still whenever the panel had the keyboard, hiding every arrival.)
func TestFinishedFollowsNewestAtTop(t *testing.T) {
	d, snap := finishedFollowDashboard(t)
	fin := d.current.finished.table
	for n := int64(1); n <= 3; n++ {
		finishArrives(t, d, snap, 2000+n)
		if off, _ := fin.GetOffset(); off != 0 {
			t.Fatalf("after arrival %d the view is at offset %d; the newest is hidden", n, off)
		}
		if got := selectedFinishedID(d); got != 1000 {
			t.Errorf("after arrival %d the selection is on #%d, want #1000", n, got)
		}
	}
}

// TestFinishedSelectionStopsAtBottomEdgeAtTop: at the top, a selection pushed
// down by arrivals stops at the last visible row, because following its
// pipeline past the edge would make tview scroll the view off the top.
func TestFinishedSelectionStopsAtBottomEdgeAtTop(t *testing.T) {
	d, snap := finishedFollowDashboard(t)
	fin := d.current.finished.table
	visible := finishedVisibleRows(fin)
	fin.Select(visible, 0) // the last row in view
	_ = renderToText(t, d, 130, 32)
	finishArrives(t, d, snap, 2001)
	if off, _ := fin.GetOffset(); off != 0 {
		t.Fatalf("the view moved to offset %d; it should stay at the top", off)
	}
	if row, _ := fin.GetSelection(); row != visible {
		t.Errorf("selection on row %d, want the last visible row %d", row, visible)
	}
}

// TestFinishedHoldsStillWhenScrolledDown: scrolled down the list, arrivals
// don't move the rows being read — the offset moves with them, and the
// selection keeps its pipeline — and scrolling back to the top resumes
// following.
func TestFinishedHoldsStillWhenScrolledDown(t *testing.T) {
	d, snap := finishedFollowDashboard(t)
	fin := d.current.finished.table
	fin.Select(25, 0) // well below the fold: the view scrolls down to it
	_ = renderToText(t, d, 130, 32)
	off, _ := fin.GetOffset()
	if off == 0 {
		t.Fatal("selecting row 25 should have scrolled the list")
	}
	top := d.current.finishedPipes[off].ID // the first row in view
	sel := selectedFinishedID(d)

	finishArrives(t, d, snap, 2001)
	newOff, _ := fin.GetOffset()
	if got := d.current.finishedPipes[newOff].ID; got != top {
		t.Errorf("the first row in view is #%d, want #%d still (offset %d → %d)", got, top, off, newOff)
	}
	if got := selectedFinishedID(d); got != sel {
		t.Errorf("selection moved to #%d, want #%d", got, sel)
	}

	// Back to the top, as the wheel or ↑ would leave it: following resumes.
	fin.Select(1, 0)
	_ = renderToText(t, d, 130, 32)
	finishArrives(t, d, snap, 2002)
	if off, _ := fin.GetOffset(); off != 0 {
		t.Errorf("after scrolling back to the top, an arrival left the view at offset %d", off)
	}
}
