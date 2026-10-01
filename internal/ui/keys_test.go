package ui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

// keyRune is a typed character, never a Ctrl chord: tcell gives Ctrl-F the
// rune 'f' too.
func TestKeyRune(t *testing.T) {
	for _, tc := range []struct {
		name string
		ev   *tcell.EventKey
		want rune
	}{
		{"f", tcell.NewEventKey(tcell.KeyRune, 'f', tcell.ModNone), 'f'},
		{"?", tcell.NewEventKey(tcell.KeyRune, '?', tcell.ModShift), '?'},
		{"Ctrl-F", tcell.NewEventKey(tcell.KeyCtrlF, 0, tcell.ModCtrl), 0},
		{"Ctrl-F as a terminal sends it (0x06)", tcell.NewEventKey(tcell.KeyRune, 0x06, tcell.ModNone), 0},
		{"Ctrl-Shift-F", tcell.NewEventKey(tcell.KeyRune, 'F', tcell.ModCtrl|tcell.ModShift), 0},
		{"Enter", tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), 0},
	} {
		if got := keyRune(tc.ev); got != tc.want {
			t.Errorf("%s: keyRune = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Through the real event loop, Ctrl and a letter does what the focused widget
// does with it, not what the letter does: Ctrl-F pages the tree down instead
// of moving to the finished list, and Ctrl-T, Ctrl-R, Ctrl-O and Ctrl-Q do
// nothing.
func TestCtrlLettersAreNotTheirLetters(t *testing.T) {
	d, opened := linkDashboard(t, "")
	l := startLive(t, d)
	l.waitFor("active pipelines")
	for _, k := range []tcell.Key{tcell.KeyCtrlF, tcell.KeyCtrlT, tcell.KeyCtrlR, tcell.KeyCtrlO, tcell.KeyCtrlQ} {
		l.screen.InjectKey(k, 0, tcell.ModNone)
	}
	// ? is queued behind them, so once the help shows, they've all been
	// handled and glute is still running.
	l.screen.InjectKey(tcell.KeyRune, '?', tcell.ModNone)
	l.waitFor("switch tabs")
	l.do(func() {
		if d.current.finishedActive {
			t.Error("Ctrl-F moved the keyboard to the finished list")
		}
		if row, _ := d.current.table.GetSelection(); row <= 1 {
			t.Errorf("after Ctrl-F the tree's selection is on row %d; it should have paged down", row)
		}
		if d.window != 0 {
			t.Errorf("Ctrl-T changed the history window to %v", d.window)
		}
		if len(d.trigger) != 0 {
			t.Error("Ctrl-R triggered a refresh")
		}
		if len(*opened) != 0 {
			t.Errorf("Ctrl-O opened %v", *opened)
		}
	})
	l.quit()
}
