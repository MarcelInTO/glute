package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/MarcelInTO/glute/internal/gitlab"
	"github.com/gdamore/tcell/v2"
)

// renderToText draws the dashboard onto an in-memory screen and returns the
// visible text, so the TUI can be exercised without a real terminal.
func renderToText(t *testing.T, d *Dashboard, w, h int) string {
	t.Helper()
	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatalf("sim screen init: %v", err)
	}
	screen.SetSize(w, h)
	d.outer.SetRect(0, 0, w, h)
	d.outer.Draw(screen)
	screen.Sync()

	cells, cw, ch := screen.GetContents()
	var b strings.Builder
	for y := 0; y < ch; y++ {
		var line strings.Builder
		for x := 0; x < cw; x++ {
			runes := cells[y*cw+x].Runes
			if len(runes) == 0 || runes[0] == 0 {
				line.WriteByte(' ')
			} else {
				line.WriteRune(runes[0])
			}
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
		b.WriteByte('\n')
	}
	return b.String()
}

func newSampleDashboard() *Dashboard {
	snap := gitlab.SampleSnapshot()
	return newDashboardShowing(gitlab.FakeService{Snap: snap}, snap)
}

// newDashboardShowing builds a dashboard over svc with snap already rendered,
// as if its first refresh had landed.
func newDashboardShowing(svc gitlab.Service, snap gitlab.Snapshot) *Dashboard {
	d := NewDashboard(svc, Options{Title: "sample data"})
	d.snapshot = snap
	d.current.update(snap)
	d.renderHistory()
	d.updateHeader()
	d.updateFooter()
	return d
}

func TestDashboardRendersCurrentTab(t *testing.T) {
	d := newSampleDashboard() // Current is the default (first) tab
	out := renderToText(t, d, 130, 32)
	t.Logf("Current tab:\n%s", out)

	for _, want := range []string{"glute", "Current", "active pipelines", "unit-tests", "integration-tests", "RUNNER", "shared-linux-02", "USER", "jchen", "priya"} {
		if !strings.Contains(out, want) {
			t.Errorf("current render missing %q", want)
		}
	}
	// The downstream child pipeline is shown, marked and indented under its root.
	if !strings.Contains(out, "↳") {
		t.Errorf("current render missing the downstream child marker")
	}
}

// TestCurrentTabShowsRecentlyFinished checks the bottom panel lists the recently
// finished pipelines (newest first) so an outcome stays visible after the
// pipeline leaves the active tree above.
func TestCurrentTabShowsRecentlyFinished(t *testing.T) {
	d := newSampleDashboard() // Current is the default (first) tab
	out := renderToText(t, d, 130, 32)
	t.Logf("Current tab:\n%s", out)

	// The panel title plus a ref that only the finished panel carries on this tab
	// (the failed gateway run), so we know it's the finished panel we're seeing.
	for _, want := range []string{"Recently finished", "feat/rate-limit", "failed", "DURATION", "WHEN"} {
		if !strings.Contains(out, want) {
			t.Errorf("current render missing recently-finished content %q", want)
		}
	}
}

// TestCurrentFinishedPanelHoverRevealsFullPath checks the finished panel
// supports the mouse-hover path reveal, which the tree above doesn't use (it's
// scrolled from the keyboard, so it reveals via selection instead).
// TestFinishedHoverFollowsScroll covers the hover once the panel has scrolled.
func TestCurrentFinishedPanelHoverRevealsFullPath(t *testing.T) {
	d := newSampleDashboard()
	_ = renderToText(t, d, 130, 32) // draw once so GetInnerRect is populated

	ix, iy, _, _ := d.current.finished.table.GetInnerRect()
	// Row 0 is the header; the first data row (newest finished) is acme/payments/api.
	if path, ok := d.current.finished.hoverAt(ix+1, iy+1); !ok || path != "acme/payments/api" {
		t.Fatalf("hover over first finished row = (%q, %v), want acme/payments/api", path, ok)
	}
	if _, ok := d.current.finished.hoverAt(ix+1, iy); ok {
		t.Errorf("hover over the header row should reveal nothing")
	}
}

func TestCurrentTabAppliesRunnerAliases(t *testing.T) {
	snap := gitlab.SampleSnapshot()
	d := NewDashboard(gitlab.FakeService{Snap: snap}, Options{
		Title:         "sample data",
		RunnerAliases: map[string]string{"shared-linux-02": "lnx-2"},
	})
	d.current.update(snap)
	out := renderToText(t, d, 130, 32)
	t.Logf("Current tab (aliased):\n%s", out)

	if !strings.Contains(out, "lnx-2") {
		t.Errorf("aliased runner name %q not shown", "lnx-2")
	}
	if strings.Contains(out, "shared-linux-02") {
		t.Errorf("aliased runner should not show its full name")
	}
	// A runner without an alias keeps its real name.
	if !strings.Contains(out, "docker-builder") {
		t.Errorf("unaliased runner should show its real name")
	}
}

func TestCurrentSelectionRevealsFullPath(t *testing.T) {
	d := newSampleDashboard()
	_ = renderToText(t, d, 130, 32) // draw once so the table has data rows

	// Row 1 is the first pipeline (acme/payments/api); the header is row 0.
	if path, ok := d.current.pathAtRow(1); !ok || path != "acme/payments/api" {
		t.Fatalf("pathAtRow(1) = (%q, %v), want acme/payments/api", path, ok)
	}
	if _, ok := d.current.pathAtRow(0); ok {
		t.Errorf("the header row should reveal no path")
	}
}

