package ui

import (
	"strings"

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

// statusColor maps a CI status to a cell color.
func statusColor(s gitlab.Status) tcell.Color {
	switch {
	case s == gitlab.StatusSuccess:
		return tcell.ColorGreen
	case s == gitlab.StatusFailed:
		return tcell.ColorRed
	case s == gitlab.StatusCanceled, s == gitlab.StatusSkipped:
		return tcell.ColorSilver
	case s.IsActive():
		return tcell.ColorYellow
	default:
		return tcell.ColorWhite
	}
}

// newTable builds a bordered, non-selectable table with a fixed header row.
func newTable(title string) *tview.Table {
	t := tview.NewTable()
	t.SetSelectable(false, false)
	t.SetFixed(1, 0)
	t.SetBorder(true)
	t.SetTitle(" " + title + " ")
	t.SetTitleAlign(tview.AlignLeft)
	return t
}

// setHeader writes the bold header row and resets the table body.
func setHeader(t *tview.Table, cols ...string) {
	t.Clear()
	for c, name := range cols {
		cell := tview.NewTableCell(name)
		cell.SetTextColor(tcell.ColorAqua)
		cell.SetAttributes(tcell.AttrBold)
		cell.SetSelectable(false)
		cell.SetExpansion(1)
		t.SetCell(0, c, cell)
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

// emptyRow renders a single muted "(none)" row spanning cols columns.
func emptyRow(t *tview.Table, cols int) {
	none := tview.NewTableCell("(none)")
	none.SetTextColor(tcell.ColorSilver)
	none.SetExpansion(1)
	t.SetCell(1, 0, none)
	for i := 1; i < cols; i++ {
		blank := tview.NewTableCell("")
		blank.SetExpansion(1)
		t.SetCell(1, i, blank)
	}
}
