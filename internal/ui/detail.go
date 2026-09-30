package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/MarcelInTO/glute/internal/format"
	"github.com/MarcelInTO/glute/internal/gitlab"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// detailView is the modal opened over a recently finished pipeline from the
// Current tab's bottom panel. It exists for two questions a finished run gets
// asked — which jobs failed, and where the time went — so it lays the whole tree
// out the way the active tree above does (pipeline, its jobs in execution order,
// downstream children nested beneath), and swaps the live-monitoring columns for
// timing ones: QUEUED (how long a job waited for a runner), START (how far into
// the run it began), TIME, and a timeline bar per row. In a DAG pipeline the
// durations alone don't add up to the wall-clock time; the bars show which
// jobs overlapped and which chain the run actually waited on.
//
// The tree is fetched on demand (Service.PipelineTree), so the view has a
// loading and an error state as well as the tree itself.
type detailView struct {
	root   *tview.Flex // full-screen overlay: frame, inset so the tab shows around it
	frame  *tview.Flex // the bordered, titled box
	table  *tview.Table
	info   *tview.TextView // the selected row's full path and attribution
	legend *tview.TextView

	aliases map[string]string // runner full name → short display label
	rows    []curRow          // data rows, indexed by (tableRow - 1)
	spans   []span            // each data row's timeline geometry, parallel to rows
	total   time.Duration     // the timeline's extent: origin → last finish
}

// Detail table column indices; detailCols is the count.
const (
	detColID = iota
	detColName
	detColStatus
	detColQueued
	detColStart
	detColTime
	detColTimeline
	detailCols
)

// minTimelineWidth is the bar width the timeline keeps before the name column
// gets the rest: below this a bar can't tell a 10% job from a 20% one. When the
// pane is too narrow for both at full size, the name is elided down to leave
// the timeline this much — or half the room, if even that is more.
const minTimelineWidth = 30

// The modal's inset from the screen edge. Two rows keep glute's header and
// footer lines visible above and below it, with the tab's own border between;
// one column shows just the tab's side border, so it reads as a panel over the
// tab. A wider side margin would show a sliver of the tab's first column
// through the gap, which reads as clutter, not as depth.
const (
	detailMarginRows = 2
	detailMarginCols = 1
)

func newDetailView(aliases map[string]string) *detailView {
	t := tview.NewTable()
	t.SetFixed(1, 0)
	t.SetSelectable(true, false)
	t.SetSelectedStyle(tcell.StyleDefault.Background(tcell.ColorDarkSlateGray).Foreground(tcell.ColorWhite))

	info := tview.NewTextView()
	info.SetDynamicColors(true)
	legend := tview.NewTextView()
	legend.SetDynamicColors(true)
	legend.SetTextAlign(tview.AlignRight)
	legend.SetText("[silver]░[-] queued  [white]█[-] ran  [white]━[-] pipeline ")
	bottom := tview.NewFlex()
	bottom.AddItem(info, 0, 1, false)
	bottom.AddItem(legend, 34, 0, false)

	frame := tview.NewFlex().SetDirection(tview.FlexRow)
	frame.SetBorder(true)
	frame.SetTitleAlign(tview.AlignLeft)
	frame.AddItem(t, 0, 1, true)
	frame.AddItem(bottom, 1, 0, false)

	middle := tview.NewFlex()
	middle.AddItem(nil, detailMarginCols, 0, false)
	middle.AddItem(frame, 0, 1, true)
	middle.AddItem(nil, detailMarginCols, 0, false)
	root := tview.NewFlex().SetDirection(tview.FlexRow)
	root.AddItem(nil, detailMarginRows, 0, false)
	root.AddItem(middle, 0, 1, true)
	root.AddItem(nil, detailMarginRows, 0, false)

	v := &detailView{root: root, frame: frame, table: t, info: info, legend: legend, aliases: aliases}
	// The timeline is drawn to whatever width the pane has, so it's rendered in
	// the draw hook, like the flexible name column it shares the room with.
	t.SetEvaluateAllRows(true)
	t.SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
		ix, iy, iw, ih := t.GetInnerRect() // safe here; see flexColumns
		v.fit(iw)
		return ix, iy, iw, ih
	})
	t.SetSelectionChangedFunc(func(row, _ int) { v.showInfo(row) })
	return v
}

// displayRunner maps a runner's full name to its configured short label.
func (v *detailView) displayRunner(runner string) string {
	if short, ok := v.aliases[runner]; ok {
		return short
	}
	return runner
}

// loading resets the view for p while its tree is being fetched.
func (v *detailView) loading(p gitlab.Pipeline) {
	v.setTitle(p)
	v.placeholder("(loading…)", tcell.ColorSilver)
	v.info.SetText(" [aqua]" + tview.Escape(p.ProjectPath) + "[-]")
}

