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
	running *tview.Table
	recent  *tview.Table
	top     *tview.Table
}

func newJobView() *jobView {
	running := newTable("Running jobs")
	recent := newTable("Recent · failures & successes")
	top := newTable("Top jobs · avg length")

	bottom := tview.NewFlex().SetDirection(tview.FlexColumn)
	bottom.AddItem(recent, 0, 1, false)
	bottom.AddItem(top, 0, 1, false)

	root := tview.NewFlex().SetDirection(tview.FlexRow)
	root.AddItem(running, 0, 1, false)
	root.AddItem(bottom, 0, 1, false)

	return &jobView{root: root, running: running, recent: recent, top: top}
}

func (v *jobView) update(s gitlab.Snapshot) {
	fillRunningJobs(v.running, s.RunningJobs)
	fillRecentJobs(v.recent, s.RecentJobs)
	fillTopJobs(v.top, s.TopJobs)
}

func fillRunningJobs(t *tview.Table, jobs []gitlab.Job) {
	setHeader(t, "PROJECT", "JOB", "STATUS", "ELAPSED")
	if len(jobs) == 0 {
		emptyRow(t, 4)
		return
	}
	for i, j := range jobs {
		r := i + 1
		t.SetCell(r, 0, textCell(format.Trunc(j.ProjectPath, 34)))
		t.SetCell(r, 1, textCell(format.Trunc(j.Name, 22)))
		t.SetCell(r, 2, statusCell(j.Status))
		t.SetCell(r, 3, numCell(format.Elapsed(j.Started, j.Created)))
	}
}

func fillRecentJobs(t *tview.Table, jobs []gitlab.Job) {
	setHeader(t, "PROJECT", "JOB", "STATUS", "DURATION", "WHEN")
	if len(jobs) == 0 {
		emptyRow(t, 5)
		return
	}
	for i, j := range jobs {
		r := i + 1
		t.SetCell(r, 0, textCell(format.Trunc(j.ProjectPath, 24)))
		t.SetCell(r, 1, textCell(format.Trunc(j.Name, 15)))
		t.SetCell(r, 2, statusCell(j.Status))
		t.SetCell(r, 3, numCell(format.Duration(j.Duration)))
		t.SetCell(r, 4, numCell(format.Ago(j.Finished)))
	}
}

func fillTopJobs(t *tview.Table, aggs []gitlab.JobAgg) {
	setHeader(t, "PROJECT", "JOB", "RUNS", "AVG", "SUCCESS")
	if len(aggs) == 0 {
		emptyRow(t, 5)
		return
	}
	for i, a := range aggs {
		r := i + 1
		t.SetCell(r, 0, textCell(format.Trunc(a.ProjectPath, 26)))
		t.SetCell(r, 1, textCell(format.Trunc(a.Name, 16)))
		t.SetCell(r, 2, numCell(strconv.Itoa(a.Count)))
		t.SetCell(r, 3, numCell(format.Duration(a.AvgDuration)))
		t.SetCell(r, 4, numCell(fmt.Sprintf("%.0f%%", a.SuccessRate()*100)))
	}
}
