package ui

import (
	"strings"
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

// liveTimeout bounds every wait on the event loop. A loop stuck in a key
// handler never answers, so a wait that runs out is the failure being tested.
const liveTimeout = 3 * time.Second

// liveApp runs a dashboard's real event loop on an in-memory screen, so keys
// go through tview's own dispatch (the app's input capture, the pages, the
// focused widget) instead of straight to onKey.
type liveApp struct {
	t      *testing.T
	d      *Dashboard
	screen tcell.SimulationScreen
	done   chan error
}

func startLive(t *testing.T, d *Dashboard) *liveApp {
	t.Helper()
	screen := tcell.NewSimulationScreen("")
	d.app.SetScreen(screen) // initializes it
	screen.SetSize(120, 30)
	l := &liveApp{t: t, d: d, screen: screen, done: make(chan error, 1)}
	go func() { l.done <- d.app.Run() }()
	return l
}

// do runs f on the event loop and waits for it.
func (l *liveApp) do(f func()) {
	l.t.Helper()
	ran := make(chan struct{})
	go l.d.app.QueueUpdate(func() { f(); close(ran) })
	select {
	case <-ran:
	case <-time.After(liveTimeout):
		l.t.Fatal("the event loop is stuck: it never ran a queued update")
	}
}

// waitFor waits until the screen shows want. Keys and queued updates reach the
// loop by different channels, so a check after a key has to poll.
func (l *liveApp) waitFor(want string) {
	l.t.Helper()
	deadline := time.Now().Add(liveTimeout)
	for {
		var b strings.Builder
		l.do(func() { // on the loop, so it can't race a draw
			cells, w, _ := l.screen.GetContents()
			for i, c := range cells {
				if len(c.Runes) == 0 || c.Runes[0] == 0 {
					b.WriteByte(' ')
				} else {
					b.WriteRune(c.Runes[0])
				}
				if (i+1)%w == 0 {
					b.WriteByte('\n')
				}
			}
		})
		if strings.Contains(b.String(), want) {
			return
		}
		if time.Now().After(deadline) {
			l.t.Fatalf("the screen never showed %q:\n%s", want, b.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// quit presses Ctrl-C and waits for the loop to stop. Ctrl-C is queued behind
// every key pressed before it, so a key handler that never returns fails here.
func (l *liveApp) quit() {
	l.t.Helper()
	l.screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	select {
	case err := <-l.done:
		if err != nil {
			l.t.Fatalf("Run: %v", err)
		}
	case <-time.After(liveTimeout):
		l.t.Fatal("Ctrl-C didn't stop glute: the event loop is stuck in a key handler")
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
		if k.key == tcell.KeyCtrlF {
			// tcell gives a Ctrl-letter key its letter's rune, so glute's f
			// binding takes Ctrl-F, and the keys after it would go to the
			// finished list instead.
			continue
		}
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
