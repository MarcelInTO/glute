package ui

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MarcelInTO/glute/internal/gitlab"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

var colorTag = regexp.MustCompile(`\[[^\]]*\]`)

// plainBar is a timeline bar with its color tags stripped: the glyph layout.
func plainBar(s span, a timeAxis) string {
	return colorTag.ReplaceAllString(timelineBar(s, a), "")
}

func TestTimelineBar(t *testing.T) {
	const total = 100 * time.Second
	sec := func(n int) time.Duration { return time.Duration(n) * time.Second }
	cases := []struct {
		name string
		s    span
		want string
	}{
		{"ran from the origin", span{ran: true, run: 0, end: sec(50)}, "█████"},
		{"queued, then ran", span{ran: true, wait: sec(10), run: sec(20), end: sec(50)}, " ░███"},
		{"ran to the very end", span{ran: true, wait: sec(90), run: sec(90), end: total}, "         █"},
		{"too short to see still gets a cell", span{ran: true, wait: sec(40), run: sec(40), end: sec(41)}, "    █"},
		{"a pipeline brackets its jobs", span{ran: true, pipeline: true, wait: 0, run: sec(20), end: total}, "──━━━━━━━━"},
		{"never ran: no bar", span{ran: false}, ""},
	}
	linear := newTimeAxis(total, nil, 10)
	for _, c := range cases {
		if got := plainBar(c.s, linear); got != c.want {
			t.Errorf("%s: bar = %q, want %q", c.name, got, c.want)
		}
	}
	// The run is drawn in the row's status color, the wait in muted silver.
	got := timelineBar(span{ran: true, wait: 0, run: sec(50), end: total, status: gitlab.StatusFailed}, linear)
	if !strings.Contains(got, "[#ff0000]█") || !strings.Contains(got, "░") {
		t.Errorf("failed bar should be red after a silver wait, got %q", got)
	}
	if timelineBar(span{ran: true, end: sec(5)}, newTimeAxis(0, nil, 10)) != "" {
		t.Error("a zero-length timeline should draw nothing")
	}
}

func TestTimelineHeaderMarksTotal(t *testing.T) {
	if got := timelineHeader(newTimeAxis(5*time.Minute+52*time.Second, nil, 20)); got != "TIMELINE        5:52" {
		t.Errorf("header = %q, want the total flush right", got)
	}
	if got := timelineHeader(newTimeAxis(5*time.Minute, nil, 10)); got != "TIMELINE" {
		t.Errorf("header = %q: no room for the total, want the bare label", got)
	}
	if got := timelineHeader(newTimeAxis(0, nil, 30)); got != "TIMELINE" {
		t.Errorf("header = %q: nothing timed, want the bare label", got)
	}
	// With a cut, the header says how much idle time the ┆ removed — or, short
	// of room for that, falls back to label and total.
	cut := newTimeAxis(10*time.Hour, []idleGap{{time.Hour, 9 * time.Hour}}, 40)
	if got := timelineHeader(cut); got != "TIMELINE  ┆ cuts 8:00:00 idle   10:00:00" {
		t.Errorf("header = %q, want the idle cut named", got)
	}
	if got := timelineHeader(newTimeAxis(10*time.Hour, []idleGap{{time.Hour, 9 * time.Hour}}, 20)); got != "TIMELINE    10:00:00" {
		t.Errorf("narrow header = %q, want label and total only", got)
	}
}

