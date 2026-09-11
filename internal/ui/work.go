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

// workView is the Work tab: a 2x2 grid of historical panels about the *work*
// being run — pipelines on top, the jobs inside them below — over the selected
// history window (the `t` key; the full Top window, default 30d, until changed).
// Unlike the Current tab it says nothing about what's running now,
// and unlike the Infrastructure tab it's keyed by project rather than by
// capacity: the question here is "which projects' pipelines and jobs need
// attention", not "which runners are saturated". Everything aggregates by
// project; the ref is dropped, since that's how this analysis is done by hand.
type workView struct {
	root   *tview.Flex
	fails  *panelTable
	slow   *panelTable
	recent *panelTable
	top    *panelTable
}

func newWorkView() *workView {
	// The windowed panels get their "· last Nd" title suffix in update, once a
	// snapshot says which window is showing.
	// PROJECT is the identity column everywhere here; the job panels add JOB
	// beside it, since "which project" alone doesn't say which step failed.
	fails := newPanelTable(titleFails, 0)
	slow := newPanelTable(titleSlowest, 0)
	recent := newPanelTable("Recent jobs · failures & successes", 0, 1)
	top := newPanelTable(titleTopJobs, 0, 1)

	pipeRow := tview.NewFlex().SetDirection(tview.FlexColumn)
	pipeRow.AddItem(fails.table, 0, 1, false)
	pipeRow.AddItem(slow.table, 0, 1, false)

	jobRow := tview.NewFlex().SetDirection(tview.FlexColumn)
	jobRow.AddItem(recent.table, 0, 1, false)
	jobRow.AddItem(top.table, 0, 1, false)

	root := tview.NewFlex().SetDirection(tview.FlexRow)
	root.AddItem(pipeRow, 0, 1, false)
	root.AddItem(jobRow, 0, 1, false)

	return &workView{root: root, fails: fails, slow: slow, recent: recent, top: top}
}

// Base titles of the windowed panels; update appends the window suffix.
const (
	titleFails   = "Fails most often"
	titleSlowest = "Slowest"
	titleTopJobs = "Top jobs · avg length"
)

// update fills the panels: the three history panels from h (the snapshot's
// aggregates at the selected window), the recent-jobs panel from the snapshot's
// recent window — it's a newest-first event list over the configured
// recent_window, not a windowed aggregate, so the `t` key doesn't move it.
func (v *workView) update(s gitlab.Snapshot, h gitlab.WindowStats) {
	suffix := windowSuffix(h.Window)
	v.fails.setTitle(titleFails + suffix)
	v.slow.setTitle(titleSlowest + suffix)
	v.top.setTitle(titleTopJobs + suffix)
	fillFailsPanel(v.fails, h.PipelineStats)
	fillSlowestPanel(v.slow, h.PipelineStats)
	fillRecentJobs(v.recent, s.RecentJobs)
	fillTopJobs(v.top, h.TopJobs)
}

// hoverPathAt returns the full project path under (x, y). Every panel on this
// tab is project-keyed, so all four record paths.
func (v *workView) hoverPathAt(x, y int) (string, bool) {
	for _, p := range []*panelTable{v.fails, v.slow, v.recent, v.top} {
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
		p.table.SetCell(r, 0, nameCell(format.Base(s.ProjectPath)))
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
		p.table.SetCell(r, 0, nameCell(format.Base(s.ProjectPath)))
		p.table.SetCell(r, 1, numCell(strconv.Itoa(s.Runs)))
		p.table.SetCell(r, 2, numCell(format.HMS(s.DurMin)))
		p.table.SetCell(r, 3, numCell(format.HMS(s.DurMean)))
		p.table.SetCell(r, 4, numCell(format.HMS(s.DurP95)))
		p.table.SetCell(r, 5, numCell(format.HMS(s.DurMax)))
	}
}

// fillRecentJobs lists individual job outcomes over the recent window, newest
// first. It's the per-job counterpart to the pipeline panels above: those say
// which projects fail or drag, this says which job it actually was.
func fillRecentJobs(p *panelTable, jobs []gitlab.Job) {
	p.reset("PROJECT", "JOB", "STATUS", "DURATION", "WHEN")
	rightAlignHeaders(p.table, 3, 4)
	p.table.ScrollToBeginning()
	if len(jobs) == 0 {
		emptyRow(p.table, 5)
		return
	}
	for i, j := range jobs {
		r := i + 1
		p.addPath(j.ProjectPath)
		p.table.SetCell(r, 0, nameCell(format.Base(j.ProjectPath)))
		p.table.SetCell(r, 1, nameCell(j.Name))
		p.table.SetCell(r, 2, statusCell(j.Status))
		p.table.SetCell(r, 3, numCell(format.Duration(j.Duration)))
		p.table.SetCell(r, 4, numCell(format.Ago(j.Finished)))
	}
}

// fillTopJobs ranks the most-run jobs by how long they take and how often they
// pass — the job-level view of where a project's pipeline time goes and which
// steps are flaky.
func fillTopJobs(p *panelTable, aggs []gitlab.JobAgg) {
	p.reset("PROJECT", "JOB", "RUNS", "AVG", "SUCCESS")
	rightAlignHeaders(p.table, 2, 3, 4)
	p.table.ScrollToBeginning()
	if len(aggs) == 0 {
		emptyRow(p.table, 5)
		return
	}
	for i, a := range aggs {
		r := i + 1
		p.addPath(a.ProjectPath)
		p.table.SetCell(r, 0, nameCell(format.Base(a.ProjectPath)))
		p.table.SetCell(r, 1, nameCell(a.Name))
		p.table.SetCell(r, 2, numCell(strconv.Itoa(a.Count)))
		p.table.SetCell(r, 3, numCell(format.Duration(a.AvgDuration)))
		p.table.SetCell(r, 4, numCell(fmt.Sprintf("%.0f%%", a.SuccessRate()*100)))
	}
}
