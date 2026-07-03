package ui

import (
	"strings"
	"testing"

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
	d := NewDashboard(gitlab.FakeService{Snap: snap}, Options{Title: "sample data"})
	d.snapshot = snap
	d.pipelines.update(snap)
	d.jobs.update(snap)
	d.updateHeader()
	d.updateFooter()
	return d
}

func TestDashboardRendersPipelinesTab(t *testing.T) {
	d := newSampleDashboard()
	out := renderToText(t, d, 130, 32)
	t.Logf("Pipelines tab:\n%s", out)

	for _, want := range []string{"glute", "Pipelines", "Jobs", "Running pipelines", "gateway", "success", "RUNS", "updated"} {
		if !strings.Contains(out, want) {
			t.Errorf("pipelines render missing %q", want)
		}
	}
	// Project cells show only the last path segment, not the full path.
	if strings.Contains(out, "acme/payments/api") {
		t.Errorf("project column should be truncated to the last segment, found the full path")
	}
}

func TestHoverRevealsFullPath(t *testing.T) {
	d := newSampleDashboard()
	_ = renderToText(t, d, 130, 32) // draw once so GetInnerRect is populated

	ix, iy, _, _ := d.pipelines.running.table.GetInnerRect()
	if path, ok := d.pipelines.hoverPathAt(ix+1, iy+1); !ok || path != "acme/payments/api" {
		t.Fatalf("hover over first running row = (%q, %v), want acme/payments/api", path, ok)
	}
	if _, ok := d.pipelines.hoverPathAt(ix+1, iy); ok {
		t.Errorf("hover over the header row should reveal nothing")
	}
}

func TestDashboardRendersJobsTab(t *testing.T) {
	d := newSampleDashboard()
	d.selectTab(1)
	out := renderToText(t, d, 130, 32)
	t.Logf("Jobs tab:\n%s", out)

	for _, want := range []string{"Running jobs", "integration-tests", "deploy-staging"} {
		if !strings.Contains(out, want) {
			t.Errorf("jobs render missing %q", want)
		}
	}
}
