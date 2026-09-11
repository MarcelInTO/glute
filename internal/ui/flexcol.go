package ui

import (
	"sort"

	"github.com/MarcelInTO/glute/internal/format"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Flexible identity columns.
//
// Every panel has one or two columns that say *which row this is* — the project,
// the job, the tag or runner, the ref — and a tail of narrow columns that say
// what happened to it. Sizing the identity columns to a constant (which is what
// the fill functions used to do, via format.Trunc) is wrong in both directions:
// on a wide terminal the name is clipped anyway while the numbers drift apart in
// whitespace, and on a narrow one the constant can still be more than the pane
// has to spare — in which case tview drops the columns on the *right*, so the
// stats vanish to keep a name nobody asked to see in full.
//
// So the identity columns are sized on every draw instead, from the data and the
// pane's real width: the other columns take their natural (content) width, and
// what's left over is the identity columns' budget. Their text is elided only
// when it doesn't fit that budget — which on a wide enough terminal means never.
// That's also why format.Elide's collision-avoiding middle cut matters less than
// it used to: it's now the last resort rather than the normal case.

// minFlexWidth is the floor a flexible column is never squeezed below. Narrower
// than this an elided name ("ab…yz") identifies nothing, and the space would do
// the numbers beside it no more good; at that point the pane is too narrow for
// the panel either way and tview clips whatever still doesn't fit.
const minFlexWidth = 8

// nameCell builds a cell for a flexible column. It shows full text to begin
// with and keeps the untruncated string in the cell's Reference, which is what
// fitFlexColumns re-elides from on each draw — the cell's own text can't be the
// source, since by then it may already be an elided form.
func nameCell(full string) *tview.TableCell {
	c := tview.NewTableCell(full)
	c.SetReference(full)
	c.SetExpansion(1)
	return c
}

// flexColumns marks cols of t as the flexible identity columns and installs the
// draw hook that sizes them. Call it once, when the table is built.
func flexColumns(t *tview.Table, cols ...int) {
	// Measure every row, not just the on-screen ones: that keeps a column from
	// changing width as the table scrolls, and makes tview's own measurement —
	// taken from these same cells a moment after the hook runs — agree with the
	// budget we computed.
	t.SetEvaluateAllRows(true)
	t.SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
		// Calling GetInnerRect from inside the hook is safe even though the
		// hook's return value is what caches that rect: SetRect invalidates the
		// cache, so this is either freshly computed or last frame's value for an
		// unchanged rect. We hand it straight back, so it stays a fixed point.
		ix, iy, iw, ih := t.GetInnerRect()
		fitFlexColumns(t, iw, cols)
		return ix, iy, iw, ih
	})
}

// fitFlexColumns gives the flexible columns whatever width is left once every
// other column has its natural width, split max-min fair: a column whose text
// fits inside its equal share keeps all of it, and what it didn't need goes to
// the columns that are still too wide. Only a column that overruns even then is
// elided.
func fitFlexColumns(t *tview.Table, innerWidth int, cols []int) {
	rows, colCount := t.GetRowCount(), t.GetColumnCount()
	if rows == 0 || colCount == 0 || innerWidth <= 0 || len(cols) == 0 {
		return
	}

	flexible := make([]bool, colCount)
	for _, c := range cols {
		if c >= 0 && c < colCount {
			flexible[c] = true
		}
	}

	// What the flexible columns don't get: every other column at its content
	// width, plus the single blank cell tview puts between columns.
	budget := innerWidth - (colCount - 1)
	for c := range colCount {
		if !flexible[c] {
			budget -= columnWidth(t, rows, c)
		}
	}

	// Narrowest first, which is what makes the hand-out max-min fair: a column
	// that wants less than an equal share takes only what it needs, and the
	// remainder is re-divided among the columns still to come.
	type flexCol struct{ col, natural int }
	order := make([]flexCol, 0, len(cols))
	for c := range colCount {
		if flexible[c] {
			order = append(order, flexCol{col: c, natural: naturalWidth(t, rows, c)})
		}
	}
	sort.Slice(order, func(i, j int) bool { return order[i].natural < order[j].natural })

	remaining, left := budget, len(order)
	for _, f := range order {
		w := max(min(f.natural, remaining/left), minFlexWidth)
		elideColumn(t, rows, f.col, w)
		remaining -= w
		left--
	}
}

// columnWidth is the width tview will give column c: its widest cell.
func columnWidth(t *tview.Table, rows, c int) int {
	w := 0
	for r := range rows {
		if cell := t.GetCell(r, c); cell != nil {
			w = max(w, tview.TaggedStringWidth(cell.Text))
		}
	}
	return w
}

// naturalWidth is how wide column c would be with nothing in it elided.
func naturalWidth(t *tview.Table, rows, c int) int {
	w := 0
	for r := range rows {
		cell := t.GetCell(r, c)
		if cell == nil {
			continue
		}
		text := cell.Text
		if full, ok := cell.Reference.(string); ok {
			text = full
		}
		w = max(w, tview.TaggedStringWidth(text))
	}
	return w
}

// elideColumn fits column c into w cells. MaxWidth backs the elision up: it's
// what stops a cell we can't re-elide (one with no full text recorded, like the
// "(none)" placeholder) — or one whose runes are wider than a cell each, which
// format.Elide counts as one — from widening the column past its budget and
// pushing the columns to its right off the pane.
func elideColumn(t *tview.Table, rows, c, w int) {
	for r := range rows {
		cell := t.GetCell(r, c)
		if cell == nil {
			continue
		}
		if full, ok := cell.Reference.(string); ok {
			cell.SetText(format.Elide(full, w))
		}
		cell.SetMaxWidth(w)
	}
}
