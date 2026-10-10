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

// currentView is the Current tab: the "what's happening right now" monitoring
// view, split top-to-bottom into the active-pipeline tree (roughly the top two
// thirds) and, below it (roughly the bottom third), a "recently finished" panel
// beside a narrower "unresolved failures" one.
//
// The tree table renders each root pipeline, its jobs, and its downstream child
// pipelines, indented to show the hierarchy. Unlike the other tabs' panels it's
// selectable so it scrolls with the arrow keys; the selected row's full project
// path is surfaced in the footer (in place of the mouse-hover other tabs use).
//
// The finished panel keeps a just-completed pipeline visible with its outcome
// after it drops out of the tree above — otherwise a pipeline you were watching
// simply vanishes the moment it finishes and you have to leave the tab to learn
// whether it passed. It lists newest first, so the most recent completions sit
// at the top and older ones clip off the bottom. It reaches back past the recent
// window when that holds few runs (see Snapshot.RecentPipelines), so after a few
// days away it still says where things stood. It's also the way into the
// detail view (detail.go): its rows are selectable once it has focus (the `f`
// key, or a click), and Enter or a click opens the selected pipeline's tree.
//
// The unresolved-failures panel lists every ref (branch, tag, merge request)
// whose newest pipeline outcome is a failure, with how long it has been
// failing: where attention is needed, however long ago it broke. The ref that
// went red most recently comes first, so the list reads in the order of that
// column, and a ref sinks as it stays red. It has less to say than the finished
// list, so it's the narrower of the two. Its rows open the same way (the `u`
// key, or a click), onto the ref's latest failed pipeline.
//
// Only one panel takes the arrow keys at a time, and only that one shows its
// selection highlight — tview highlights a selected row whether or not its
// table has focus, so otherwise all would, and nothing on screen would say
// which one ↑/↓ is about to move.
type currentView struct {
	root     *tview.Flex
	table    *tview.Table
	finished *panelTable
	failing  *panelTable
	paths    []string          // full project path per data row, indexed by (tableRow - 1)
	rows     []curRow          // last-rendered rows, so tick can re-time the live ones
	aliases  map[string]string // runner full name → short display label

	finishedPipes []gitlab.Pipeline   // the finished panel's rows, indexed by (tableRow - 1)
	failingRefs   []gitlab.FailingRef // the failures panel's rows, likewise
	// keyboard says which panel owns the keyboard while the tab is shown. Only
	// the user's intent sets it — the f or u key, or a mouse press on a panel —
	// never a focus change as such: tview moves focus incidentally too (hiding
	// an overlay page re-focuses the page's default item, the tree), and
	// following that would forget, each time the help or the detail view
	// closed, that the user was on a list.
	keyboard curPanel
}

// curPanel names one of the Current tab's panels, for which of them owns the
// keyboard.
type curPanel int

const (
	curTree curPanel = iota
	curFinished
	curFailing
)

// failingWidth is how many of the bottom row's w columns the unresolved-
// failures panel takes: three eighths, but at least what its title needs to
// show its key hints whole, and no more than its three columns can use, so
// that on a wide terminal the rest goes to the finished list's seven. It never
// takes more than half, however narrow the terminal.
func failingWidth(w int) int {
	return min(max(w*3/8, 40), 56, w/2)
}

