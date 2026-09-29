package ui

import (
	"testing"

	"github.com/MarcelInTO/glute/internal/gitlab"
	"github.com/gdamore/tcell/v2"
)

// TestStatusColorDistinguishesRunningFromQueued pins the status palette, and
// in particular that running has a color of its own: on a busy server the rows
// actually executing must be told apart from the ones still queued for a
// runner, which share the "waiting" yellow.
func TestStatusColorDistinguishesRunningFromQueued(t *testing.T) {
	cases := map[gitlab.Status]tcell.Color{
		gitlab.StatusSuccess:            tcell.ColorGreen,
		gitlab.StatusFailed:             tcell.ColorRed,
		gitlab.StatusCanceled:           tcell.ColorSilver,
		gitlab.StatusSkipped:            tcell.ColorSilver,
		gitlab.StatusRunning:            tcell.ColorDodgerBlue,
		gitlab.StatusPending:            tcell.ColorYellow,
		gitlab.StatusCreated:            tcell.ColorYellow,
		gitlab.StatusPreparing:          tcell.ColorYellow,
		gitlab.StatusWaitingForResource: tcell.ColorYellow,
		gitlab.StatusScheduled:          tcell.ColorYellow,
		gitlab.StatusManual:             tcell.ColorWhite,
	}
	for s, want := range cases {
		if got := statusColor(s); got != want {
			t.Errorf("statusColor(%q) = %v, want %v", s, got, want)
		}
	}
	// The point of the split: running is neither the queued color nor the
	// header color, so it can't be mistaken for either.
	running := statusColor(gitlab.StatusRunning)
	if running == statusColor(gitlab.StatusPending) {
		t.Errorf("running and pending share a color; running must stand out")
	}
	if running == tcell.ColorAqua {
		t.Errorf("running uses the header color")
	}
}

func TestPipelineIDText(t *testing.T) {
	if got := pipelineIDText(234923); got != "234923" {
		t.Errorf("pipelineIDText(234923) = %q", got)
	}
	if got := pipelineIDText(0); got != "" {
		t.Errorf("pipelineIDText(0) = %q, want blank — an unknown id is not pipeline #0", got)
	}
}
