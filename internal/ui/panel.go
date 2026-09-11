package ui

import "github.com/rivo/tview"

// panelTable is a bordered table plus the full project path for each data row.
// Cells display only the last path segment; hoverAt recovers the full path for
// the row under the cursor.
type panelTable struct {
	table *tview.Table
	paths []string
}

// newPanelTable builds a panel whose identity columns — the free-text ones that
// say which row this is — are flex: they take whatever width the pane has left
// over and are elided only when it isn't enough (see flexcol.go). Passing no
// columns leaves every column sized to its own content.
func newPanelTable(title string, flex ...int) *panelTable {
	t := newTable(title)
	flexColumns(t, flex...)
	return &panelTable{table: t}
}

// setTitle replaces the panel's border title (padded like newTable does).
func (p *panelTable) setTitle(title string) {
	p.table.SetTitle(" " + title + " ")
}

// reset writes the bold header row and clears recorded paths.
func (p *panelTable) reset(cols ...string) {
	setHeader(p.table, cols...)
	p.paths = p.paths[:0]
}

// addPath records the full project path for the next data row, in order.
func (p *panelTable) addPath(fullPath string) {
	p.paths = append(p.paths, fullPath)
}

// hoverAt returns the full project path for the row under (x, y), if any.
func (p *panelTable) hoverAt(x, y int) (string, bool) {
	ix, iy, iw, ih := p.table.GetInnerRect()
	if x < ix || x >= ix+iw || y < iy || y >= iy+ih {
		return "", false
	}
	idx := (y - iy) - 1 // table row 0 is the header
	if idx < 0 || idx >= len(p.paths) {
		return "", false
	}
	return p.paths[idx], true
}