func newCurrentView(aliases map[string]string) *currentView {
	t := newTable("Current · active pipelines")
	// The name column carries the tree: the project, the ref, the stage and job
	// names, plus a level of indent per depth. It's the flexible one, so it grows
	// to whatever the pane can spare — without it the label's natural width would
	// push STATUS/USER/RUNNER/TIME/DONE off a narrow pane entirely, tview having
	// dropped the columns on the right to keep column 0 whole.
	flexColumns(t, curColName)
	// Selectable rows give us keyboard scrolling for a list that can outgrow the
	// pane; the header stays fixed (SetFixed in newTable) so it never scrolls off.
	t.SetSelectable(true, false)
	t.SetSelectedStyle(tcell.StyleDefault.Background(tcell.ColorDarkSlateGray).Foreground(tcell.ColorWhite))

	// ID leads (see fillFinishedPipelines); PROJECT and REF together identify
	// the run, so those two flex.
	finished := newPanelTable(finishedTitle(false), 1, 2)
	finished.table.SetSelectedStyle(tcell.StyleDefault.Background(tcell.ColorDarkSlateGray).Foreground(tcell.ColorWhite))

	failing := newPanelTable(failingTitle(false), 0, 1) // PROJECT and REF flex
	failing.table.SetSelectedStyle(tcell.StyleDefault.Background(tcell.ColorDarkSlateGray).Foreground(tcell.ColorWhite))

	// The bottom row: the finished list, and the failures beside it at the
	// width failingWidth picks from the row's own width, set as it's drawn,
	// since a Flex can only split by a fixed size or a proportion.
	bottom := tview.NewFlex()
	bottom.AddItem(finished.table, 0, 1, false)
	bottom.AddItem(failing.table, 0, 0, false)
	bottom.SetDrawFunc(func(_ tcell.Screen, x, y, w, h int) (int, int, int, int) {
		bottom.ResizeItem(failing.table, failingWidth(w), 0)
		return x, y, w, h
	})

	// 2:1 split puts the bottom row at about the bottom third; the tree
	// starts with focus so the arrow keys scroll it.
	root := tview.NewFlex().SetDirection(tview.FlexRow)
	root.AddItem(t, 0, 2, true)
	root.AddItem(bottom, 0, 1, false)
	v := &currentView{root: root, table: t, finished: finished, failing: failing, aliases: aliases}

	// Selectability is the highlight switch (see the type comment): a panel is
	// selectable only while it has focus. Its selected row is kept either way,
	// so focus coming back lands where it left.
	t.SetFocusFunc(func() { t.SetSelectable(true, false) })
	t.SetBlurFunc(func() { t.SetSelectable(false, false) })
	for _, l := range []struct {
		p     *panelTable
		title func(focused bool) string
	}{{finished, finishedTitle}, {failing, failingTitle}} {
		l.p.table.SetFocusFunc(func() {
			l.p.setTitle(l.title(true))
			l.p.table.SetSelectable(true, false)
			// Re-select to clamp the view to the selection: while unfocused the
			// panel is pinned to the top on each refresh, which may have left
			// the selected row below the fold.
			row, _ := l.p.table.GetSelection()
			l.p.table.Select(max(row, 1), 0)
		})
		l.p.table.SetBlurFunc(func() {
			l.p.setTitle(l.title(false))
			l.p.table.SetSelectable(false, false)
		})
	}
	return v
}

// finishedTitle is the finished panel's title, ending in the key hint for what
// the panel does from where the user is: how to get into it, or, once in, how
// to open a pipeline and get back. The hint lives here rather than in the
// footer, whose line is already full at common widths, and where it says what
// it applies to.
func finishedTitle(focused bool) string {
	const base = "Recently finished pipelines · newest first"
	if focused {
		return base + " · Enter or click opens · o GitLab · f back"
	}
	return base + " · f to select"
}

// failingTitle is the unresolved-failures panel's title, with its key hint
// like finishedTitle's. The panel is narrow, so the hint is terse: the title
// has to fit the panel's minimum width (failingWidth) whole.
func failingTitle(focused bool) string {
	const base = "Unresolved failures"
	if focused {
		return base + " · Enter · u back"
	}
	return base + " · u to select"
}

// focusTarget is the panel that should hold keyboard focus when the Current
// tab is shown.
func (v *currentView) focusTarget() tview.Primitive {
	switch v.keyboard {
	case curFinished:
		return v.finished.table
	case curFailing:
		return v.failing.table
	}
	return v.table
}

// toggleKeyboard hands the keyboard to list p (the f and u keys), or back to
// the tree if p already has it, and returns the panel that should now have
// focus. A list with nothing in it to select doesn't take the keyboard.
func (v *currentView) toggleKeyboard(p curPanel) tview.Primitive {
	switch {
	case v.keyboard == p:
		v.keyboard = curTree
	case p == curFinished && len(v.finishedPipes) > 0,
		p == curFailing && len(v.failingRefs) > 0:
		v.keyboard = p
	}
	return v.focusTarget()
}