// TestIdleGapsCutOnlyDominantIdle checks what counts as a cut: idle stretches
// (no job queued or running) at least a minute long and longer than all the
// busy time. A wait shorter than the work stays drawn to scale; a retry days
// later is cut; pipeline spans, which cover their own gaps, don't count as busy.
func TestIdleGapsCutOnlyDominantIdle(t *testing.T) {
	mins := func(m int) time.Duration { return time.Duration(m) * time.Minute }
	job := func(from, to int) span { return span{ran: true, wait: mins(from), run: mins(from), end: mins(to)} }
	root := span{ran: true, pipeline: true, run: 0, end: mins(12)}

	// sems-platform's shape: 6.5 busy minutes, then a 5.5-minute wait for release.
	shaped := []span{root, job(0, 6), job(1, 5), job(11, 12)}
	if got := idleGaps(shaped, mins(12)); len(got) != 0 {
		t.Errorf("a wait shorter than the work was cut: %+v", got)
	}

	// tlp-sz!438's shape: a minute of work, then a retry four days later.
	late := []span{{ran: true, pipeline: true, end: mins(4*24*60 + 1)}, job(0, 1), job(4*24*60, 4*24*60+1)}
	got := idleGaps(late, mins(4*24*60+1))
	if len(got) != 1 || got[0].from != mins(1) || got[0].to != mins(4*24*60) {
		t.Errorf("gaps = %+v, want the one between the minute of work and the retry", got)
	}

	// Overlapping jobs merge: nothing is idle between them.
	if got := idleGaps([]span{job(0, 10), job(5, 20), job(8, 9)}, mins(20)); len(got) != 0 {
		t.Errorf("overlapping jobs left a gap: %+v", got)
	}
	// Under a minute is never cut, however lopsided.
	sec := []span{{ran: true, end: time.Second}, {ran: true, wait: 50 * time.Second, run: 50 * time.Second, end: 51 * time.Second}}
	if got := idleGaps(sec, 51*time.Second); len(got) != 0 {
		t.Errorf("a sub-minute gap was cut: %+v", got)
	}
}

// TestTimeAxisCutsGaps lays two busy stretches around a cut gap on 21 cells:
// the gap takes one cell drawn ┆ on every row, and the busy stretches share the
// other 20 by length, so a job's bar keeps its place within its stretch.
func TestTimeAxisCutsGaps(t *testing.T) {
	h := time.Hour
	a := newTimeAxis(100*h, []idleGap{{10 * h, 90 * h}}, 21)
	if a.idle != 80*h {
		t.Errorf("idle = %s, want 80h", a.idle)
	}
	cases := []struct {
		name string
		s    span
		want string
	}{
		{"work before the gap", span{ran: true, wait: 0, run: 0, end: 10 * h}, "██████████┆"},
		{"work after it", span{ran: true, wait: 90 * h, run: 90 * h, end: 100 * h}, "          ┆██████████"},
		{"a pipeline across it", span{ran: true, pipeline: true, run: 0, end: 100 * h}, "━━━━━━━━━━┆━━━━━━━━━━"},
		{"half the stretch before it", span{ran: true, wait: 5 * h, run: 5 * h, end: 10 * h}, "     █████┆"},
		{"a row that never ran still carries the break", span{}, "          ┆"},
	}
	for _, c := range cases {
		if got := plainBar(c.s, a); got != c.want {
			t.Errorf("%s: bar = %q, want %q", c.name, got, c.want)
		}
	}
	// A stretch too short for a share of its own still gets a cell: a few
	// seconds' retry after a cut stays visible rather than vanishing under ┆.
	tail := newTimeAxis(100*h, []idleGap{{10 * h, 100*h - 5*time.Second}}, 21)
	if got := plainBar(span{ran: true, wait: 100*h - 5*time.Second, run: 100*h - 5*time.Second, end: 100 * h}, tail); !strings.HasSuffix(got, "┆█") {
		t.Errorf("a 5s job after the cut = %q, want its own cell after the ┆", got)
	}

	// Too narrow for a cut to leave room: the axis stays linear.
	if narrow := newTimeAxis(100*h, []idleGap{{10 * h, 90 * h}}, 2); narrow.idle != 0 {
		t.Errorf("a 2-cell axis cut a gap: %+v", narrow)
	}
}

// TestTimelineSpans checks the shared time axis: the origin is the root's
// creation, a job's wait is its runner queue, a pipeline's wait is created →
// started, a job that never ran has no span, and the total runs to the latest
// finish anywhere — here a child that outlived its root.
func TestTimelineSpans(t *testing.T) {
	t0 := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	rows := []curRow{
		{pipeline: true, created: at(0), started: at(10), finished: at(100), duration: 90 * time.Second},
		{started: at(10), finished: at(40), duration: 30 * time.Second, queued: 4 * time.Second},
		{status: gitlab.StatusSkipped},
		{pipeline: true, child: true, created: at(90), started: at(95), finished: at(130)},
	}
	spans, total := timelineSpans(rows)
	if total != 130*time.Second {
		t.Errorf("total = %s, want 130s (the child finishing after its root)", total)
	}
	want := []span{
		{wait: 0, run: 10 * time.Second, end: 100 * time.Second, ran: true, pipeline: true},
		{wait: 6 * time.Second, run: 10 * time.Second, end: 40 * time.Second, ran: true},
		{status: gitlab.StatusSkipped},
		{wait: 90 * time.Second, run: 95 * time.Second, end: 130 * time.Second, ran: true, pipeline: true},
	}
	for i := range want {
		if spans[i] != want[i] {
			t.Errorf("span %d = %+v, want %+v", i, spans[i], want[i])
		}
	}
}

