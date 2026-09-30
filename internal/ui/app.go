// Package ui implements the glute terminal dashboard: a tabbed, panel-based
// view over a gitlab.Service, refreshed in the background.
package ui

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/MarcelInTO/glute/internal/format"
	"github.com/MarcelInTO/glute/internal/gitlab"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const (
	pageMain    = "main"
	pageHelp    = "help"
	pageDetail  = "detail"
	pageCurrent = "current"
	pageWork    = "work"
	pageInfra   = "infra"
)

// detailFetchTimeout bounds the on-demand fetch behind the detail view: one
// GraphQL call per pipeline in the tree, a level of the tree at a time.
const detailFetchTimeout = time.Minute

const helpText = `glute — keys

  Tab / Shift-Tab    switch tabs
  1 / 2 / 3          Current / Work / Infrastructure
  ↑ / ↓              scroll the Current tree (or finished list)
  f                  move between the tree and the finished list
  Enter / click      open a finished pipeline's jobs and timeline
  Esc / q            close it
  t                  cycle the history window (1d / 7d / 30d)
  r                  refresh now
  ?                  toggle this help
  q / Ctrl-C         quit`

// Options configures a Dashboard.
type Options struct {
	RefreshInterval time.Duration
	Title           string // shown in the footer, e.g. "<instance> · <url>"
	LogPath         string // file the standard logger is redirected to
	// RunnerAliases maps a runner's full name to a short display label for the
	// Current tab; runners absent from the map show their real name.
	RunnerAliases map[string]string
}

// Dashboard is the top-level TUI application.
type Dashboard struct {
	app    *tview.Application
	outer  *tview.Pages // main + help overlay
	pages  *tview.Pages // tab bodies
	header *tview.TextView
	footer *tview.TextView
	help   *tview.Modal

	current *currentView
	work    *workView
	infra   *infraView
	detail  *detailView

	// detailSeq numbers each opening of the detail view, so a fetch that lands
	// after its view was closed, or replaced by another pipeline's, is dropped
	// rather than drawn over whatever is showing now.
	detailSeq int
	// ctx is the dashboard's lifetime, which the detail fetches run under;
	// Run replaces the background default with one it cancels on exit.
	ctx context.Context

	svc  gitlab.Service
	opts Options

	tabs      []string
	tabLabels []string
	active    int

	trigger    chan struct{}
	snapshot   gitlab.Snapshot
	lastErr    error
	refreshing bool
	hoverPath  string // full project path under the mouse, shown in the footer
	// dismissing is set by the press that closed the detail view, so the
	// release (and the click tview would build from it) that finish the same
	// gesture are swallowed rather than landing on the tab beneath.
	dismissing bool
	// window is the selected history lookback the Work and Infrastructure
	// panels aggregate over; zero (the default) means the full Top window. It's
	// session state only — not persisted — and is resolved against the
	// snapshot's Windows on every render (see cycleWindow).
	window time.Duration
}