// pressAt records a mouse press at (x, y) as the user choosing a panel: the
// one under the cursor becomes the one that owns the keyboard (tview then gives
// it focus). A press elsewhere changes nothing.
func (v *currentView) pressAt(x, y int) {
	switch {
	case v.finished.table.InRect(x, y):
		v.keyboard = curFinished
	case v.failing.table.InRect(x, y):
		v.keyboard = curFailing
	case v.table.InRect(x, y):
		v.keyboard = curTree
	}
}

// selectedURL is the GitLab page for the selected row of whichever panel has
// the keyboard: a finished pipeline's, a failing ref's latest failure's,
// or a tree row's — a pipeline's page, or on a job row the job's own (its log,
// which is where "why did it fail" goes next). Empty when there's no row or no
// known page.
func (v *currentView) selectedURL() string {
	switch v.keyboard {
	case curFinished:
		row, _ := v.finished.table.GetSelection()
		p, _ := v.finishedAt(row)
		return p.WebURL
	case curFailing:
		row, _ := v.failing.table.GetSelection()
		f, _ := v.failingAt(row)
		return f.Latest.WebURL
	}
	row, _ := v.table.GetSelection()
	if i := row - 1; i >= 0 && i < len(v.rows) {
		return v.rows[i].url
	}
	return ""
}

// selectedPath is the full project path of the selected row of whichever
// panel has the keyboard, for the footer; empty when there's none.
func (v *currentView) selectedPath() string {
	switch v.keyboard {
	case curFinished:
		row, _ := v.finished.table.GetSelection()
		p, _ := v.finishedAt(row)
		return p.ProjectPath
	case curFailing:
		row, _ := v.failing.table.GetSelection()
		f, _ := v.failingAt(row)
		return f.ProjectPath
	}
	row, _ := v.table.GetSelection()
	path, _ := v.pathAtRow(row)
	return path
}

// finishedAt returns the pipeline on a finished-panel table row (1-based;
// the header is row 0).
func (v *currentView) finishedAt(row int) (gitlab.Pipeline, bool) {
	idx := row - 1
	if idx < 0 || idx >= len(v.finishedPipes) {
		return gitlab.Pipeline{}, false
	}
	return v.finishedPipes[idx], true
}

// updateFinished refills the finished panel, keeping the selection on the same
// pipeline rather than the same row: each refresh can push newly finished
// pipelines in on top, and a selection left on the row index would slide onto
// a different pipeline under the user's cursor — the one Enter then opens. The
// view follows the newest-first rule in placeSelection.
func (v *currentView) updateFinished(pipes []gitlab.Pipeline) {
	f := v.finished.table
	prevRow, _ := f.GetSelection()
	prevOffset, _ := f.GetOffset()
	prev, hadPrev := v.finishedAt(prevRow)

	fillFinishedPipelines(v.finished, pipes)
	v.finishedPipes = pipes
	if len(pipes) == 0 {
		return
	}

	row := min(max(prevRow, 1), len(pipes)) // the pipeline aged out: stay near where it was
	if hadPrev {
		for i, p := range pipes {
			if p.ID == prev.ID {
				row = i + 1
				break
			}
		}
	}
	placeSelection(f, v.keyboard == curFinished && hadPrev, prevRow, prevOffset, row)
}

// failingAt returns the failing ref on a failures-panel table row (1-based;
// the header is row 0).
func (v *currentView) failingAt(row int) (gitlab.FailingRef, bool) {
	idx := row - 1
	if idx < 0 || idx >= len(v.failingRefs) {
		return gitlab.FailingRef{}, false
	}
	return v.failingRefs[idx], true
}

