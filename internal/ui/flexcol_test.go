package ui

import (
	"strings"
	"testing"

	"github.com/MarcelInTO/glute/internal/gitlab"
)

// longProject is the shape that motivated the flexible columns: a real
// watchlist's names are long and share a conventional prefix, so a column sized
// to a constant either clips them all to the same string or crowds out the
// numbers beside it.
const longProject = "payments-platform-transaction-api"

// longSampleDashboard is the sample dashboard with the project paths rewritten
// long, so a panel has to make a width decision it can get wrong.
func longSampleDashboard() *Dashboard {
	snap := gitlab.SampleSnapshot()
	long := map[string]string{
		"acme/payments/api":     "acme/payments/" + longProject,
		"acme/payments/web":     "acme/payments/payments-platform-customer-web",
		"acme/platform/gateway": "acme/platform/payments-platform-edge-gateway",
	}
	fix := func(p string) string {
		if l, ok := long[p]; ok {
			return l
		}
		return p
	}
	var walk func([]gitlab.ActivePipeline)
	walk = func(aps []gitlab.ActivePipeline) {
		for i := range aps {
			aps[i].ProjectPath = fix(aps[i].ProjectPath)
			walk(aps[i].Children)
		}
	}
	walk(snap.Current)
	for i := range snap.RecentPipelines {
		snap.RecentPipelines[i].ProjectPath = fix(snap.RecentPipelines[i].ProjectPath)
	}
	for i := range snap.PipelineStats {
		snap.PipelineStats[i].ProjectPath = fix(snap.PipelineStats[i].ProjectPath)
	}
	for i := range snap.RecentJobs {
		snap.RecentJobs[i].ProjectPath = fix(snap.RecentJobs[i].ProjectPath)
	}
	for i := range snap.TopJobs {
		snap.TopJobs[i].ProjectPath = fix(snap.TopJobs[i].ProjectPath)
	}

	d := NewDashboard(gitlab.FakeService{Snap: snap}, Options{Title: "sample data"})
	d.snapshot = snap
	d.current.update(snap)
	d.renderHistory()
	d.updateHeader()
	d.updateFooter()
	return d
}

// TestFlexColumnShowsFullNameWhenItFits is the headline case: on a wide pane the
// identity column is sized from the data, so a name that used to be cut at a
// constant now shows whole — and nothing else loses room for it.
func TestFlexColumnShowsFullNameWhenItFits(t *testing.T) {
	d := longSampleDashboard()
	d.selectTab(1) // Work: "fails most often" is keyed by project
	out := renderToText(t, d, 160, 32)
	t.Logf("Work tab @ 160:\n%s", out)

	if !strings.Contains(out, longProject) {
		t.Errorf("wide render truncated %q, which fits", longProject)
	}
	for _, want := range []string{"RUNS", "FAIL%", "MEAN", "P95", "SUCCESS"} {
		if !strings.Contains(out, want) {
			t.Errorf("wide render lost the %q column to the name column", want)
		}
	}
}

// TestFlexColumnYieldsToStatsOnNarrowPane is the other direction, and the bug
// the hook exists to prevent: tview lays columns out left to right and drops the
// ones on the *right* that no longer fit, so an unbounded name column doesn't
// merely crowd the numbers — it deletes them. The name must give way instead.
func TestFlexColumnYieldsToStatsOnNarrowPane(t *testing.T) {
	d := longSampleDashboard() // Current is the default tab
	out := renderToText(t, d, 64, 32)
	t.Logf("Current tab @ 64:\n%s", out)

	for _, want := range []string{"STATUS", "RUNNER", "TIME", "DONE", "1/4"} {
		if !strings.Contains(out, want) {
			t.Errorf("narrow render dropped %q; the name column took the pane", want)
		}
	}
	// And the name did give way, rather than overflowing.
	if !strings.Contains(out, "…") {
		t.Errorf("narrow render shows no elision, so something must have overflowed")
	}
}

// TestFlexColumnsShareByNeed checks the max-min split between two identity
// columns: the recent-jobs panel's JOB names are far shorter than its project
// paths, so JOB should show in full and PROJECT absorb the shortfall — not both
// be cut to the same fraction.
func TestFlexColumnsShareByNeed(t *testing.T) {
	d := longSampleDashboard()
	d.selectTab(1)
	out := renderToText(t, d, 130, 32)
	t.Logf("Work tab @ 130:\n%s", out)

	for _, want := range []string{"DURATION", "WHEN", "SUCCESS"} {
		if !strings.Contains(out, want) {
			t.Errorf("render dropped the %q column", want)
		}
	}
	// Read the panel's own cells rather than the screen: the wider panels on the
	// same tab have room for the full name, so finding it *somewhere* says
	// nothing about this one. Drawing is what elides, so the cells now hold what
	// was drawn.
	recent := d.work.recent.table
	if job := recent.GetCell(1, 1).Text; job != "unit-tests" {
		t.Errorf("JOB cell = %q, want the full %q — it was inside its share", job, "unit-tests")
	}
	// The shortfall landed entirely on the column that was over its share, which
	// is the point of a max-min split; a proportional one would cut both.
	if project := recent.GetCell(1, 0).Text; !strings.Contains(project, "…") {
		t.Errorf("PROJECT cell = %q, want it cut — the pane can't fit both columns whole", project)
	}
}