// NewDashboard builds (but does not start) the dashboard.
func NewDashboard(svc gitlab.Service, opts Options) *Dashboard {
	if opts.RefreshInterval <= 0 {
		opts.RefreshInterval = 10 * time.Second
	}

	current := newCurrentView(opts.RunnerAliases)
	work := newWorkView()
	infra := newInfraView(opts.RunnerAliases)
	detail := newDetailView(opts.RunnerAliases)

	pages := tview.NewPages()
	pages.AddPage(pageCurrent, current.root, true, true)
	pages.AddPage(pageWork, work.root, true, false)
	pages.AddPage(pageInfra, infra.root, true, false)

	header := tview.NewTextView()
	header.SetDynamicColors(true)
	footer := tview.NewTextView()
	footer.SetDynamicColors(true)

	main := tview.NewFlex().SetDirection(tview.FlexRow)
	main.AddItem(header, 1, 0, false)
	main.AddItem(pages, 0, 1, true)
	main.AddItem(footer, 1, 0, false)

	help := tview.NewModal()
	help.SetText(helpText)
	help.AddButtons([]string{"Close"})

	outer := tview.NewPages()
	outer.AddPage(pageMain, main, true, true)
	outer.AddPage(pageHelp, help, true, false)
	outer.AddPage(pageDetail, detail.root, true, false)

	d := &Dashboard{
		app:       tview.NewApplication(),
		outer:     outer,
		pages:     pages,
		header:    header,
		footer:    footer,
		help:      help,
		current:   current,
		work:      work,
		infra:     infra,
		detail:    detail,
		ctx:       context.Background(),
		svc:       svc,
		opts:      opts,
		tabs:      []string{pageCurrent, pageWork, pageInfra},
		tabLabels: []string{"Current", "Work", "Infrastructure"},
		trigger:   make(chan struct{}, 1),
	}

	// Selecting a row in the Current tree reveals that row's full project path
	// in the footer, the scroll-safe counterpart to the mouse hover the other
	// tabs use (a scrolling table breaks the hover's fixed row math).
	// Either panel's selection only speaks for the footer while that panel has
	// the keyboard: a refresh re-selects in both, and the other one's path would
	// otherwise replace the one the user is looking at.
	current.table.SetSelectionChangedFunc(func(row, _ int) {
		if current.finishedActive {
			return
		}
		if path, ok := current.pathAtRow(row); ok {
			d.hoverPath = path
		} else {
			d.hoverPath = ""
		}
		d.updateFooter()
	})
	current.finished.table.SetSelectionChangedFunc(func(row, _ int) {
		if !current.finishedActive {
			return
		}
		d.hoverPath = ""
		if p, ok := current.finishedAt(row); ok {
			d.hoverPath = p.ProjectPath
		}
		d.updateFooter()
	})
	current.finished.table.SetSelectedFunc(func(row, _ int) {
		if p, ok := current.finishedAt(row); ok {
			d.openDetail(p)
		}
	})

	help.SetDoneFunc(func(int, string) { d.hideHelp() })
	d.app.SetInputCapture(d.onKey)
	d.app.SetMouseCapture(d.onMouse)
	d.app.SetRoot(outer, true)
	d.app.EnableMouse(true)
	d.focusActive()
	return d
}

// Run starts the background refresh loop and blocks on the UI event loop until
// the user quits.
func (d *Dashboard) Run() error {
	if f, err := setupLogging(d.opts.LogPath); err != nil {
		log.SetOutput(io.Discard) // never let log noise corrupt the screen
	} else {
		defer f.Close()
	}
	log.Printf("dashboard starting: %s", d.opts.Title)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.ctx = ctx

	go watchDumpSignal(ctx)
	go d.refreshLoop(ctx)
	d.triggerRefresh()
	d.updateHeader()
	d.updateFooter()

	err := d.app.Run()
	log.Printf("dashboard stopped")
	return err
}

func (d *Dashboard) onKey(ev *tcell.EventKey) *tcell.EventKey {
	// While help is open, only our close keys act; everything else goes to it.
	switch name, _ := d.outer.GetFrontPage(); name {
	case pageHelp:
		if ev.Key() == tcell.KeyEscape || ev.Rune() == '?' || ev.Rune() == 'q' {
			d.hideHelp()
			return nil
		}
		return ev
	case pageDetail:
		// Likewise the detail view: q closes it (as it does the help) rather than
		// quitting from under it, and the tab keys stay inert while it's up —
		// everything else scrolls its table.
		switch {
		case ev.Key() == tcell.KeyEscape || ev.Rune() == 'q':
			d.closeDetail()
			return nil
		case ev.Key() == tcell.KeyCtrlC:
			d.app.Stop()
			return nil
		}
		return ev
	}

	switch ev.Key() {
	case tcell.KeyCtrlC:
		d.app.Stop()
		return nil
	case tcell.KeyTab:
		d.cycleTab(1)
		return nil
	case tcell.KeyBacktab:
		d.cycleTab(-1)
		return nil
	}

	switch ev.Rune() {
	case 'q':
		d.app.Stop()
		return nil
	case 'r':
		d.triggerRefresh()
		return nil
	case 't':
		d.cycleWindow()
		return nil
	case 'f':
		if d.tabs[d.active] == pageCurrent {
			d.app.SetFocus(d.current.toggleFocusTarget())
			d.syncCurrentFooter()
			return nil
		}
	case '?':
		d.showHelp()
		return nil
	case '1':
		d.selectTab(0)
		return nil
	case '2':
		d.selectTab(1)
		return nil
	case '3':
		d.selectTab(2)
		return nil
	}
	return ev
}

func (d *Dashboard) selectTab(i int) {
	if i < 0 || i >= len(d.tabs) {
		return
	}
	d.active = i
	d.pages.SwitchToPage(d.tabs[i])
	// The footer's hover/selection detail belongs to the tab it came from; on
	// the Current tab, re-derive it from the row that's already selected so the
	// path shows immediately rather than only after the next selection move.
	d.hoverPath = ""
	d.focusActive()
	if d.tabs[i] == pageCurrent {
		d.syncCurrentFooter()
	}
	d.updateHeader()
	d.updateFooter()
}

