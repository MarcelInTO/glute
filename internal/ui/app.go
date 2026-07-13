// Package ui implements the glute terminal dashboard: a tabbed, panel-based
// view over a gitlab.Service, refreshed in the background.
package ui

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MarcelInTO/glute/internal/format"
	"github.com/MarcelInTO/glute/internal/gitlab"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const (
	pageMain      = "main"
	pageHelp      = "help"
	pageCurrent   = "current"
	pagePipelines = "pipelines"
	pageJobs      = "jobs"
)

const helpText = `glute — keys

  Tab / Shift-Tab    switch tabs
  1 / 2 / 3          Current / Pipelines / Jobs
  ↑ / ↓              scroll the Current tree
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

	current   *currentView
	pipelines *pipelineView
	jobs      *jobView

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
}

// NewDashboard builds (but does not start) the dashboard.
func NewDashboard(svc gitlab.Service, opts Options) *Dashboard {
	if opts.RefreshInterval <= 0 {
		opts.RefreshInterval = 30 * time.Second
	}

	current := newCurrentView(opts.RunnerAliases)
	pipe := newPipelineView()
	jobs := newJobView()

	pages := tview.NewPages()
	pages.AddPage(pageCurrent, current.root, true, true)
	pages.AddPage(pagePipelines, pipe.root, true, false)
	pages.AddPage(pageJobs, jobs.root, true, false)

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

	d := &Dashboard{
		app:       tview.NewApplication(),
		outer:     outer,
		pages:     pages,
		header:    header,
		footer:    footer,
		help:      help,
		current:   current,
		pipelines: pipe,
		jobs:      jobs,
		svc:       svc,
		opts:      opts,
		tabs:      []string{pageCurrent, pagePipelines, pageJobs},
		tabLabels: []string{"Current", "Pipelines", "Jobs"},
		trigger:   make(chan struct{}, 1),
	}

	// Selecting a row in the Current tree reveals that row's full project path
	// in the footer, the scroll-safe counterpart to the mouse hover the other
	// tabs use (a scrolling table breaks the hover's fixed row math).
	current.table.SetSelectionChangedFunc(func(row, _ int) {
		if path, ok := current.pathAtRow(row); ok {
			d.hoverPath = path
		} else {
			d.hoverPath = ""
		}
		d.updateFooter()
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
	if name, _ := d.outer.GetFrontPage(); name == pageHelp {
		if ev.Key() == tcell.KeyEscape || ev.Rune() == '?' || ev.Rune() == 'q' {
			d.hideHelp()
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
	if d.tabs[i] == pageCurrent {
		if row, _ := d.current.table.GetSelection(); row > 0 {
			if path, ok := d.current.pathAtRow(row); ok {
				d.hoverPath = path
			}
		}
	}
	d.focusActive()
	d.updateHeader()
	d.updateFooter()
}

// focusActive directs keyboard focus at the active tab's primitive. The Current
// tab's table must hold focus for its arrow-key scrolling; the other tabs have
// no focusable widget, so focus rests on the pages container.
func (d *Dashboard) focusActive() {
	if d.tabs[d.active] == pageCurrent {
		d.app.SetFocus(d.current.table)
	} else {
		d.app.SetFocus(d.pages)
	}
}

func (d *Dashboard) cycleTab(delta int) {
	n := len(d.tabs)
	d.selectTab(((d.active+delta)%n + n) % n)
}

func (d *Dashboard) showHelp() {
	d.outer.ShowPage(pageHelp)
	d.app.SetFocus(d.help)
}

func (d *Dashboard) hideHelp() {
	d.outer.HidePage(pageHelp)
	d.focusActive()
}

// onMouse reveals the full project path of the row under the cursor in the
// footer. tview has no native tooltip, so this is the conventional
// hover-detail; it follows the mouse where the terminal reports motion and
// otherwise updates on click.
func (d *Dashboard) onMouse(event *tcell.EventMouse, action tview.MouseAction) (*tcell.EventMouse, tview.MouseAction) {
	if event == nil {
		return event, action
	}
	if name, _ := d.outer.GetFrontPage(); name == pageHelp {
		return event, action // don't chase the mouse under the help overlay
	}
	// The Current tab scrolls, so its footer detail is driven by row selection
	// (see SetSelectionChangedFunc), not by mouse position; let tview handle the
	// click for selection and leave the footer alone.
	if d.tabs[d.active] == pageCurrent {
		return event, action
	}

	hover := ""
	x, y := event.Position()
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
	switch d.tabs[d.active] {
	case pagePipelines:
		return d.pipelines.hoverPathAt(x, y)
	case pageJobs:
		return d.jobs.hoverPathAt(x, y)
	}
	return "", false // the Current tab reveals paths via selection, not hover
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
	foot := time.NewTicker(time.Second) // keeps the "updated Ns ago" fresh
	defer foot.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.trigger:
			d.doRefresh(ctx)
		case <-data.C:
			d.doRefresh(ctx)
		case <-foot.C:
			d.app.QueueUpdateDraw(d.updateFooter)
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
			d.pipelines.update(snap)
			d.jobs.update(snap)
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
	d.header.SetText(b.String())
}

func (d *Dashboard) updateFooter() {
	const hints = "[silver]Tab switch · r refresh · ? help · q quit[-]"

	// While hovering a row, reveal that project's full path.
	if d.hoverPath != "" {
		d.footer.SetText(fmt.Sprintf(" [aqua]%s[-]    %s", tview.Escape(d.hoverPath), hints))
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