// updateFailing refills the failures panel, keeping the selection on the same
// project and ref: a refresh adds newly broken refs on top (a ref that
// recovered and broke again between two refreshes among them) and drops
// recovered ones, so the row index alone would slide onto a different ref. If
// the ref recovered, the selection stays near the row it was on. The list is
// newest first by when each ref went red, so the view follows the same
// newest-first rule as the finished list (placeSelection).
func (v *currentView) updateFailing(failing []gitlab.FailingRef) {
	t := v.failing.table
	prevRow, _ := t.GetSelection()
	prevOffset, _ := t.GetOffset()
	prev, hadPrev := v.failingAt(prevRow)

	fillFailingRefs(v.failing, failing)
	v.failingRefs = failing
	if len(failing) == 0 {
		return
	}
	row := min(max(prevRow, 1), len(failing))
	if hadPrev {
		for i, f := range failing {
			if f.ProjectPath == prev.ProjectPath && f.Ref == prev.Ref {
				row = i + 1
				break
			}
		}
	}
	placeSelection(t, v.keyboard == curFailing && hadPrev, prevRow, prevOffset, row)
}

// placeSelection selects row in a newest-first list just refilled (which has
// pinned its view to the top), row being where the previously selected item,
// once on prevRow with the view at prevOffset, now sits. held says the list
// has the keyboard and something was selected in it.
//
// The view follows the usual rule for a newest-first list. At the top — the
// panel scrolled to its first row, or not the panel with the keyboard, which is
// always kept there — it stays at the top, so each new arrival shows up. Scrolled
// down, it holds still instead (the offset moves with the selected row), so the
// rows being read don't shift under the user; scrolling back to the top resumes
// following. The finished list once held still whenever it had the keyboard,
// even at the top, which hid every arrival just above the view from the first
// `f`, click or opened pipeline on.
//
// At the top, the selection follows its item only as far as the last visible
// row: tview scrolls the view to keep a selection in sight, so following the
// item past the bottom edge would pull the view off the top. There the
// highlight stays on the last visible row instead.
func placeSelection(t *tview.Table, held bool, prevRow, prevOffset, row int) {
	if held && prevOffset > 0 {
		t.SetOffset(max(prevOffset+row-prevRow, 0), 0)
	} else if visible := visibleRows(t); visible > 0 {
		row = min(row, visible)
	}
	t.Select(row, 0)
}

// visibleRows is how many data rows a list panel showed when last drawn (its
// height less the fixed header row), or 0 before its first draw.
func visibleRows(t *tview.Table) int {
	_, _, _, h := t.GetInnerRect()
	return max(h-1, 0)
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
	pipeline bool  // true for a pipeline row, false for a job row
	child    bool  // pipeline row that is a downstream child (not a root)
	id       int64 // the pipeline's id (pipeline rows only; job rows show none)
	label    string
	status   gitlab.Status
	user     string        // triggering user (pipeline rows only)
	runner   string        // job's runner (empty for pipeline rows / unassigned jobs)
	started  time.Time     // for the live TIME column (running rows)
	created  time.Time     // fallback start when Started is unknown
	finished time.Time     // when the row finished (zero while live); the detail timeline's bar end
	duration time.Duration // total duration, for finished rows
	queued   time.Duration // job's wait for a runner (job rows only), for the detail view
	progress string
	path     string // full project path, for the footer reveal
	url      string // the row's GitLab page: the pipeline's, or on a job row the job's own
}

// live reports whether this row's TIME still ticks (i.e. it hasn't finished).
func (r curRow) live() bool { return !r.status.IsFinished() }

// when renders the TIME column: elapsed-since-start while the row is live (so it
// counts up between refreshes), or the fixed total duration once finished. Both
// go through format.HMS so the column lines up.
func (r curRow) when() string {
	if r.live() {
		return format.HMS(format.ElapsedSince(r.started, r.created))
	}
	return format.HMS(r.duration)
}