// newTreeDashboard is the sample dashboard with the sample finished trees
// behind its service, as `glute --sample` runs.
func newTreeDashboard() (*Dashboard, map[int64]gitlab.ActivePipeline) {
	snap := gitlab.SampleSnapshot()
	trees := gitlab.SampleTrees(snap)
	d := NewDashboard(gitlab.FakeService{Snap: snap, Trees: trees}, Options{Title: "sample data"})
	d.snapshot = snap
	d.current.update(snap)
	d.renderHistory()
	d.updateHeader()
	d.updateFooter()
	return d, trees
}

// TestDetailViewShowsFailedTree renders the failed sample pipeline's detail
// view: the failed job and the skipped one behind it are both listed, with the
// timing columns and a bar for every job that ran.
func TestDetailViewShowsFailedTree(t *testing.T) {
	d, trees := newTreeDashboard()
	failed := d.snapshot.RecentPipelines[1] // #97, gateway · feat/rate-limit · failed
	d.openDetail(failed)
	d.detail.show(trees[failed.ID], nil) // what the background fetch delivers
	out := renderToText(t, d, 130, 24)
	t.Logf("detail view:\n%s", out)

	for _, want := range []string{"Pipeline #97", "feat/rate-limit", "QUEUED", "START", "TIMELINE",
		"deploy-staging", "failed", "smoke-test", "skipped", "░", "█", "━", "Esc/q close"} {
		if !strings.Contains(out, want) {
			t.Errorf("detail view missing %q", want)
		}
	}
	// The skipped job never ran: no timing, no bar.
	for row := 1; row < d.detail.table.GetRowCount(); row++ {
		if !strings.Contains(d.detail.table.GetCell(row, detColName).Text, "smoke-test") {
			continue
		}
		for _, c := range []int{detColQueued, detColStart, detColTime, detColTimeline} {
			if got := d.detail.table.GetCell(row, c).Text; got != "" {
				t.Errorf("skipped job's column %d = %q, want blank", c, got)
			}
		}
	}
}

// TestDetailViewNamesWholeAtCommonWidth checks the text columns come before
// the timeline: it takes the room the name and runner don't need, rather than
// a fixed share, so at 120 columns, inset panel and all, the sample's longest
// job name and its runners still show whole.
func TestDetailViewNamesWholeAtCommonWidth(t *testing.T) {
	d, trees := newTreeDashboard()
	p := d.snapshot.RecentPipelines[0] // #98, with a child pipeline
	d.openDetail(p)
	d.detail.show(trees[p.ID], nil)
	out := renderToText(t, d, 120, 30)
	t.Logf("detail view at 120 columns:\n%s", out)
	for _, want := range []string{"integration-tests", "↳ deploy · main", "deploy-staging", "shared-linux-01", "shared-linux-02"} {
		if !strings.Contains(out, want) {
			t.Errorf("at 120 columns, missing %q (elided when it needn't be)", want)
		}
	}
}

// TestDetailViewIsInset checks the modal sits inside the screen with the tab
// visible all round it, so it reads as a panel over the tab: at 120×30 it's
// inset 10 columns and 3 rows, and the finished panel's title shows beside it.
func TestDetailViewIsInset(t *testing.T) {
	d, trees := newTreeDashboard()
	p := d.snapshot.RecentPipelines[0]
	d.openDetail(p)
	d.detail.show(trees[p.ID], nil)
	out := renderToText(t, d, 120, 30)
	if x, y, w, h := d.detail.frame.GetRect(); x != 10 || y != 3 || w != 100 || h != 24 {
		t.Errorf("frame rect = (%d,%d %dx%d), want (10,3 100x24)", x, y, w, h)
	}
	if !strings.Contains(out, "┌ Recently") {
		t.Errorf("the tab beneath should show around the panel:\n%s", out)
	}
}

