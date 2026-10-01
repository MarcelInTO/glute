package ui

import (
	"strconv"
	"strings"
	"time"

	"github.com/MarcelInTO/glute/internal/format"
	"github.com/MarcelInTO/glute/internal/gitlab"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// displayRef shortens a GitLab merge-request pipeline ref
// ("refs/merge-requests/<iid>/head") to "MR <iid>", since the raw ref is long
// and uninformative. Any other ref (a branch or tag) is returned unchanged.
func displayRef(ref string) string {
	const prefix = "refs/merge-requests/"
	if !strings.HasPrefix(ref, prefix) {
		return ref
	}
	iid := ref[len(prefix):]
	if i := strings.IndexByte(iid, '/'); i >= 0 {
		iid = iid[:i] // drop the trailing "/head" or "/merge"
	}
	if iid == "" || strings.IndexFunc(iid, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return ref // not the numeric shape we expected; leave it as-is
	}
	return "MR " + iid
}

// windowSuffix is the " · last 7d" tail a history panel's title carries for the
// window its rows cover, or empty when no window is known yet (the empty first
// render, before the initial refresh names the snapshot's windows).
func windowSuffix(w time.Duration) string {
	if w <= 0 {
		return ""
	}
	return " · last " + format.Window(w)
}

// statusColor maps a CI status to a cell color. Running has a color of its own
// so that on a busy server the rows actually executing stand out from the ones
// merely queued for a runner (pending, created, waiting…), which stay yellow.
// It's DodgerBlue rather than tcell.ColorBlue for the same reason muted text is
// silver rather than gray — the ANSI blue is near-illegible on dark terminals —
// and not aqua, which the headers own.
func statusColor(s gitlab.Status) tcell.Color {
	switch {
	case s == gitlab.StatusSuccess:
		return tcell.ColorGreen
	case s == gitlab.StatusFailed:
		return tcell.ColorRed
	case s == gitlab.StatusCanceled, s == gitlab.StatusSkipped:
		return tcell.ColorSilver
	case s == gitlab.StatusRunning:
		return tcell.ColorDodgerBlue
	case s.IsActive():
		return tcell.ColorYellow
	default:
		return tcell.ColorWhite
	}
}

// pipelineIDText renders a pipeline's instance-wide id for an ID column, or
// nothing when it isn't known — a zero id is a placeholder, not pipeline #0.
func pipelineIDText(id int64) string {
	if id <= 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}

// newTable builds a bordered, non-selectable table with a fixed header row,
// guarded for when it's made selectable (see guardSelection).
func newTable(title string) *tview.Table {
	t := tview.NewTable()
	guardSelection(t)
	t.SetSelectable(false, false)
	t.SetFixed(1, 0)
	t.SetBorder(true)
	t.SetTitle(" " + title + " ")
	t.SetTitleAlign(tview.AlignLeft)
	return t
}

// setHeader writes the bold header row and resets the table body. Each header
// keeps its own text in the cell's Reference so that a header sitting over a
// flexible column elides with the column (see flexcol.go) instead of being the
// one cell that refuses to shrink and widens it.
func setHeader(t *tview.Table, cols ...string) {
	t.Clear()
	for c, name := range cols {
		cell := tview.NewTableCell(name)
		cell.SetReference(name)
		cell.SetTextColor(tcell.ColorAqua)
		cell.SetAttributes(tcell.AttrBold)
		cell.SetSelectable(false)
		cell.SetExpansion(1)
		t.SetCell(0, c, cell)
	}
}

// rightAlignHeaders right-justifies the given header columns, so a numeric
// column's header lines up over its right-aligned cells (the display convention
// the Current tab's setCurrentHeader and finished panel also follow).
func rightAlignHeaders(t *tview.Table, cols ...int) {
	for _, c := range cols {
		if cell := t.GetCell(0, c); cell != nil {
			cell.SetAlign(tview.AlignRight)
		}
	}
}

func textCell(text string) *tview.TableCell {
	c := tview.NewTableCell(text)
	c.SetExpansion(1)
	return c
}

func numCell(text string) *tview.TableCell {
	c := tview.NewTableCell(text)
	c.SetExpansion(1)
	c.SetAlign(tview.AlignRight)
	return c
}

func statusCell(s gitlab.Status) *tview.TableCell {
	c := tview.NewTableCell(string(s))
	c.SetTextColor(statusColor(s))
	c.SetExpansion(1)
	return c
}

// emptyRow renders a single muted "(none)" row spanning cols columns, with the
// text in the first column.
func emptyRow(t *tview.Table, cols int) {
	emptyRowAt(t, cols, 0)
}

// emptyRowAt is emptyRow with the "(none)" text in column at — for a panel
// whose first column is a narrow numeric one (the finished panel's ID), where
// the placeholder belongs under the name column beside it instead.
func emptyRowAt(t *tview.Table, cols, at int) {
	for i := 0; i < cols; i++ {
		cell := tview.NewTableCell("")
		if i == at {
			cell.SetText("(none)")
			cell.SetTextColor(tcell.ColorSilver)
		}
		cell.SetExpansion(1)
		t.SetCell(1, i, cell)
	}
}

// link is the Reference an id cell carries: the page it opens. It lives in
// the Reference because tcell has no getter for a style's URL, and a click
// needs to find it (urlAt). Flexible columns keep a string there instead
// (see nameCell); an id column is never flexible, so the two don't meet.
type link struct{ url string }

// linkCell is a right-aligned, content-sized id cell that links to url: it's
// underlined and carries a terminal hyperlink (OSC 8), which a terminal that
// supports them opens on a modifier-click, on the user's own machine even
// with glute running over SSH. It also holds url as its Reference, so glute
// can open it on a plain click (see browser.go). With no url, or no text to
// click, it's just curNumCell.
func linkCell(text, url string) *tview.TableCell {
	c := curNumCell(text)
	if url == "" || text == "" {
		return c
	}
	c.SetStyle(c.Style.Underline(true).Url(url))
	c.SetReference(link{url})
	return c
}

// urlAt returns the link on the cell at screen position (x, y) of t, if that
// cell is a linkCell. CellAt counts the scroll offset, so it's right on a
// scrolled table.
func urlAt(t *tview.Table, x, y int) (string, bool) {
	row, col := t.CellAt(x, y)
	if row < 0 || col < 0 {
		return "", false
	}
	if cell := t.GetCell(row, col); cell != nil {
		if l, ok := cell.Reference.(link); ok {
			return l.url, true
		}
	}
	return "", false
}
