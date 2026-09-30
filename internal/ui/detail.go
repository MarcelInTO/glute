package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

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
	root   *insetView  // full-screen overlay: frame, inset so the tab shows around it
	frame  *tview.Flex // the bordered, titled box
	table  *tview.Table
	info   *tview.TextView // the selected row's full path and attribution
	legend *tview.TextView

	aliases map[string]string // runner full name → short display label
	rows    []curRow          // data rows, indexed by (tableRow - 1)
	spans   []span            // each data row's timeline geometry, parallel to rows
	total   time.Duration     // the timeline's extent: origin → last finish
	gaps    []idleGap         // idle stretches the timeline cuts out (see idleGaps)
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

// detailBackground is the modal's own background: a charcoal a step up from a
// dark terminal's black, so the panel stands apart from the tab behind it.
// It's kept that dark so everything drawn on it stays legible — the palette
// green of "success" is the weakest, and is still clear on this. As a 256-
// colour value it degrades to plain black on a 16-colour terminal, i.e. to
// how the panel looked before, rather than to something garish. The selection
// highlight (DarkSlateGray) still stands out against it.
var detailBackground = tcell.NewRGBColor(0x26, 0x26, 0x26)

// detailLegend explains the timeline's glyphs and, last, how to close the
// view: the footer says so too, but a hint outside the panel is easy to miss.
const detailLegend = "[silver]░[-] queued  [white]█[-] ran  [white]━[-] pipeline    [aqua]Esc[-]/[aqua]q[-] close "

// detailInset is how far the modal is inset from each screen edge: about a
// twelfth of the width and an eighth of the height, so enough of the tab shows
// all round for the modal to read as a panel over it rather than as a new
// screen. The floors (4 columns, 3 rows) keep that true on a modest terminal;
// on a small one the panel's content wins, and the inset gives way until the
// panel is minDetailWidth × minDetailHeight or the margins are down to one.
func detailInset(width, height int) (mx, my int) {
	const minDetailWidth, minDetailHeight = 72, 12
	mx, my = max(width/12, 4), max(height/8, 3)
	if width-2*mx < minDetailWidth {
		mx = max((width-minDetailWidth)/2, 1)
	}
	if height-2*my < minDetailHeight {
		my = max((height-minDetailHeight)/2, 1)
	}
	return mx, my
}

// insetView lays its child out inside its own rect, detailInset in from each
// edge, and draws nothing else: it doesn't clear its margins, so the page
// beneath stays visible around the child. (A Flex with spacer items would do
// the clearing part, but can't express a margin that scales with a floor.)
// Focus, keys and the mouse all go straight to the child.
type insetView struct {
	*tview.Box
	child tview.Primitive
}

func newInsetView(child tview.Primitive) *insetView {
	return &insetView{Box: tview.NewBox(), child: child}
}

func (v *insetView) Draw(screen tcell.Screen) {
	x, y, w, h := v.GetRect()
	mx, my := detailInset(w, h)
	v.child.SetRect(x+mx, y+my, max(w-2*mx, 0), max(h-2*my, 0))
	v.child.Draw(screen)
}

func (v *insetView) Focus(delegate func(p tview.Primitive)) { delegate(v.child) }

func (v *insetView) HasFocus() bool { return v.child.HasFocus() }

func (v *insetView) InputHandler() func(*tcell.EventKey, func(tview.Primitive)) {
	return v.child.InputHandler()
}

func (v *insetView) MouseHandler() func(tview.MouseAction, *tcell.EventMouse, func(tview.Primitive)) (bool, tview.Primitive) {
	return v.child.MouseHandler()
}