func TestDetailInset(t *testing.T) {
	cases := []struct{ w, h, mx, my int }{
		{120, 30, 10, 3}, // about a twelfth and an eighth
		{200, 60, 16, 7},
		{80, 24, 4, 3}, // the floors
		{76, 16, 2, 2}, // small: the panel keeps 72×12 by giving up margin
		{40, 10, 1, 1}, // tiny: a one-cell margin is the last to go
	}
	for _, c := range cases {
		if mx, my := detailInset(c.w, c.h); mx != c.mx || my != c.my {
			t.Errorf("detailInset(%d, %d) = (%d, %d), want (%d, %d)", c.w, c.h, mx, my, c.mx, c.my)
		}
	}
}

func TestDetailViewLoadingAndError(t *testing.T) {
	d, _ := newTreeDashboard()
	p := d.snapshot.RecentPipelines[0]
	d.openDetail(p)
	if out := renderToText(t, d, 130, 24); !strings.Contains(out, "(loading…)") || !strings.Contains(out, "Pipeline #98") {
		t.Errorf("before the fetch lands, want a titled loading state:\n%s", out)
	}
	d.detail.show(gitlab.ActivePipeline{}, errors.New("gql http 502: bad gateway"))
	out := renderToText(t, d, 130, 24)
	if !strings.Contains(out, "couldn't load") || !strings.Contains(out, "gql http 502") {
		t.Errorf("a failed fetch should say so, with the error:\n%s", out)
	}
}

func pressSpecial(d *Dashboard, k tcell.Key) {
	d.onKey(tcell.NewEventKey(k, 0, tcell.ModNone))
}

// TestFocusKeyMovesHighlight checks `f` hands the keyboard between the tree and
// the finished list, and that only the focused one shows a selection highlight.
func TestFocusKeyMovesHighlight(t *testing.T) {
	d, _ := newTreeDashboard()
	tree, fin := d.current.table, d.current.finished.table
	selectable := func(tb *tview.Table) bool { rows, _ := tb.GetSelectable(); return rows }

	if !selectable(tree) || selectable(fin) {
		t.Fatalf("at start the tree should be the highlighted panel")
	}
	pressKey(d, 'f')
	if d.app.GetFocus() != fin || !selectable(fin) || selectable(tree) {
		t.Fatalf("after f the finished list should have focus and the only highlight")
	}
	if !strings.Contains(d.footer.GetText(true), "acme/payments/api") {
		t.Errorf("footer should reveal the selected finished row's path, got %q", d.footer.GetText(true))
	}
	pressKey(d, 'f')
	if d.app.GetFocus() != tree || !selectable(tree) || selectable(fin) {
		t.Fatalf("a second f should hand focus back to the tree")
	}
}

// TestEnterOpensAndEscReturns opens the selected finished pipeline with Enter
// and closes it with Esc, landing back on the finished list; q closes the view
// rather than quitting from under it.
func TestEnterOpensAndEscReturns(t *testing.T) {
	d, _ := newTreeDashboard()
	fin := d.current.finished.table
	pressKey(d, 'f')
	fin.InputHandler()(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), func(tview.Primitive) {})
	fin.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(tview.Primitive) {})
	if !d.detailOpen() {
		t.Fatal("Enter on a finished row should open the detail view")
	}
	if out := renderToText(t, d, 130, 24); !strings.Contains(out, "Pipeline #97") {
		t.Errorf("Enter should open the selected (second) pipeline, #97:\n%s", out)
	}
	pressKey(d, '2') // tab keys are inert under the view
	if d.active != 0 {
		t.Errorf("a tab key switched tabs under the detail view")
	}
	pressSpecial(d, tcell.KeyEscape)
	if d.detailOpen() || d.app.GetFocus() != fin {
		t.Fatalf("Esc should close the view and return focus to the finished list")
	}
	fin.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(tview.Primitive) {})
	pressKey(d, 'q')
	if d.detailOpen() {
		t.Error("q should close the detail view")
	}
}