// syncCurrentFooter re-derives the footer's path from the selected row of
// whichever Current panel has the keyboard, so it shows at once when that
// panel changes (a tab switch, the f key) rather than only on the next move.
func (d *Dashboard) syncCurrentFooter() {
	d.hoverPath = ""
	if d.current.finishedActive {
		row, _ := d.current.finished.table.GetSelection()
		if p, ok := d.current.finishedAt(row); ok {
			d.hoverPath = p.ProjectPath
		}
	} else if row, _ := d.current.table.GetSelection(); row > 0 {
		if path, ok := d.current.pathAtRow(row); ok {
			d.hoverPath = path
		}
	}
	d.updateFooter()
}

// focusActive directs keyboard focus at the active tab's primitive. On the
// Current tab that's whichever of its two tables last had the keyboard (the
// tree, unless the user moved to the finished list), which must hold focus for
// its arrow-key scrolling; the other tabs have no focusable widget, so focus
// rests on the pages container.
func (d *Dashboard) focusActive() {
	if d.tabs[d.active] == pageCurrent {
		d.app.SetFocus(d.current.focusTarget())
	} else {
		d.app.SetFocus(d.pages)
	}
}

func (d *Dashboard) cycleTab(delta int) {
	n := len(d.tabs)
	d.selectTab(((d.active+delta)%n + n) % n)
}

// cycleWindow steps the history window to the next of the snapshot's selectable
// lookbacks — ascending, wrapping from the full window (the default) back to the
// shortest. It's a pure view switch: the snapshot already carries every window's
// aggregates, so nothing is fetched or re-aggregated, and the change applies to
// every windowed panel at once (Work and Infrastructure both). Before the first
// snapshot lands there are no windows to cycle, so the key is a no-op.
func (d *Dashboard) cycleWindow() {
	ws := d.snapshot.Windows
	if len(ws) == 0 {
		return
	}
	i := len(ws) - 1 // an unmatched selection (incl. the zero default) counts as the full window
	for k, w := range ws {
		if w.Window == d.window {
			i = k
			break
		}
	}
	d.window = ws[(i+1)%len(ws)].Window
	d.renderHistory()
	d.updateHeader()
}

// renderHistory (re)fills the windowed panels from the held snapshot at the
// selected window. The Current tab isn't windowed, so it's not touched here.
func (d *Dashboard) renderHistory() {
	h := d.snapshot.WindowAt(d.window)
	d.work.update(d.snapshot, h)
	d.infra.update(h)
}

func (d *Dashboard) showHelp() {
	d.outer.ShowPage(pageHelp)
	d.app.SetFocus(d.help)
}

func (d *Dashboard) hideHelp() {
	d.outer.HidePage(pageHelp)
	d.focusActive()
}

// openDetail shows the detail view for a finished pipeline and fetches its tree
// in the background: the view opens at once in its loading state, and the tree
// replaces it when the fetch lands — unless the view was closed or moved on to
// another pipeline in the meantime (detailSeq).
func (d *Dashboard) openDetail(p gitlab.Pipeline) {
	d.detailSeq++
	seq := d.detailSeq
	d.detail.loading(p)
	d.outer.ShowPage(pageDetail)
	d.app.SetFocus(d.detail.table)
	d.updateFooter()

	parent := d.ctx
	go func() {
		ctx, cancel := context.WithTimeout(parent, detailFetchTimeout)
		defer cancel()
		tree, err := d.svc.PipelineTree(ctx, p)
		if err != nil {
			log.Printf("pipeline tree %s #%d: %v", p.ProjectPath, p.ID, err)
		}
		d.app.QueueUpdateDraw(func() {
			if seq != d.detailSeq {
				return
			}
			d.detail.show(tree, err)
		})
	}()
}

// closeDetail hides the detail view and hands the keyboard back to the panel
// it was opened from.
func (d *Dashboard) closeDetail() {
	d.detailSeq++ // a fetch still in flight is for a view nobody's looking at
	d.outer.HidePage(pageDetail)
	d.focusActive()
	d.updateFooter()
}

