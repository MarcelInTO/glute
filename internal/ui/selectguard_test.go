package ui

import (
	"testing"
	"time"

	"github.com/MarcelInTO/glute/internal/gitlab"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// movementKeys are the keys a tview Table moves its selection on.
var movementKeys = []struct {
	key  tcell.Key
	r    rune
	name string
}{
	{tcell.KeyDown, 0, "↓"}, {tcell.KeyUp, 0, "↑"}, {tcell.KeyPgDn, 0, "PgDn"}, {tcell.KeyPgUp, 0, "PgUp"},
	{tcell.KeyCtrlF, 0, "Ctrl-F"}, {tcell.KeyCtrlB, 0, "Ctrl-B"}, {tcell.KeyHome, 0, "Home"}, {tcell.KeyEnd, 0, "End"},
	{tcell.KeyLeft, 0, "←"}, {tcell.KeyRight, 0, "→"},
	{tcell.KeyRune, 'j', "j"}, {tcell.KeyRune, 'k', "k"}, {tcell.KeyRune, 'g', "g"},
	{tcell.KeyRune, 'G', "G"}, {tcell.KeyRune, 'h', "h"}, {tcell.KeyRune, 'l', "l"},
}

// A selectable table with no selectable cell, a header over a placeholder,
// takes every movement key without hanging (issue #1). The draw that parks the
// selection past the last row is what sets the hang up, so the table is drawn
// first.
func TestGuardSelectionWithNothingToSelect(t *testing.T) {
	tbl := newTable("panel")
	setHeader(tbl, "ID", "NAME", "STATUS")
	for c := range 3 {
		cell := blankCell()
		if c == 1 {
			cell.SetText("(nothing running)")
		}
		tbl.SetCell(1, c, cell)
	}
	tbl.SetSelectable(true, false)

	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatalf("sim screen init: %v", err)
	}
	screen.SetSize(60, 10)
	tbl.SetRect(0, 0, 60, 10)
	tbl.Draw(screen)
	if row, _ := tbl.GetSelection(); row != tbl.GetRowCount() {
		t.Fatalf("drawn, the selection is on row %d; this test needs tview to park it past the last row (%d)", row, tbl.GetRowCount())
	}

	handle := tbl.InputHandler()
	for _, k := range movementKeys {
		done := make(chan struct{})
		go func() {
			handle(tcell.NewEventKey(k.key, k.r, tcell.ModNone), func(tview.Primitive) {})
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(liveTimeout):
			t.Fatalf("%s hung the table's key handler", k.name)
		}
	}
}

// Issue #1, through the real event loop: with nothing running, the Current
// tree is a header over "(nothing running)", none of it selectable, and it has
// the keyboard. Every movement key reaches it, and Ctrl-C still quits.
func TestMovementKeysOnNothingRunning(t *testing.T) {
	snap := gitlab.SampleSnapshot()
	snap.Current = nil
	l := startLive(t, newDashboardShowing(gitlab.FakeService{Snap: snap}, snap))
	l.waitFor("(nothing running)")
	for _, k := range movementKeys {
		l.screen.InjectKey(k.key, k.r, tcell.ModNone)
	}
	l.quit()
}

// Where there is something to select, the movement keys move the selection as
// before.
func TestMovementKeysStillMoveTheSelection(t *testing.T) {
	snap := gitlab.SampleSnapshot()
	l := startLive(t, newDashboardShowing(gitlab.FakeService{Snap: snap}, snap))
	l.waitFor("active pipelines")
	l.screen.InjectKey(tcell.KeyDown, 0, tcell.ModNone)
	l.screen.InjectKey(tcell.KeyDown, 0, tcell.ModNone)
	deadline := time.Now().Add(liveTimeout)
	for {
		var row int
		l.do(func() { row, _ = l.d.current.table.GetSelection() })
		if row == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after ↓ ↓ the tree's selection is on row %d, want 3", row)
		}
		time.Sleep(10 * time.Millisecond)
	}
	l.quit()
}
