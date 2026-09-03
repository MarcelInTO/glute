package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/MarcelInTO/glute/internal/gitlab"
	"github.com/gdamore/tcell/v2"
)

func pressKey(d *Dashboard, r rune) {
	d.onKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
}

// TestWindowKeyCyclesHistoryWindow presses `t` and checks the history window
// advances 30d → 1d → 7d → 30d across every windowed panel at once: the Work
// tab's titles, the Infrastructure tab's titles, and the header's indicator.
func TestWindowKeyCyclesHistoryWindow(t *testing.T) {
	d := newSampleDashboard()
	d.selectTab(1) // Work

	want := []string{"last 30d", "last 1d", "last 7d", "last 30d"}
	for i, label := range want {
		if i > 0 {
			pressKey(d, 't')
		}
		out := renderToText(t, d, 130, 32)
		t.Logf("Work tab after %d press(es):\n%s", i, out)
		// Header indicator plus the three windowed panel titles; the recent-jobs
		// panel isn't windowed and must not pick up the suffix.
		for _, s := range []string{
			"window " + label,
			"Fails most often · " + label,
			"Slowest · " + label,
			"Top jobs · avg length · " + label,
			"Recent jobs · failures & successes",
		} {
			if !strings.Contains(out, s) {
				t.Errorf("after %d press(es): missing %q", i, s)
			}
		}
		for _, other := range want {
			if other != label && strings.Contains(out, "· "+other) {
				t.Errorf("after %d press(es): stale window label %q still shown", i, other)
			}
		}
	}

	// The same selection drives the Infrastructure tab: at the current 30d step,
	// one more press lands on 1d there too.
	pressKey(d, 't')
	d.selectTab(2)
	out := renderToText(t, d, 130, 32)
	t.Logf("Infrastructure tab at 1d:\n%s", out)
	for _, s := range []string{"Tag performance · last 1d", "Runner performance · last 1d", "window last 1d"} {
		if !strings.Contains(out, s) {
			t.Errorf("infrastructure at 1d: missing %q", s)
		}
	}
}

// TestWindowKeyChangesPanelData checks the switch actually re-fills the panels
// from the selected window's aggregates, not just the titles: the sample's 30d
// gateway project runs 141 pipelines; its 7d window is scaled to a third of
// that, and at 1d the fails panel goes empty (nothing clears the run floor).
func TestWindowKeyChangesPanelData(t *testing.T) {
	d := newSampleDashboard()
	d.selectTab(1)

	full := renderToText(t, d, 130, 32)
	if !strings.Contains(full, "141") {
		t.Fatalf("30d Work tab should show gateway's 141 runs:\n%s", full)
	}

	pressKey(d, 't') // 1d
	day := renderToText(t, d, 130, 32)
	if strings.Contains(day, "141") {
		t.Errorf("1d Work tab still shows the 30d run count:\n%s", day)
	}
	if !strings.Contains(day, "(none)") {
		t.Errorf("1d fails panel should be empty below the run floor:\n%s", day)
	}

	pressKey(d, 't') // 7d
	week := renderToText(t, d, 130, 32)
	if !strings.Contains(week, "32") { // 141 * 7/30 = 32.9, truncated
		t.Errorf("7d Work tab should show gateway's scaled 32 runs:\n%s", week)
	}
}

// TestWindowKeyBeforeFirstSnapshotIsNoop presses `t` on a dashboard that has no
// snapshot yet (no windows to cycle) and checks nothing breaks and the header
// stays quiet about a window it can't name.
func TestWindowKeyBeforeFirstSnapshotIsNoop(t *testing.T) {
	d := NewDashboard(gitlab.FakeService{}, Options{Title: "t"})
	d.updateHeader()
	pressKey(d, 't')
	if d.window != 0 {
		t.Errorf("window changed to %v with no snapshot; want the zero default", d.window)
	}
	out := renderToText(t, d, 130, 32)
	if strings.Contains(out, "window last") {
		t.Errorf("header names a window before any snapshot:\n%s", out)
	}
	// The windowed panels render with their bare titles until a window is known.
	d.selectTab(1)
	out = renderToText(t, d, 130, 32)
	if !strings.Contains(out, "Fails most often") || strings.Contains(out, "Fails most often ·") {
		t.Errorf("pre-snapshot Work tab should show bare panel titles:\n%s", out)
	}
}

// TestWindowSelectionSurvivesRefresh checks a refresh re-renders at the window
// the user picked rather than snapping back to the full window.
func TestWindowSelectionSurvivesRefresh(t *testing.T) {
	d := newSampleDashboard()
	pressKey(d, 't') // 1d
	if d.window != 24*time.Hour {
		t.Fatalf("window after one press = %v, want 24h", d.window)
	}
	// Mimic doRefresh's UI-side apply with a fresh snapshot.
	snap := gitlab.SampleSnapshot()
	d.snapshot = snap
	d.current.update(snap)
	d.renderHistory()
	d.updateHeader()

	d.selectTab(2)
	out := renderToText(t, d, 130, 32)
	if !strings.Contains(out, "Tag performance · last 1d") {
		t.Errorf("refresh reset the selected window:\n%s", out)
	}
}