// onMouse reveals the full project path of the row under the cursor in the
// footer. tview has no native tooltip, so this is the conventional
// hover-detail; it follows the mouse where the terminal reports motion and
// otherwise updates on click.
func (d *Dashboard) onMouse(event *tcell.EventMouse, action tview.MouseAction) (*tcell.EventMouse, tview.MouseAction) {
	if event == nil {
		return event, action
	}
	x, y := event.Position()
	if d.dismissing && action == tview.MouseLeftUp {
		// Swallowing the release also stops tview building a click from it.
		d.dismissing = false
		return nil, action
	}
	switch name, _ := d.outer.GetFrontPage(); name {
	case pageHelp:
		return event, action // don't chase the mouse under the help overlay
	case pageDetail:
		// A press outside the detail view dismisses it, as clicking off a
		// modal does; inside, the view's own table handles clicks and
		// scrolling. Everything else outside it is swallowed: tview's Pages
		// would hand an event the view didn't take to the tab beneath, so a
		// wheel over the margin would scroll the tab under the modal. It's the
		// press, not the click, that dismisses: tview builds a click only from
		// a release that got through, and letting that release through would
		// let the click land on the tab (see dismissing).
		//
		// A move is never swallowed. tview dispatches one terminal event as a
		// move (if the pointer moved) and then the press or release, sharing
		// one event between them: swallowing the move would drop the press
		// that follows it, so a click on the margin would never arrive. A move
		// reaching the tab is harmless.
		if d.detail.contains(x, y) || action == tview.MouseMove {
			return event, action
		}
		if action == tview.MouseLeftDown {
			d.closeDetail()
			d.dismissing = true
		}
		return nil, action
	}

	// The Current tab's tree table scrolls, so its footer detail is driven by row
	// selection (see SetSelectionChangedFunc), not by mouse position. The
	// non-scrolling "recently finished" panel below it has stable row math,
	// though, so it gets the usual hover reveal — but only while the cursor is
	// actually over one of its rows, so passing over the tree above leaves the
	// selection-derived path untouched.
	//
	// A click on a finished row opens that pipeline's detail view straight away
	// — what clicking a row in a list means — with the panel taking focus and
	// the row selected first, so closing the view comes back to it. The click is
	// consumed, since the table would otherwise re-select the same row; so is a
	// click on the panel that misses every row, which tview would turn into a
	// selection of row -1 and then quietly reset to the top.
	if d.tabs[d.active] == pageCurrent {
		if action == tview.MouseLeftDown {
			d.current.pressAt(x, y)
		}
		if action == tview.MouseLeftClick && d.current.finished.table.InRect(x, y) {
			if idx, ok := d.current.finished.rowAt(x, y); ok {
				if p, ok := d.current.finishedAt(idx + 1); ok {
					d.current.finishedActive = true
					d.app.SetFocus(d.current.finished.table)
					d.current.finished.table.Select(idx+1, 0)
					d.openDetail(p)
				}
			}
			return nil, action
		}
		if path, ok := d.current.finished.hoverAt(x, y); ok && path != d.hoverPath {
			d.hoverPath = path
			d.updateFooter()
		}
		return event, action
	}

	hover := ""
	if path, ok := d.hoverPathAt(x, y); ok {
		hover = path
	}
	if hover != d.hoverPath {
		d.hoverPath = hover
		d.updateFooter()
	}
	return event, action
}

func (d *Dashboard) hoverPathAt(x, y int) (string, bool) {
	if d.tabs[d.active] == pageWork {
		return d.work.hoverPathAt(x, y)
	}
	// The Current tab reveals paths via selection, not hover; the Infrastructure
	// tab's panels are keyed by tag/runner, so they have no path to reveal.
	return "", false
}

func (d *Dashboard) triggerRefresh() {
	select {
	case d.trigger <- struct{}{}:
	default: // a refresh is already pending; coalesce
	}
}

func (d *Dashboard) refreshLoop(ctx context.Context) {
	data := time.NewTicker(d.opts.RefreshInterval)
	defer data.Stop()
	// A one-second tick counts the running timers up between data refreshes and
	// keeps the footer's "updated Ns ago" fresh.
	sec := time.NewTicker(time.Second)
	defer sec.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.trigger:
			d.doRefresh(ctx)
		case <-data.C:
			d.doRefresh(ctx)
		case <-sec.C:
			d.app.QueueUpdateDraw(func() {
				d.current.tick()
				d.updateFooter()
			})
		}
	}
}