func newDetailView(aliases map[string]string) *detailView {
	t := tview.NewTable()
	t.SetFixed(1, 0)
	t.SetSelectable(true, false)
	t.SetSelectedStyle(tcell.StyleDefault.Background(tcell.ColorDarkSlateGray).Foreground(tcell.ColorWhite))

	t.SetBackgroundColor(detailBackground) // cells are transparent, so they sit on this

	info := tview.NewTextView()
	info.SetDynamicColors(true)
	info.SetBackgroundColor(detailBackground) // also the text's own background
	legend := tview.NewTextView()
	legend.SetDynamicColors(true)
	legend.SetBackgroundColor(detailBackground)
	legend.SetTextAlign(tview.AlignRight)
	legend.SetText(detailLegend)
	bottom := tview.NewFlex()
	bottom.AddItem(info, 0, 1, false)
	bottom.AddItem(legend, tview.TaggedStringWidth(detailLegend), 0, false)

	// A Flex paints no background of its own (its children above do); this
	// colours the border and title row to match.
	frame := tview.NewFlex().SetDirection(tview.FlexRow)
	frame.SetBackgroundColor(detailBackground)
	frame.SetBorder(true)
	frame.SetTitleAlign(tview.AlignLeft)
	frame.AddItem(t, 0, 1, true)
	frame.AddItem(bottom, 1, 0, false)

	v := &detailView{root: newInsetView(frame), frame: frame, table: t, info: info, legend: legend, aliases: aliases}
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
	v.gaps = idleGaps(v.spans, v.total)
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
	v.rows, v.spans, v.total, v.gaps = nil, nil, 0, nil
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

	axis := newTimeAxis(v.total, v.gaps, bar)
	if cell := t.GetCell(0, detColTimeline); cell != nil {
		cell.SetText(timelineHeader(axis))
	}
	for i, s := range v.spans {
		if cell := t.GetCell(i+1, detColTimeline); cell != nil {
			cell.SetText(timelineBar(s, axis))
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

// minIdleGap is the shortest idle stretch the timeline will cut out; below it
// a cut saves too little room to be worth breaking the axis for.
const minIdleGap = time.Minute

// idleGap is a stretch of the timeline, as offsets from the origin, in which
// no job was queued or running.
type idleGap struct{ from, to time.Duration }

// idleGaps finds the stretches of the timeline worth cutting out: those in
// which no job was queued or running, that last at least minIdleGap, and that
// are longer than all the busy time put together. The last test is what keeps
// a pipeline's shape honest: a five-minute wait for a release in a twelve-
// minute run is part of where the time went, and stays drawn to scale, while
// a job retried three days after the rest finished would otherwise stretch the
// axis so far that every real job shrinks into a column or two. Pipelines'
// own spans don't count as busy — they cover their gaps by definition.
func idleGaps(spans []span, total time.Duration) []idleGap {
	var busy []idleGap
	for _, s := range spans {
		if s.ran && !s.pipeline {
			busy = append(busy, idleGap{s.wait, s.end})
		}
	}
	if len(busy) == 0 || total <= 0 {
		return nil
	}
	sort.Slice(busy, func(i, j int) bool { return busy[i].from < busy[j].from })

	// Walk the merged busy intervals, collecting the uncovered stretches
	// between them (and before the first and after the last).
	var idle []idleGap
	var covered, at time.Duration
	cur := busy[0]
	flush := func(b idleGap) {
		if b.from > at {
			idle = append(idle, idleGap{at, b.from})
		}
		covered += b.to - b.from
		at = b.to
	}
	for _, b := range busy[1:] {
		if b.from <= cur.to {
			cur.to = max(cur.to, b.to)
			continue
		}
		flush(cur)
		cur = b
	}
	flush(cur)
	if total > at {
		idle = append(idle, idleGap{at, total})
	}

	var cut []idleGap
	for _, g := range idle {
		if d := g.to - g.from; d >= minIdleGap && d > covered {
			cut = append(cut, g)
		}
	}
	return cut
}

// timeAxis maps timeline offsets to bar cells. Without gaps it's linear over
// the whole width; with them, each cut gap takes a single cell (drawn ┆ down
// every row, an axis break) and the busy stretches between share the rest of
// the width in proportion to their length.
type timeAxis struct {
	total time.Duration
	width int
	idle  time.Duration // the idle time the cuts removed, for the header
	segs  []axisSeg     // busy and gap stretches covering [0, total], in order
}

type axisSeg struct {
	from, to    time.Duration
	gap         bool
	cell, cells int // the first cell and how many the stretch gets
}

func newTimeAxis(total time.Duration, gaps []idleGap, width int) timeAxis {
	a := timeAxis{total: total, width: width}
	if total <= 0 || width <= 0 {
		return a
	}
	// Cutting only pays if the busy stretches still get most of the width.
	var idle time.Duration
	for _, g := range gaps {
		idle += g.to - g.from
	}
	if len(gaps) == 0 || width-len(gaps) < 2*len(gaps)+1 || idle >= total {
		a.segs = []axisSeg{{from: 0, to: total, cells: width}}
		return a
	}
	var at time.Duration
	for _, g := range gaps {
		if g.from > at {
			a.segs = append(a.segs, axisSeg{from: at, to: g.from})
		}
		a.segs = append(a.segs, axisSeg{from: g.from, to: g.to, gap: true, cells: 1})
		a.idle += g.to - g.from
		at = g.to
	}
	if at < total {
		a.segs = append(a.segs, axisSeg{from: at, to: total})
	}

	// Every busy stretch gets a cell of its own first — a few seconds' retry
	// after the last cut is a sliver of the busy time, and would otherwise get
	// none and vanish under the ┆ — then the rest is shared by length, largest
	// remainder first, so the cells add up to exactly the width left over.
	busySegs := 0
	for _, sg := range a.segs {
		if !sg.gap {
			busySegs++
		}
	}
	busyCells, busyTime := int64(width-len(gaps)-busySegs), int64(total-a.idle)
	given := int64(0)
	type rem struct{ seg, r int64 }
	var rems []rem
	for i := range a.segs {
		if sg := &a.segs[i]; !sg.gap {
			n := int64(sg.to-sg.from) * busyCells
			sg.cells = 1 + int(n/busyTime)
			given += int64(sg.cells - 1)
			rems = append(rems, rem{int64(i), n % busyTime})
		}
	}
	sort.Slice(rems, func(i, j int) bool { return rems[i].r > rems[j].r })
	for k := 0; given < busyCells && k < len(rems); k++ {
		a.segs[rems[k].seg].cells++
		given++
	}
	cell := 0
	for i := range a.segs {
		a.segs[i].cell = cell
		cell += a.segs[i].cells
	}
	return a
}

// floor is the cell that time t falls in (where something starting at t is
// drawn from).
func (a timeAxis) floor(t time.Duration) int {
	for i, sg := range a.segs {
		if t >= sg.to && i < len(a.segs)-1 {
			continue
		}
		if sg.gap || sg.to <= sg.from {
			return sg.cell
		}
		off := int(int64(max(t-sg.from, 0)) * int64(sg.cells) / int64(sg.to-sg.from))
		return sg.cell + min(off, sg.cells)
	}
	return 0
}

// ceil is the cell boundary at or after time t (where something ending at t
// is drawn to).
func (a timeAxis) ceil(t time.Duration) int {
	for i, sg := range a.segs {
		if t > sg.to && i < len(a.segs)-1 {
			continue
		}
		if sg.gap {
			return sg.cell + 1
		}
		if sg.to <= sg.from {
			return sg.cell
		}
		span, n := int64(sg.to-sg.from), int64(max(t-sg.from, 0))*int64(sg.cells)
		return sg.cell + min(int((n+span-1)/span), sg.cells)
	}
	return 0
}

// timelineBar draws a span on the axis: blank up to the wait, a light run of
// waiting, then a solid run of working in the row's status color. A job gets ░
// then █; a pipeline, which spans its jobs, gets the thinner ─ then ━ so it
// reads as a bracket over them. Every row that ran gets at least one cell,
// however short, so no job vanishes. A cut idle gap is a ┆ on every row, drawn
// over anything passing through it, so the break reads as one line down the
// table.
func timelineBar(s span, a timeAxis) string {
	if a.total <= 0 || a.width <= 0 {
		return ""
	}
	const (
		blank = iota
		wait
		run
		cut
	)
	kinds := make([]int, a.width)
	if s.ran {
		runFrom := min(a.floor(s.run), a.width-1)
		runTo := min(max(a.ceil(s.end), runFrom+1), a.width)
		waitFrom := min(a.floor(s.wait), runFrom)
		for i := waitFrom; i < runFrom; i++ {
			kinds[i] = wait
		}
		for i := runFrom; i < runTo; i++ {
			kinds[i] = run
		}
	}
	for _, sg := range a.segs {
		if sg.gap && sg.cell < a.width {
			kinds[sg.cell] = cut
		}
	}
	n := a.width
	for n > 0 && kinds[n-1] == blank {
		n--
	}

	waitGlyph, runGlyph := "░", "█"
	if s.pipeline {
		waitGlyph, runGlyph = "─", "━"
	}
	var b strings.Builder
	prev, tagged := blank, false
	for _, k := range kinds[:n] {
		if k != prev && k != blank {
			c := tcell.ColorSilver
			if k == run {
				c = statusColor(s.status)
			}
			fmt.Fprintf(&b, "[#%06x]", c.Hex())
			tagged = true
		}
		prev = k
		switch k {
		case blank:
			b.WriteByte(' ')
		case wait:
			b.WriteString(waitGlyph)
		case run:
			b.WriteString(runGlyph)
		case cut:
			b.WriteString("┆")
		}
	}
	if tagged {
		b.WriteString("[-]")
	}
	return b.String()
}

// timelineHeader labels the timeline column and marks its right edge with the
// wall-clock total it spans, so a bar's length can be read as time. When idle
// gaps were cut it says how much, since the ┆ breaks alone don't.
func timelineHeader(a timeAxis) string {
	const label = "TIMELINE"
	if a.total <= 0 {
		return label
	}
	end := strings.TrimSpace(format.HMS(a.total))
	fits := func(left string) (string, bool) {
		gap := a.width - utf8.RuneCountInString(left) - len(end)
		if gap < 1 {
			return "", false
		}
		return left + strings.Repeat(" ", gap) + end, true
	}
	if a.idle > 0 {
		if h, ok := fits(label + "  ┆ cuts " + strings.TrimSpace(format.HMS(a.idle)) + " idle"); ok {
			return h
		}
	}
	if h, ok := fits(label); ok {
		return h
	}
	return label
}

// contains reports whether screen position (x, y) falls on the modal's frame.
func (v *detailView) contains(x, y int) bool {
	return v.frame.InRect(x, y)
}
