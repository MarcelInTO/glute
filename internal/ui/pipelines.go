package ui

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/MarcelInTO/glute/internal/format"
	"github.com/MarcelInTO/glute/internal/gitlab"
	"github.com/rivo/tview"
)

// minFailRuns is the run floor for the "fails most often" panel: a project needs
// at least this many runs before its failure rate is trusted, so a 1-of-1 fluke
// doesn't top a panel that's meant to surface chronically-failing pipelines.
const minFailRuns = 5

// pipelineView is the Pipelines tab: a 2x2 grid of historical-analysis panels
// over the Top window (default 30d). Unlike the Current tab it says nothing about
// what's running now — it's for spotting where to improve the pipelines, the
// infrastructure, and the projects, and which kinds of capacity cost the most
// runner time. Everything aggregates by project/tag/runner; the ref is dropped,
// since that's how this analysis is done by hand.
type pipelineView struct {
	root    *tview.Flex
	fails   *panelTable
	slow    *panelTable
	tags    *panelTable
	runners *panelTable
	aliases map[string]string // runner full name → short display label
}

func newPipelineView(aliases map[string]string) *pipelineView {
	fails := newPanelTable("Fails most often · last 30d")
	slow := newPanelTable("Slowest · last 30d")
	tags := newPanelTable("Tag performance · last 30d")
	runners := newPanelTable("Runner performance · last 30d")

	top := tview.NewFlex().SetDirection(tview.FlexColumn)
	top.AddItem(fails.table, 0, 1, false)
	top.AddItem(slow.table, 0, 1, false)

	bottom := tview.NewFlex().SetDirection(tview.FlexColumn)
	bottom.AddItem(tags.table, 0, 1, false)
	bottom.AddItem(runners.table, 0, 1, false)

	root := tview.NewFlex().SetDirection(tview.FlexRow)
	root.AddItem(top, 0, 1, false)
	root.AddItem(bottom, 0, 1, false)

	return &pipelineView{root: root, fails: fails, slow: slow, tags: tags, runners: runners, aliases: aliases}
}

func (v *pipelineView) update(s gitlab.Snapshot) {
	fillFailsPanel(v.fails, s.PipelineStats)
	fillSlowestPanel(v.slow, s.PipelineStats)
	fillJobStatsPanel(v.tags, "TAG", s.TagStats, func(tag string) string { return tag })
	fillJobStatsPanel(v.runners, "RUNNER", s.RunnerStats, v.displayRunner)
}

// displayRunner maps a runner's full name to its configured short label, or
// returns the name unchanged when no alias is set (mirrors the Current tab).
func (v *pipelineView) displayRunner(runner string) string {
	if short, ok := v.aliases[runner]; ok {
		return short
	}
	return runner
}

// hoverPathAt returns the full project path under (x, y) for the project-keyed
// panels (fails/slowest); the tag and runner panels aren't keyed by a project,
// so they record no paths and never match.
func (v *pipelineView) hoverPathAt(x, y int) (string, bool) {
	for _, p := range []*panelTable{v.fails, v.slow} {
		if path, ok := p.hoverAt(x, y); ok {
			return path, true
		}
	}
	return "", false
}

// fillFailsPanel lists the projects failing the largest fraction of the time,
// among those with enough runs to trust the rate (minFailRuns). Ordered by
// failure rate — what you see is what it's sorted by — with the raw run count for
// the sample size behind each percentage. Points at projects that need
// reliability work.
func fillFailsPanel(p *panelTable, stats []gitlab.PipelineStats) {
	p.reset("PROJECT", "RUNS", "FAIL%")
	rightAlignHeaders(p.table, 1, 2)
	p.table.ScrollToBeginning()

	rows := make([]gitlab.PipelineStats, 0, len(stats))
	for _, s := range stats {
		if s.Runs >= minFailRuns && s.Failed > 0 {
			rows = append(rows, s)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if ri, rj := rows[i].FailRate(), rows[j].FailRate(); ri != rj {
			return ri > rj
		}
		if rows[i].Failed != rows[j].Failed {
			return rows[i].Failed > rows[j].Failed
		}
		return rows[i].ProjectPath < rows[j].ProjectPath
	})
	if len(rows) == 0 {
		emptyRow(p.table, 3)
		return
	}
	for i, s := range rows {
		r := i + 1
		p.addPath(s.ProjectPath)
		p.table.SetCell(r, 0, textCell(format.Trunc(format.Base(s.ProjectPath), 24)))
		p.table.SetCell(r, 1, numCell(strconv.Itoa(s.Runs)))
		p.table.SetCell(r, 2, numCell(fmt.Sprintf("%.0f%%", s.FailRate()*100)))
	}
}

// fillSlowestPanel shows each project's wall-clock pipeline-duration spread
// (min/mean/p95/max) over runs with a known duration, ordered by mean descending.
// The full spread — not just the mean — is what points at build-process work: a
// wide min-to-max or a p95 far above the mean flags inconsistency worth chasing.
func fillSlowestPanel(p *panelTable, stats []gitlab.PipelineStats) {
	p.reset("PROJECT", "RUNS", "MIN", "MEAN", "P95", "MAX")
	rightAlignHeaders(p.table, 1, 2, 3, 4, 5)
	p.table.ScrollToBeginning()

	rows := make([]gitlab.PipelineStats, 0, len(stats))
	for _, s := range stats {
		if s.KnownDurations > 0 {
			rows = append(rows, s)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].DurMean != rows[j].DurMean {
			return rows[i].DurMean > rows[j].DurMean
		}
		return rows[i].ProjectPath < rows[j].ProjectPath
	})
	if len(rows) == 0 {
		emptyRow(p.table, 6)
		return
	}
	for i, s := range rows {
		r := i + 1
		p.addPath(s.ProjectPath)
		p.table.SetCell(r, 0, textCell(format.Trunc(format.Base(s.ProjectPath), 16)))
		p.table.SetCell(r, 1, numCell(strconv.Itoa(s.Runs)))
		p.table.SetCell(r, 2, numCell(format.HMS(s.DurMin)))
		p.table.SetCell(r, 3, numCell(format.HMS(s.DurMean)))
		p.table.SetCell(r, 4, numCell(format.HMS(s.DurP95)))
		p.table.SetCell(r, 5, numCell(format.HMS(s.DurMax)))
	}
}

// fillJobStatsPanel compares job-load groups — the runners jobs ran on, or the
// runner tags they were invoked with — by the load each carried: jobs run,
// runner time consumed, and mean queue wait (how long jobs sat before being
// picked up — a saturation signal). display maps a key to its display label
// (runner aliasing; identity for tags). Rows arrive ordered by compute
// descending, so the biggest consumers lead.
func fillJobStatsPanel(p *panelTable, keyHeader string, stats []gitlab.JobStats, display func(string) string) {
	p.reset(keyHeader, "JOBS", "COMPUTE", "QUEUE")
	rightAlignHeaders(p.table, 1, 2, 3)
	p.table.ScrollToBeginning()
	if len(stats) == 0 {
		emptyRow(p.table, 4)
		return
	}
	for i, s := range stats {
		r := i + 1
		p.table.SetCell(r, 0, textCell(format.Trunc(display(s.Key), 18)))
		p.table.SetCell(r, 1, numCell(strconv.Itoa(s.Jobs)))
		p.table.SetCell(r, 2, numCell(format.Compute(s.Compute)))
		p.table.SetCell(r, 3, numCell(format.Duration(s.MeanQueue)))
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