// TestCurrentTickReTimesOnlyLiveRows checks that the per-second tick rewrites
// the TIME cell of running rows (so counters count up between refreshes) while
// leaving finished rows' fixed durations untouched.
func TestCurrentTickReTimesOnlyLiveRows(t *testing.T) {
	v := newCurrentView(nil)
	now := time.Now()
	snap := gitlab.Snapshot{Current: []gitlab.ActivePipeline{{
		Pipeline: gitlab.Pipeline{
			ProjectPath: "acme/api", Ref: "main", Status: gitlab.StatusRunning,
			Started: now.Add(-90 * time.Second),
		},
		Jobs: []gitlab.Job{
			{Stage: "build", Name: "compile", Status: gitlab.StatusSuccess, Duration: 45 * time.Second},
			{Stage: "test", Name: "unit", Status: gitlab.StatusRunning, Started: now.Add(-30 * time.Second)},
		},
	}}}
	v.update(snap)

	// Rows: 0 header, 1 pipeline (live), 2 finished job, 3 running job.
	const timeCol = curColTime
	pipeCell := v.table.GetCell(1, timeCol)
	doneCell := v.table.GetCell(2, timeCol)
	liveCell := v.table.GetCell(3, timeCol)

	doneBefore := doneCell.Text
	// Sentinels prove which cells tick() rewrites and which it leaves alone.
	pipeCell.SetText("SENTINEL")
	liveCell.SetText("SENTINEL")
	doneCell.SetText("FIXED")

	v.tick()

	if pipeCell.Text == "SENTINEL" {
		t.Errorf("tick did not re-time the running pipeline row")
	}
	if liveCell.Text == "SENTINEL" {
		t.Errorf("tick did not re-time the running job row")
	}
	if doneCell.Text != "FIXED" {
		t.Errorf("tick rewrote a finished row's TIME cell (%q); it should be left alone", doneCell.Text)
	}
	// The finished job's duration is fixed at 45s regardless of the tick.
	if want := "     45"; !strings.HasSuffix(doneBefore, "45") || strings.TrimSpace(doneBefore) != strings.TrimSpace(want) {
		t.Errorf("finished job TIME = %q, want 45s duration", doneBefore)
	}
}

func TestDashboardRendersWorkTab(t *testing.T) {
	d := newSampleDashboard()
	d.selectTab(1)
	out := renderToText(t, d, 130, 32)
	t.Logf("Work tab:\n%s", out)

	// The four panels — pipeline analysis on top, job history below — their
	// columns, and a value from each: the fails/slowest project (gateway) and a
	// job name from the recent/top job panels.
	for _, want := range []string{
		"glute", "Work", "Infrastructure",
		"Fails most often", "Slowest", "Recent jobs", "Top jobs",
		"RUNS", "MEAN", "P95", "DURATION", "SUCCESS",
		"gateway", "deploy-staging", "updated",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("work render missing %q", want)
		}
	}
	// Project cells show only the last path segment, not the full path.
	if strings.Contains(out, "acme/payments/api") {
		t.Errorf("project column should be truncated to the last segment, found the full path")
	}
	// The capacity panels belong to the Infrastructure tab, not here.
	for _, unwanted := range []string{"Tag performance", "Runner performance"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("work render should not show %q", unwanted)
		}
	}
}

func TestHoverRevealsFullPath(t *testing.T) {
	d := newSampleDashboard()
	d.selectTab(1)                  // Work
	_ = renderToText(t, d, 130, 32) // draw once so GetInnerRect is populated

	// The "fails most often" panel is project-keyed; its first row is the
	// highest-fail-rate project (gateway, 21/141), so a hover there reveals the
	// full path behind the truncated cell.
	ix, iy, _, _ := d.work.fails.table.GetInnerRect()
	if path, ok := d.work.hoverPathAt(ix+1, iy+1); !ok || path != "acme/platform/gateway" {
		t.Fatalf("hover over first fails row = (%q, %v), want acme/platform/gateway", path, ok)
	}
	if _, ok := d.work.hoverPathAt(ix+1, iy); ok {
		t.Errorf("hover over the header row should reveal nothing")
	}
}

func TestDashboardRendersInfraTab(t *testing.T) {
	d := newSampleDashboard()
	d.selectTab(2)
	out := renderToText(t, d, 130, 32)
	t.Logf("Infrastructure tab:\n%s", out)

	// Both capacity panels, their shared columns, and a value from each keying:
	// a runner tag (windows, plus the untagged bucket) and a runner.
	for _, want := range []string{
		"Tag performance", "Runner performance",
		"TAG", "RUNNER", "JOBS", "COMPUTE", "QUEUE",
		"windows", "(untagged)", "docker-builder",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("infrastructure render missing %q", want)
		}
	}
	// The running-jobs panel was dropped as redundant with the Current tab.
	if strings.Contains(out, "Running jobs") {
		t.Errorf("infrastructure render should not show a running-jobs panel")
	}
}

// TestInfraTabAppliesRunnerAliases checks the runner panel honours the
// configured short labels, like the Current tab's RUNNER column does.
func TestInfraTabAppliesRunnerAliases(t *testing.T) {
	snap := gitlab.SampleSnapshot()
	d := NewDashboard(gitlab.FakeService{Snap: snap}, Options{
		RunnerAliases: map[string]string{"docker-builder": "dkr"},
	})
	d.infra.update(snap.WindowStats)
	d.selectTab(2)
	out := renderToText(t, d, 130, 32)
	t.Logf("Infrastructure tab (aliased):\n%s", out)

	if !strings.Contains(out, "dkr") {
		t.Errorf("aliased runner name %q not shown", "dkr")
	}
	if strings.Contains(out, "docker-builder") {
		t.Errorf("aliased runner should not show its full name")
	}
}