// show renders a fetched tree, or the error that stopped the fetch.
func (v *detailView) show(tree gitlab.ActivePipeline, err error) {
	if err != nil {
		v.placeholder("(couldn't load this pipeline)", tcell.ColorRed)
		v.info.SetText(" [red]" + tview.Escape(err.Error()) + "[-]")
		return
	}
	v.setTitle(tree.Pipeline)

	t := v.table
	t.Clear()
	setDetailHeader(t)
	v.rows = flattenActive([]gitlab.ActivePipeline{tree})
	v.spans, v.total = timelineSpans(v.rows)
	for i, r := range v.rows {
		row := i + 1
		name := nameCell(strings.Repeat("  ", r.depth) + r.label)
		if r.pipeline {
			name.SetAttributes(tcell.AttrBold)
		} else {
			name.SetTextColor(tcell.ColorSilver)
		}
		ran := v.spans[i].ran
		queued, start, took := "", "", ""
		if ran {
			start = offsetText(v.spans[i].run)
			took = strings.TrimLeft(format.HMS(r.duration), " ")
			if !r.pipeline {
				queued = offsetText(r.queued)
			}
		}
		t.SetCell(row, detColID, curNumCell(pipelineIDText(r.id)))
		t.SetCell(row, detColName, name)
		t.SetCell(row, detColStatus, curStatusCell(r.status))
		t.SetCell(row, detColQueued, curNumCell(queued))
		t.SetCell(row, detColStart, curNumCell(start))
		t.SetCell(row, detColTime, curNumCell(took))
		t.SetCell(row, detColTimeline, tview.NewTableCell("")) // drawn to width in fit
	}
	t.ScrollToBeginning() // see fillFinishedPipelines: an overflowing list must open at the top
	t.Select(1, 0)
}

// placeholder replaces the table body with a single muted (or error) line
// under the name column, dropping any tree shown before.
func (v *detailView) placeholder(text string, color tcell.Color) {
	t := v.table
	t.Clear()
	setDetailHeader(t)
	v.rows, v.spans, v.total = nil, nil, 0
	cell := tview.NewTableCell(text)
	cell.SetTextColor(color)
	cell.SetSelectable(false)
	t.SetCell(1, detColName, cell)
	t.ScrollToBeginning()
}

// setTitle names the pipeline in the frame: its id (the number the team
// quotes), project, ref and outcome.
func (v *detailView) setTitle(p gitlab.Pipeline) {
	status := ""
	if p.Status != "" {
		status = fmt.Sprintf(" · [#%06x]%s[-]", statusColor(p.Status).Hex(), p.Status)
	}
	v.frame.SetTitle(fmt.Sprintf(" Pipeline #%d · %s · %s%s ",
		p.ID, tview.Escape(format.Base(p.ProjectPath)), tview.Escape(displayRef(p.Ref)), status))
}

// showInfo puts the selected row's detail on the bottom line: the full project
// path (a child pipeline can live in another project), plus who triggered a
// pipeline row or which runner ran a job row — the attribution the timing
// columns displaced.
func (v *detailView) showInfo(row int) {
	idx := row - 1
	if idx < 0 || idx >= len(v.rows) {
		return
	}
	r := v.rows[idx]
	text := " [aqua]" + tview.Escape(r.path) + "[-]"
	switch {
	case r.pipeline && r.user != "":
		text += "  [silver]by[-] " + tview.Escape(r.user)
	case !r.pipeline && r.runner != "":
		text += "  [silver]runner[-] " + tview.Escape(v.displayRunner(r.runner))
	}
	v.info.SetText(text)
}

// fit sizes the timeline and the name column for a table innerWidth wide: the
// numeric columns keep their content widths; the name gets its natural width
// if that still leaves the timeline minTimelineWidth, and is elided otherwise;
// the timeline takes the rest, and its bars are redrawn to that width.
func (v *detailView) fit(innerWidth int) {
	t := v.table
	rows, cols := t.GetRowCount(), t.GetColumnCount()
	if rows == 0 || cols == 0 || innerWidth <= 0 {
		return
	}
	room := innerWidth - (cols - 1) // tview's one-cell column gaps
	for c := range cols {
		if c != detColName && c != detColTimeline {
			room -= columnWidth(t, rows, c)
		}
	}
	bar := room - naturalWidth(t, rows, detColName)
	if bar < minTimelineWidth {
		bar = max(bar, min(minTimelineWidth, room/2))
	}
	bar = max(bar, len("TIMELINE"))

	if cell := t.GetCell(0, detColTimeline); cell != nil {
		cell.SetText(timelineHeader(v.total, bar))
	}
	for i, s := range v.spans {
		if cell := t.GetCell(i+1, detColTimeline); cell != nil {
			cell.SetText(timelineBar(s, v.total, bar))
		}
	}
	fitFlexColumns(t, innerWidth, []int{detColName})
}