// TestClickOpensFinishedRow clicks the third finished row: it opens that
// pipeline, and a click outside the view dismisses it.
func TestClickOpensFinishedRow(t *testing.T) {
	d, _ := newTreeDashboard()
	_ = renderToText(t, d, 130, 32) // lay out, so rows have screen positions
	fin := d.current.finished.table
	x, y, _, _ := fin.GetInnerRect()
	click := func(x, y int) {
		d.onMouse(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone), tview.MouseLeftClick)
	}

	click(x+4, y+3) // header is y, first row y+1: y+3 is the third, #96
	if !d.detailOpen() {
		t.Fatal("clicking a finished row should open the detail view")
	}
	if out := renderToText(t, d, 130, 32); !strings.Contains(out, "Pipeline #96") {
		t.Errorf("click should open the clicked pipeline, #96:\n%s", out)
	}
	if row, _ := fin.GetSelection(); row != 3 || d.app.GetFocus() != d.detail.table {
		t.Errorf("click should select row 3 and focus the view, got row %d", row)
	}
	// Outside the view, a wheel over the margin must not reach the tab beneath.
	mouse := func(x, y int, a tview.MouseAction) *tcell.EventMouse {
		ev, _ := d.onMouse(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone), a)
		return ev
	}
	if mouse(2, 5, tview.MouseScrollDown) != nil || !d.detailOpen() {
		t.Fatal("a wheel over the margin should be swallowed, leaving the view open")
	}
	// A move must pass: tview sends it ahead of the press in the same dispatch,
	// sharing the event, so swallowing it would drop the press after it.
	if mouse(2, 5, tview.MouseMove) == nil {
		t.Fatal("a move over the margin was swallowed; the press after it would be lost")
	}
	// A press outside dismisses it, and the release that ends the same click
	// is swallowed — which is what stops tview building a click on the tab.
	if mouse(2, 5, tview.MouseLeftDown) != nil || d.detailOpen() || d.app.GetFocus() != fin {
		t.Fatal("a press outside should close the view, back onto the finished list")
	}
	if mouse(2, 5, tview.MouseLeftUp) != nil {
		t.Error("the dismissing press's release reached the tab")
	}
	if mouse(2, 5, tview.MouseLeftUp) == nil {
		t.Error("only the one release should be swallowed")
	}

	// A click on the panel below the last row opens nothing and leaves the
	// selection alone (tview would otherwise select row -1, then reset to 1).
	click(x+4, y+6)
	if row, _ := fin.GetSelection(); d.detailOpen() || row != 3 {
		t.Errorf("a click on empty space should change nothing, got open=%v row=%d", d.detailOpen(), row)
	}
}

// Closing an overlay must hand the keyboard back to the panel the user was on.
// tview's HidePage re-focuses the page's default item (the tree) on the way,
// which is why the active panel follows intent, not focus events.
func TestHelpReturnsToFinishedList(t *testing.T) {
	d, _ := newTreeDashboard()
	pressKey(d, 'f')
	pressKey(d, '?')
	pressSpecial(d, tcell.KeyEscape)
	if d.app.GetFocus() != d.current.finished.table {
		t.Error("closing the help should return focus to the finished list")
	}
}

// TestFinishedSelectionFollowsPipeline checks a refresh that pushes a newly
// finished pipeline in on top keeps the selection on the same pipeline, not
// the same row — the row index would now point at a different run.
func TestFinishedSelectionFollowsPipeline(t *testing.T) {
	d, _ := newTreeDashboard()
	pressKey(d, 'f')
	fin := d.current.finished.table
	fin.Select(2, 0) // #97
	snap := d.snapshot
	snap.RecentPipelines = append([]gitlab.Pipeline{{ID: 99, ProjectPath: "acme/new", Ref: "main",
		Status: gitlab.StatusSuccess, Finished: time.Now()}}, snap.RecentPipelines...)
	d.current.update(snap)
	row, _ := fin.GetSelection()
	if p, _ := d.current.finishedAt(row); p.ID != 97 {
		t.Errorf("after the refresh the selection is on #%d (row %d), want #97", p.ID, row)
	}
}

// TestFinishedHoverFollowsScroll scrolls the finished list and checks the hover
// reveal names the row actually under the cursor, not the one that was there
// before scrolling.
func TestFinishedHoverFollowsScroll(t *testing.T) {
	now := time.Now()
	var recent []gitlab.Pipeline
	for i := range 30 {
		recent = append(recent, gitlab.Pipeline{ID: int64(1000 - i), ProjectPath: "acme/p" + refTag(i),
			Ref: "main", Status: gitlab.StatusSuccess, Finished: now.Add(-time.Duration(i) * time.Minute)})
	}
	snap := gitlab.Snapshot{RecentPipelines: recent, UpdatedAt: now}
	d := NewDashboard(gitlab.FakeService{Snap: snap}, Options{})
	d.snapshot = snap
	d.current.update(snap)
	pressKey(d, 'f')
	d.current.finished.table.Select(25, 0)
	_ = renderToText(t, d, 130, 32)

	fin := d.current.finished
	off, _ := fin.table.GetOffset()
	if off == 0 {
		t.Fatal("selecting row 25 should have scrolled the list")
	}
	x, y, _, _ := fin.table.GetInnerRect()
	path, ok := fin.hoverAt(x+4, y+1) // the first visible data row
	if want := recent[off].ProjectPath; !ok || path != want {
		t.Errorf("hover on the first visible row = %q, want %q (offset %d)", path, want, off)
	}
}

