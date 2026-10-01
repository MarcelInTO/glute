package ui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// guardSelection keeps a movement key from hanging t when it has nothing to
// select (issue #1, rivo/tview#1146).
//
// When a selectable tview v0.42.0 Table has no selectable cell (the Current
// tree's header over "(nothing running)" is one), its Draw walks the selection
// past the last row looking for one. A movement key then searches for a cell to
// move to that stops only on a selectable cell or on the cell the selection
// started from. That cell is outside the table, so ↑ ↓ j k PgUp PgDn spin
// forever on the UI goroutine. The whole app freezes, Ctrl-C included, which
// is a key event waiting on the same loop.
//
// So while the selection is outside the table, which after a draw means there
// is nothing to select, the movement keys are dropped: there's nowhere to move
// to. tview fixed it in 051ada1 (the search's stop cell is clamped into the
// table), which no tagged release has yet; this can go once glute moves to one
// that does.
//
// Every table that can be selectable gets this. The finished list's "(none)"
// and the detail view's placeholders happen to keep a selectable blank cell, so
// they never hung, but nothing depends on that. It's the table's input capture,
// so a later SetInputCapture on the same table would replace it.
func guardSelection(t *tview.Table) {
	t.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if movesSelection(ev) && selectionOutside(t) {
			return nil
		}
		return ev
	})
}

// movesSelection reports whether ev is one of the keys a tview Table moves its
// selection on.
func movesSelection(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyUp, tcell.KeyDown, tcell.KeyLeft, tcell.KeyRight,
		tcell.KeyHome, tcell.KeyEnd, tcell.KeyPgUp, tcell.KeyPgDn,
		tcell.KeyCtrlF, tcell.KeyCtrlB:
		return true
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'j', 'k', 'h', 'l', 'g', 'G':
			return true
		}
	}
	return false
}

// selectionOutside reports whether t is selectable and its selection isn't on
// one of its cells. A table that isn't selectable scrolls on those keys
// instead, which can't hang.
func selectionOutside(t *tview.Table) bool {
	if rows, cols := t.GetSelectable(); !rows && !cols {
		return false
	}
	row, col := t.GetSelection()
	return row < 0 || row >= t.GetRowCount() || col < 0 || col >= t.GetColumnCount()
}