// setDetailHeader writes the detail table's header row. Only the name column
// expands; the timeline is sized explicitly in fit, like the name.
func setDetailHeader(t *tview.Table) {
	cols := []struct {
		name   string
		expand int
		align  int
	}{
		{"ID", 0, tview.AlignRight},
		{"PIPELINE / JOB", 1, tview.AlignLeft},
		{"STATUS", 0, tview.AlignLeft},
		{"QUEUED", 0, tview.AlignRight},
		{"START", 0, tview.AlignRight},
		{"TIME", 0, tview.AlignRight},
		{"TIMELINE", 0, tview.AlignLeft},
	}
	for c, col := range cols {
		cell := tview.NewTableCell(col.name)
		if c == detColName {
			cell.SetReference(col.name) // elide with the column; see setHeader
		}
		cell.SetTextColor(tcell.ColorAqua)
		cell.SetAttributes(tcell.AttrBold)
		cell.SetSelectable(false)
		cell.SetExpansion(col.expand)
		cell.SetAlign(col.align)
		t.SetCell(0, c, cell)
	}
}

// offsetText renders a START offset or a QUEUED wait. Unlike format.HMS it
// shows a zero as "0", not an em dash: the first job starting at the origin, or
// a job a runner picked up at once, is a real measurement, not a missing one.
//
// Here, and for TIME, the detail view drops HMS's blank padding. That padding
// holds a column's width still while the Current tree's timers tick; these
// values never change, their cells are right-aligned anyway, and the columns
// it would widen are room the timeline can use.
func offsetText(d time.Duration) string {
	if d < time.Second {
		return "0"
	}
	return strings.TrimLeft(format.HMS(d), " ")
}

// span is one row's place on the timeline, as offsets from the tree's origin
// (the root pipeline's creation). wait..run is time spent waiting — a job queued
// for a runner, a pipeline created but with nothing picked up yet — and
// run..end is time spent working. ran is false for a row that never started (a
// skipped or manual job), which draws no bar.
type span struct {
	wait, run, end time.Duration
	ran            bool
	pipeline       bool
	status         gitlab.Status
}

// timelineSpans places every row on a shared timeline: the origin is the root
// pipeline's creation (so the root's own wait for its first runner shows), and
// total is the latest finish across the tree, which can run past the root's
// own if a child outlived it.
func timelineSpans(rows []curRow) ([]span, time.Duration) {
	if len(rows) == 0 {
		return nil, 0
	}
	origin := rows[0].created
	for _, r := range rows {
		if !r.started.IsZero() && (origin.IsZero() || r.started.Before(origin)) {
			origin = r.started
		}
	}

	spans := make([]span, len(rows))
	var total time.Duration
	for i, r := range rows {
		s := span{pipeline: r.pipeline, status: r.status}
		if !r.started.IsZero() && !origin.IsZero() {
			s.ran = true
			s.run = r.started.Sub(origin)
			s.end = s.run + r.duration
			if !r.finished.IsZero() {
				s.end = r.finished.Sub(origin)
			}
			s.wait = s.run
			switch {
			case r.pipeline && !r.created.IsZero() && r.created.Before(r.started):
				s.wait = r.created.Sub(origin)
			case !r.pipeline:
				s.wait = s.run - r.queued
			}
			s.wait = max(s.wait, 0)
			s.end = max(s.end, s.run)
			total = max(total, s.end)
		}
		spans[i] = s
	}
	return spans, total
}

// timelineBar draws a span width cells wide against a timeline total long:
// blank up to the wait, a light run of waiting, then a solid run of working in
// the row's status color. A job gets ░ then █; a pipeline, which spans its
// jobs, gets the thinner ─ then ━ so it reads as a bracket over them. Every row
// that ran gets at least one cell, however short, so no job vanishes.
func timelineBar(s span, total time.Duration, width int) string {
	if !s.ran || total <= 0 || width <= 0 {
		return ""
	}
	floor := func(d time.Duration) int { return min(int(int64(d)*int64(width)/int64(total)), width) }
	ceil := func(d time.Duration) int {
		return min(int((int64(d)*int64(width)+int64(total)-1)/int64(total)), width)
	}
	runFrom := min(floor(s.run), width-1)
	runTo := max(ceil(s.end), runFrom+1)
	waitFrom := min(floor(s.wait), runFrom)

	waitGlyph, runGlyph := "░", "█"
	if s.pipeline {
		waitGlyph, runGlyph = "─", "━"
	}
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", waitFrom))
	if waitFrom < runFrom {
		fmt.Fprintf(&b, "[#%06x]%s", tcell.ColorSilver.Hex(), strings.Repeat(waitGlyph, runFrom-waitFrom))
	}
	fmt.Fprintf(&b, "[#%06x]%s[-]", statusColor(s.status).Hex(), strings.Repeat(runGlyph, runTo-runFrom))
	return b.String()
}

// timelineHeader labels the timeline column and marks its right edge with the
// total it spans, so a bar's length can be read as time.
func timelineHeader(total time.Duration, width int) string {
	const label = "TIMELINE"
	if total <= 0 {
		return label
	}
	end := strings.TrimSpace(format.HMS(total))
	if gap := width - len(label) - len(end); gap >= 1 {
		return label + strings.Repeat(" ", gap) + end
	}
	return label
}

// contains reports whether screen position (x, y) falls on the modal's frame.
func (v *detailView) contains(x, y int) bool {
	return v.frame.InRect(x, y)
}