func (v *currentView) update(s gitlab.Snapshot) {
	v.updateFinished(s.RecentPipelines)
	v.updateFailing(s.FailingRefs)

	t := v.table
	t.Clear()
	setCurrentHeader(t)
	v.paths = v.paths[:0]

	rows := flattenActive(s.Current)
	v.rows = rows
	if len(rows) == 0 {
		none := tview.NewTableCell("(nothing running)")
		none.SetTextColor(tcell.ColorSilver)
		none.SetSelectable(false)
		none.SetExpansion(1)
		t.SetCell(1, curColName, none)
		for i := 0; i < currentCols; i++ {
			if i != curColName {
				t.SetCell(1, i, blankCell())
			}
		}
		return
	}

	for i, r := range rows {
		row := i + 1
		label := strings.Repeat("  ", r.depth) + r.label
		name := nameCell(label)
		switch {
		case r.pipeline:
			name.SetAttributes(tcell.AttrBold)
		default:
			name.SetTextColor(tcell.ColorSilver)
		}
		t.SetCell(row, curColID, linkCell(pipelineIDText(r.id), r.url))
		t.SetCell(row, curColName, name)
		t.SetCell(row, curColStatus, curStatusCell(r.status))
		t.SetCell(row, curColUser, curTextCell(format.Trunc(r.user, 16)))
		t.SetCell(row, curColRunner, curTextCell(format.Trunc(v.displayRunner(r.runner), 24)))
		t.SetCell(row, curColTime, curNumCell(r.when()))
		t.SetCell(row, curColDone, curNumCell(r.progress))
		v.paths = append(v.paths, r.path)
	}

	// Keep the selection on a valid data row after a redraw shortens the list.
	if sr, _ := t.GetSelection(); sr > len(rows) {
		t.Select(len(rows), 0)
	} else if sr < 1 {
		t.Select(1, 0)
	}
}

