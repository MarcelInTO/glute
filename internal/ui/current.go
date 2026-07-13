package ui

import (
	"fmt"
	"strings"

	"github.com/MarcelInTO/glute/internal/format"
	"github.com/MarcelInTO/glute/internal/gitlab"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// currentView is the Current tab: a single, full-height table that renders the
// active-pipeline tree — each root pipeline, its jobs, and its downstream child
// pipelines — indented to show the hierarchy. It's the "what's happening right
// now" monitoring view. Unlike the other tabs' panels the table is selectable so
// it scrolls with the arrow keys; the selected row's full project path is
// surfaced in the footer (in place of the mouse-hover other tabs use).
type currentView struct {
	root    *tview.Flex
	table   *tview.Table
	paths   []string          // full project path per data row, indexed by (tableRow - 1)
	aliases map[string]string // runner full name → short display label
}

func newCurrentView(aliases map[string]string) *currentView {
	t := newTable("Current · active pipelines")
	// Selectable rows give us keyboard scrolling for a list that can outgrow the
	// pane; the header stays fixed (SetFixed in newTable) so it never scrolls off.
	t.SetSelectable(true, false)
	t.SetSelectedStyle(tcell.StyleDefault.Background(tcell.ColorDarkSlateGray).Foreground(tcell.ColorWhite))

	root := tview.NewFlex().SetDirection(tview.FlexRow)
	root.AddItem(t, 0, 1, true)
	return &currentView{root: root, table: t, aliases: aliases}
}

// displayRunner maps a runner's full name to its configured short label, or
// returns the name unchanged when no alias is set.
func (v *currentView) displayRunner(runner string) string {
	if short, ok := v.aliases[runner]; ok {
		return short
	}
	return runner
}

// curRow is one flattened tree row queued for rendering.
type curRow struct {
	depth    int
	pipeline bool // true for a pipeline row, false for a job row
	child    bool // pipeline row that is a downstream child (not a root)
	label    string
	status   gitlab.Status
	runner   string // job's runner (empty for pipeline rows / unassigned jobs)
	when     string
	progress string
	path     string // full project path, for the footer reveal
}

func (v *currentView) update(s gitlab.Snapshot) {
	t := v.table
	t.Clear()
	setCurrentHeader(t)
	v.paths = v.paths[:0]

	rows := flattenActive(s.Current)
	if len(rows) == 0 {
		none := tview.NewTableCell("(nothing running)")
		none.SetTextColor(tcell.ColorSilver)
		none.SetSelectable(false)
		none.SetExpansion(1)
		t.SetCell(1, 0, none)
		for i := 1; i < currentCols; i++ {
			t.SetCell(1, i, blankCell())
		}
		return
	}

	for i, r := range rows {
		row := i + 1
		label := strings.Repeat("  ", r.depth) + r.label
		name := tview.NewTableCell(label)
		name.SetExpansion(1)
		switch {
		case r.pipeline:
			name.SetAttributes(tcell.AttrBold)
		default:
			name.SetTextColor(tcell.ColorSilver)
		}
		t.SetCell(row, 0, name)
		t.SetCell(row, 1, curStatusCell(r.status))
		t.SetCell(row, 2, curTextCell(format.Trunc(v.displayRunner(r.runner), 24)))
		t.SetCell(row, 3, curNumCell(r.when))
		t.SetCell(row, 4, curNumCell(r.progress))
		v.paths = append(v.paths, r.path)
	}

	// Keep the selection on a valid data row after a redraw shortens the list.
	if sr, _ := t.GetSelection(); sr > len(rows) {
		t.Select(len(rows), 0)
	} else if sr < 1 {
		t.Select(1, 0)
	}
}

// pathAtRow returns the full project path for a table row (1-based, header is 0).
func (v *currentView) pathAtRow(row int) (string, bool) {
	idx := row - 1
	if idx < 0 || idx >= len(v.paths) {
		return "", false
	}
	if v.paths[idx] == "" {
		return "", false
	}
	return v.paths[idx], true
}

// flattenActive walks the active-pipeline tree depth-first into display rows:
// a pipeline row followed by its jobs, then each child pipeline's subtree
// indented one level deeper.
func flattenActive(aps []gitlab.ActivePipeline) []curRow {
	var rows []curRow
	var walk func(ap gitlab.ActivePipeline, depth int, isChild bool)
	walk = func(ap gitlab.ActivePipeline, depth int, isChild bool) {
		done, total := ap.Progress()
		progress := "—"
		if total > 0 {
			progress = fmt.Sprintf("%d/%d", done, total)
		}
		label := format.Base(ap.ProjectPath) + " · " + ap.Ref
		if isChild {
			label = "↳ " + label
		}
		rows = append(rows, curRow{
			depth:    depth,
			pipeline: true,
			child:    isChild,
			label:    label,
			status:   ap.Status,
			when:     pipeWhen(ap.Pipeline),
			progress: progress,
			path:     ap.ProjectPath,
		})
		for _, j := range ap.Jobs {
			rows = append(rows, curRow{
				depth:  depth + 1,
				label:  j.Stage + " · " + j.Name,
				status: j.Status,
				runner: j.Runner,
				when:   jobWhen(j),
				path:   ap.ProjectPath,
			})
		}
		for _, c := range ap.Children {
			walk(c, depth+1, true)
		}
	}
	for _, ap := range aps {
		walk(ap, 0, false)
	}
	return rows
}

// pipeWhen shows elapsed-since-start while a pipeline runs, or its total duration
// once finished (a finished child under a still-running root).
func pipeWhen(p gitlab.Pipeline) string {
	if p.Status.IsFinished() {
		return format.Duration(p.Duration)
	}
	return format.Elapsed(p.Started, p.Created)
}

// jobWhen mirrors pipeWhen for a job: elapsed while active, total once finished.
func jobWhen(j gitlab.Job) string {
	if j.Status.IsFinished() {
		return format.Duration(j.Duration)
	}
	return format.Elapsed(j.Started, j.Created)
}

// currentCols is the number of columns in the Current table.
const currentCols = 5

// setCurrentHeader writes the Current tab's header, expanding only the name
// column so the status/runner/time/done columns size to their content.
func setCurrentHeader(t *tview.Table) {
	cols := []struct {
		name   string
		expand int
		align  int
	}{
		{"PIPELINE / JOB", 1, tview.AlignLeft},
		{"STATUS", 0, tview.AlignLeft},
		{"RUNNER", 0, tview.AlignLeft},
		{"TIME", 0, tview.AlignRight},
		{"DONE", 0, tview.AlignRight},
	}
	for c, col := range cols {
		cell := tview.NewTableCell(col.name)
		cell.SetTextColor(tcell.ColorAqua)
		cell.SetAttributes(tcell.AttrBold)
		cell.SetSelectable(false)
		cell.SetExpansion(col.expand)
		cell.SetAlign(col.align)
		t.SetCell(0, c, cell)
	}
}

// curStatusCell is a status-colored cell that, unlike statusCell, doesn't expand
// — so the name column takes all the horizontal slack and the status/time/done
// columns pack together on the right.
func curStatusCell(s gitlab.Status) *tview.TableCell {
	c := tview.NewTableCell(string(s))
	c.SetTextColor(statusColor(s))
	return c
}

// curNumCell is a right-aligned, content-sized cell (it doesn't expand, so the
// name column keeps the slack for its indentation).
func curNumCell(text string) *tview.TableCell {
	c := tview.NewTableCell(text)
	c.SetAlign(tview.AlignRight)
	return c
}

// curTextCell is a left-aligned, content-sized cell, muted so it reads as
// secondary detail next to the status and timing columns.
func curTextCell(text string) *tview.TableCell {
	c := tview.NewTableCell(text)
	c.SetTextColor(tcell.ColorSilver)
	return c
}

func blankCell() *tview.TableCell {
	c := tview.NewTableCell("")
	c.SetSelectable(false)
	return c
}