func (d *Dashboard) doRefresh(ctx context.Context) {
	d.app.QueueUpdateDraw(func() {
		d.refreshing = true
		d.updateFooter()
	})

	snap, err := d.svc.Refresh(ctx)

	d.app.QueueUpdateDraw(func() {
		d.refreshing = false
		d.lastErr = err
		if err == nil {
			d.snapshot = snap
			d.current.update(snap)
			d.renderHistory()
			d.updateHeader() // the first snapshot names the windows the header shows
		}
		d.updateFooter()
	})

	if err != nil {
		log.Printf("refresh error: %v", err)
		return
	}
	log.Printf("refresh ok: %d projects, %d running pipelines, %d running jobs, %d warnings",
		snap.Projects, len(snap.RunningPipelines), len(snap.RunningJobs), len(snap.Errors))
}

func (d *Dashboard) updateHeader() {
	var b strings.Builder
	b.WriteString(" [::b]glute[::-]   ")
	for i, label := range d.tabLabels {
		if i == d.active {
			fmt.Fprintf(&b, "[black:aqua] %d %s [-:-]  ", i+1, label)
		} else {
			fmt.Fprintf(&b, "[silver]%d %s[-]  ", i+1, label)
		}
	}
	// The history window is global state (it applies to the Work and
	// Infrastructure panels alike), so it sits up here beside the tabs rather
	// than only in those tabs' panel titles — which also means the Current tab
	// shows what `t` just did. Unknown until the first snapshot names the windows.
	if h := d.snapshot.WindowAt(d.window); h.Window > 0 {
		fmt.Fprintf(&b, "  [silver]window[-] last %s", format.Window(h.Window))
	}
	d.header.SetText(b.String())
}

func (d *Dashboard) updateFooter() {
	hints := "[silver]Tab switch · t window · r refresh · ? help · q quit[-]"
	path := d.hoverPath
	switch {
	case d.detailOpen():
		// The detail view shows its own row's path; the footer carries only
		// the status and the keys that work while it's up.
		path = ""
		hints = "[silver]↑/↓ scroll · Esc/q close · r refresh[-]"
	}

	// While hovering a row, reveal that project's full path.
	if path != "" {
		d.footer.SetText(fmt.Sprintf(" [aqua]%s[-]    %s", tview.Escape(path), hints))
		return
	}

	var status string
	switch {
	case d.refreshing:
		status = "[yellow]refreshing…[-]"
	case d.snapshot.UpdatedAt.IsZero():
		status = "starting…"
	default:
		status = fmt.Sprintf("updated %s ago · %d project(s)", format.Ago(d.snapshot.UpdatedAt), d.snapshot.Projects)
	}

	trailer := ""
	switch {
	case d.lastErr != nil:
		trailer = fmt.Sprintf("   [red]! %s[-]", format.Trunc(d.lastErr.Error(), 48))
	case len(d.snapshot.Errors) > 0:
		trailer = fmt.Sprintf("   [red]! %d warning(s)[-]", len(d.snapshot.Errors))
	}

	title := ""
	if d.opts.Title != "" {
		title = "  [silver]" + format.Trunc(d.opts.Title, 40) + "[-]"
	}

	d.footer.SetText(fmt.Sprintf(" %s%s%s    %s", status, trailer, title, hints))
}

// detailOpen reports whether the detail view is up.
func (d *Dashboard) detailOpen() bool {
	name, _ := d.outer.GetFrontPage()
	return name == pageDetail
}

// watchDumpSignal writes every goroutine's stack to the log whenever a dump
// signal (SIGUSR1 on Unix) arrives, so a wedged glute can be diagnosed with
// `kill -USR1 <pid>` — the trace lands in glute.log without disturbing the
// screen and without needing a debugger (ptrace attach is often restricted).
// On platforms with no dump signal (Windows) it does nothing. SIGQUIT still
// triggers the Go runtime's own crash-dump to stderr as a fallback.
func watchDumpSignal(ctx context.Context) {
	if len(dumpSignals) == 0 {
		return
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, dumpSignals...)
	defer signal.Stop(ch)
	for {
		select {
		case <-ctx.Done():
			return
		case sig := <-ch:
			dumpGoroutines(sig)
		}
	}
}

// dumpGoroutines logs the stacks of all goroutines. The buffer grows until the
// whole dump fits, since runtime.Stack truncates to what's given.
func dumpGoroutines(sig os.Signal) {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	log.Printf("=== goroutine dump on %s (%d goroutines) ===\n%s=== end goroutine dump ===",
		sig, runtime.NumGoroutine(), buf)
}

// setupLogging points the standard logger at the given file, since a TUI owns
// the screen and can't safely write to stdout/stderr.
func setupLogging(path string) (*os.File, error) {
	if path == "" {
		return nil, fmt.Errorf("no log path configured")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	log.SetOutput(f)
	log.SetFlags(log.LstdFlags)
	return f, nil
}