// TestDetailViewHasItsOwnBackground checks the modal is painted in its own
// background everywhere inside the frame — blank space, text cells (which must
// keep the panel's colour, not punch holes to the terminal's), the title row
// and the bottom line — while the margin around it keeps the tab's.
func TestDetailViewHasItsOwnBackground(t *testing.T) {
	d, trees := newTreeDashboard()
	p := d.snapshot.RecentPipelines[1]
	d.openDetail(p)
	d.detail.show(trees[p.ID], nil)
	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(120, 30)
	d.outer.SetRect(0, 0, 120, 30)
	d.outer.Draw(screen)

	bgAt := func(x, y int) tcell.Color {
		_, _, style, _ := screen.GetContent(x, y)
		_, bg, _ := style.Decompose()
		return bg
	}
	fx, fy, fw, fh := d.detail.frame.GetRect()
	sx, sy, _, _ := d.detail.table.GetInnerRect()
	for _, pt := range []struct {
		name string
		x, y int
	}{
		{"the title row", fx + 5, fy},
		{"a header cell", sx + 1, sy},
		{"a status cell (row 2; row 1 is selected)", sx + 30, sy + 2},
		{"blank space below the rows", fx + fw/2, fy + fh - 4},
		{"the bottom line", fx + 3, fy + fh - 2},
	} {
		if got := bgAt(pt.x, pt.y); got != detailBackground {
			t.Errorf("%s at (%d,%d): background %v, want the panel's %v", pt.name, pt.x, pt.y, got, detailBackground)
		}
	}
	if got := bgAt(fx-2, fy+5); got == detailBackground {
		t.Error("the margin outside the frame took the panel's background; the tab should show there")
	}
}

// TestDetailViewShowsRunners: a finished pipeline's job rows name the runner
// that ran each job — aliased as in the tree — so "which runner ran the slow
// job" can be read down the list; pipeline rows, and jobs that never ran, show
// none. The bottom line repeats the selected job's.
func TestDetailViewShowsRunners(t *testing.T) {
	snap := gitlab.SampleSnapshot()
	trees := gitlab.SampleTrees(snap)
	d := NewDashboard(gitlab.FakeService{Snap: snap, Trees: trees},
		Options{RunnerAliases: map[string]string{"shared-linux-01": "sl1"}})
	d.snapshot = snap
	d.current.update(snap)
	p := snap.RecentPipelines[1] // #97: build on docker-builder, deploy-staging on shared-linux-01, smoke-test skipped
	d.openDetail(p)
	d.detail.show(trees[p.ID], nil)
	out := renderToText(t, d, 120, 30)
	t.Logf("detail view:\n%s", out)

	tb := d.detail.table
	runnerOf := func(job string) string { // the RUNNER cell of the row whose name ends in job
		for row := 1; row < tb.GetRowCount(); row++ {
			if strings.HasSuffix(tb.GetCell(row, detColName).Text, "· "+job) {
				return tb.GetCell(row, detColRunner).Text
			}
		}
		t.Fatalf("no row for job %q", job)
		return ""
	}
	if got := tb.GetCell(1, detColRunner).Text; got != "" {
		t.Errorf("the pipeline row's RUNNER = %q, want blank", got)
	}
	for job, want := range map[string]string{"build": "docker-builder", "deploy-staging": "sl1", "smoke-test": ""} {
		if got := runnerOf(job); got != want {
			t.Errorf("%s: RUNNER = %q, want %q", job, got, want)
		}
	}
	if !strings.Contains(out, "RUNNER") || !strings.Contains(out, "docker-builder") || !strings.Contains(out, "sl1") {
		t.Errorf("RUNNER column not rendered whole at 120 columns")
	}
	for row := 1; row < tb.GetRowCount(); row++ {
		if strings.Contains(tb.GetCell(row, detColName).Text, "deploy-staging") {
			tb.Select(row, 0)
		}
	}
	if info := d.detail.info.GetText(true); !strings.Contains(info, "runner sl1") {
		t.Errorf("bottom line = %q, want the selected job's runner", info)
	}
}
