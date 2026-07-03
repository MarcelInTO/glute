package ui

import (
	"fmt"
	"strconv"

	"github.com/MarcelInTO/glute/internal/format"
	"github.com/MarcelInTO/glute/internal/gitlab"
	"github.com/rivo/tview"
)

// jobView is the Jobs tab, mirroring the Pipelines layout but focused on
// individual jobs.
type jobView struct {
	root    *tview.Flex
	running *panelTable
	recent  *panelTable
	top     *panelTable
}

func newJobView() *jobView {
	running := newPanelTable("Running jobs")
	recent := newPanelTable("Recent · failures & successes")
	top := newPanelTable("Top jobs · avg length")

	bottom := tview.NewFlex().SetDirection(tview.FlexColumn)
	bottom.AddItem(recent.table, 0, 1, false)
	bottom.AddItem(top.table, 0, 1, false)

	root := tview.NewFlex().SetDirection(tview.FlexRow)
	root.AddItem(running.table, 0, 1, false)
	root.AddItem(bottom, 0, 1, false)

	return &jobView{root: root, running: running, recent: recent, top: top}
}

func (v *jobView) update(s gitlab.Snapshot) {
	fillRunningJobs(v.running, s.RunningJobs)
	fillRecentJobs(v.recent, s.RecentJobs)
	fillTopJobs(v.top, s.TopJobs)
}

// hoverPathAt returns the full project path under (x, y) across this tab's
// panels, if any.
func (v *jobView) hoverPathAt(x, y int) (string, bool) {
	for _, p := range []*panelTable{v.running, v.recent, v.top} {
		if path, ok := p.hoverAt(x, y); ok {
			return path, true
		}
	}
	return "", false
}

func fillRunningJobs(p *panelTable, jobs []gitlab.Job) {
	p.reset("PROJECT", "JOB", "STATUS", "ELAPSED")
	if len(jobs) == 0 {
		emptyRow(p.table, 4)
		return
	}
	for i, j := range jobs {
		r := i + 1
		p.addPath(j.ProjectPath)
		p.table.SetCell(r, 0, textCell(format.Trunc(format.Base(j.ProjectPath), 28)))
		p.table.SetCell(r, 1, textCell(format.Trunc(j.Name, 22)))
		p.table.SetCell(r, 2, statusCell(j.Status))
		p.table.SetCell(r, 3, numCell(format.Elapsed(j.Started, j.Created)))
	}
}

func fillRecentJobs(p *panelTable, jobs []gitlab.Job) {
	p.reset("PROJECT", "JOB", "STATUS", "DURATION", "WHEN")
	if len(jobs) == 0 {
		emptyRow(p.table, 5)
		return
	}
	for i, j := range jobs {
		r := i + 1
		p.addPath(j.ProjectPath)
		p.table.SetCell(r, 0, textCell(format.Trunc(format.Base(j.ProjectPath), 18)))
		p.table.SetCell(r, 1, textCell(format.Trunc(j.Name, 15)))
		p.table.SetCell(r, 2, statusCell(j.Status))
		p.table.SetCell(r, 3, numCell(format.Duration(j.Duration)))
		p.table.SetCell(r, 4, numCell(format.Ago(j.Finished)))
	}
}

func fillTopJobs(p *panelTable, aggs []gitlab.JobAgg) {
	p.reset("PROJECT", "JOB", "RUNS", "AVG", "SUCCESS")
	if len(aggs) == 0 {
		emptyRow(p.table, 5)
		return
	}
	for i, a := range aggs {
		r := i + 1
		p.addPath(a.ProjectPath)
		p.table.SetCell(r, 0, textCell(format.Trunc(format.Base(a.ProjectPath), 18)))
		p.table.SetCell(r, 1, textCell(format.Trunc(a.Name, 15)))
		p.table.SetCell(r, 2, numCell(strconv.Itoa(a.Count)))
		p.table.SetCell(r, 3, numCell(format.Duration(a.AvgDuration)))
		p.table.SetCell(r, 4, numCell(fmt.Sprintf("%.0f%%", a.SuccessRate()*100)))
	}
}