// tick re-times only the live rows' TIME cells, so the elapsed counters count
// up smoothly between refreshes. It touches nothing else — the selection, the
// scroll offset, and every other cell stay put — so it's safe to call every
// second even while the user is scrolling.
func (v *currentView) tick() {
	for i, r := range v.rows {
		if !r.live() {
			continue // finished rows have a fixed duration; leave them alone
		}
		if cell := v.table.GetCell(i+1, curColTime); cell != nil {
			cell.SetText(r.when())
		}
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
		label := format.Base(ap.ProjectPath) + " · " + displayRef(ap.Ref)
		if isChild {
			label = "↳ " + label
		}
		rows = append(rows, curRow{
			depth:    depth,
			pipeline: true,
			child:    isChild,
			id:       ap.ID,
			label:    label,
			status:   ap.Status,
			user:     ap.User,
			started:  ap.Started,
			created:  ap.Created,
			finished: ap.Finished,
			duration: ap.Duration,
			progress: progress,
			path:     ap.ProjectPath,
			url:      ap.WebURL,
		})
		for _, j := range ap.Jobs {
			rows = append(rows, curRow{
				depth:    depth + 1,
				label:    j.Stage + " · " + j.Name,
				status:   j.Status,
				runner:   j.Runner,
				started:  j.Started,
				created:  j.Created,
				finished: j.Finished,
				duration: j.Duration,
				queued:   j.Queued,
				path:     ap.ProjectPath,
				url:      j.WebURL,
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

// fillFinishedPipelines renders the Current tab's bottom panel: the most
// recently finished pipelines, newest first. s.RecentPipelines is already
// finished-only and sorted newest-finished-first, so the rows the user most
// likely just watched sit at the top; older ones beyond the pane's height clip
// off the bottom. ID leads for the same reason as in the tree above: the
// pipeline's id is the number the team quotes as a build number. USER mirrors
// the tree too (who triggered it); STATUS/DURATION/WHEN answer "did it pass,
// and when". This is deliberately close to the Pipelines tab's recent panel but
// adds USER, so it's kept separate rather than shared.
func fillFinishedPipelines(p *panelTable, pipes []gitlab.Pipeline) {
	const colID, colDuration, colWhen = 0, 5, 6
	p.reset("ID", "PROJECT", "REF", "STATUS", "USER", "DURATION", "WHEN")
	// A header reads best justified the same way as its column's data, so right-
	// align the numeric headers to match their right-aligned cells (the tree
	// above does the same for ID/TIME/DONE via setCurrentHeader).
	for _, c := range []int{colID, colDuration, colWhen} {
		if cell := p.table.GetCell(0, c); cell != nil {
			cell.SetAlign(tview.AlignRight)
		}
	}
	// The ID column is content-sized, like the tree's: on a wide pane every
	// other column here shares the slack (they all expand), and an expanding
	// first column would park the ids behind a gutter of blank space.
	p.table.GetCell(0, colID).SetExpansion(0)
	// Pin the view to the top so the newest rows always show. tview's Table sets
	// a sticky "trackEnd" the first time its content fits the pane — which it does
	// on the empty first render, before the initial refresh lands — and then keeps
	// the view parked at the bottom once the list overflows, hiding exactly the
	// most-recent rows this panel exists to show. ScrollToBeginning clears it.
	p.table.ScrollToBeginning()
	if len(pipes) == 0 {
		emptyRowAt(p.table, 7, 1) // "(none)" under PROJECT, not the ID column
		return
	}
	for i, pipe := range pipes {
		r := i + 1
		p.addPath(pipe.ProjectPath)
		p.table.SetCell(r, colID, linkCell(pipelineIDText(pipe.ID), pipe.WebURL))
		p.table.SetCell(r, 1, nameCell(format.Base(pipe.ProjectPath)))
		p.table.SetCell(r, 2, nameCell(displayRef(pipe.Ref)))
		p.table.SetCell(r, 3, statusCell(pipe.Status))
		p.table.SetCell(r, 4, textCell(format.Trunc(pipe.User, 16)))
		// HMS matches the Current tree's TIME column above for a consistent look.
		p.table.SetCell(r, colDuration, numCell(format.HMS(pipe.Duration)))
		p.table.SetCell(r, colWhen, numCell(format.Ago(pipe.Finished)))
	}
}

// fillFailingRefs renders the unresolved-failures panel: each ref whose newest
// pipeline outcome is a failure, and for how long it has been failing, in the
// order of that column: the ref that went red most recently first, however
// recently the others failed again. The duration is in the failed status's
// red, which is what the panel is about. PROJECT and REF flex; there's no ID
// column, since the row opens the latest failure, whose id the detail view
// shows.
func fillFailingRefs(p *panelTable, failing []gitlab.FailingRef) {
	const colFailing = 2
	p.reset("PROJECT", "REF", "FAILING")
	rightAlignHeaders(p.table, colFailing)
	p.table.ScrollToBeginning() // see fillFinishedPipelines
	if len(failing) == 0 {
		emptyRow(p.table, 3)
		return
	}
	for i, f := range failing {
		r := i + 1
		p.addPath(f.ProjectPath)
		p.table.SetCell(r, 0, nameCell(format.Base(f.ProjectPath)))
		p.table.SetCell(r, 1, nameCell(displayRef(f.Ref)))
		cell := numCell(format.Ago(f.Since))
		cell.SetTextColor(statusColor(gitlab.StatusFailed))
		p.table.SetCell(r, colFailing, cell)
	}
}

// Current table column indices. ID leads: the pipeline's instance-wide id
// (Pipeline.ID — the "#N" GitLab shows and the number in its URL), which the
// team quotes as a build number. It's filled on pipeline rows, roots and
// downstream children alike, and deliberately blank on job rows: nobody quotes
// job ids, and a column of them would only be noise. Sitting before the
// indented name column, the ids line up regardless of tree depth. USER (who
// triggered the pipeline) is filled on pipeline rows; RUNNER (where a job ran)
// is filled on job rows — they never coincide, so they sit adjacent as the
// row's attribution columns. currentCols is the count (the iota block leaves it
// as the last value).
const (
	curColID = iota
	curColName
	curColStatus
	curColUser
	curColRunner
	curColTime
	curColDone
	currentCols
)

// setCurrentHeader writes the Current tab's header, expanding only the name
// column so the id/status/user/runner/time/done columns size to their content.
func setCurrentHeader(t *tview.Table) {
	cols := []struct {
		name   string
		expand int
		align  int
	}{
		{"ID", 0, tview.AlignRight},
		{"PIPELINE / JOB", 1, tview.AlignLeft},
		{"STATUS", 0, tview.AlignLeft},
		{"USER", 0, tview.AlignLeft},
		{"RUNNER", 0, tview.AlignLeft},
		{"TIME", 0, tview.AlignRight},
		{"DONE", 0, tview.AlignRight},
	}
	for c, col := range cols {
		cell := tview.NewTableCell(col.name)
		cell.SetReference(col.name) // elide with the column; see setHeader
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
