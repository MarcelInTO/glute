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
