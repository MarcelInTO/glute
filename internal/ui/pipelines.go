package ui

import (
	"fmt"
	"strconv"

	"github.com/MarcelInTO/glute/internal/format"
	"github.com/MarcelInTO/glute/internal/gitlab"
	"github.com/rivo/tview"
)

// pipelineView is the Pipelines tab: a full-width "running" panel above a row
// split between "recent" and "top".
type pipelineView struct {
	root    *tview.Flex
	running *tview.Table
	recent  *tview.Table
	top     *tview.Table
}

func newPipelineView() *pipelineView {
	running := newTable("Running pipelines")
	recent := newTable("Recent · failures & successes")
	top := newTable("Top pipelines · avg length")

	bottom := tview.NewFlex().SetDirection(tview.FlexColumn)
	bottom.AddItem(recent, 0, 1, false)
	bottom.AddItem(top, 0, 1, false)

	root := tview.NewFlex().SetDirection(tview.FlexRow)
	root.AddItem(running, 0, 1, false)
	root.AddItem(bottom, 0, 1, false)

	return &pipelineView{root: root, running: running, recent: recent, top: top}
}

func (v *pipelineView) update(s gitlab.Snapshot) {
	fillRunningPipelines(v.running, s.RunningPipelines)
	fillRecentPipelines(v.recent, s.RecentPipelines)
	fillTopPipelines(v.top, s.TopPipelines)
}

func fillRunningPipelines(t *tview.Table, pipes []gitlab.Pipeline) {
	setHeader(t, "PROJECT", "REF", "STATUS", "ELAPSED")
	if len(pipes) == 0 {
		emptyRow(t, 4)
		return
	}
	for i, p := range pipes {
		r := i + 1
		t.SetCell(r, 0, textCell(format.Trunc(p.ProjectPath, 34)))
		t.SetCell(r, 1, textCell(format.Trunc(p.Ref, 22)))
		t.SetCell(r, 2, statusCell(p.Status))
		t.SetCell(r, 3, numCell(format.Elapsed(p.Started, p.Created)))
	}
}

func fillRecentPipelines(t *tview.Table, pipes []gitlab.Pipeline) {
	setHeader(t, "PROJECT", "REF", "STATUS", "DURATION", "WHEN")
	if len(pipes) == 0 {
		emptyRow(t, 5)
		return
	}
	for i, p := range pipes {
		r := i + 1
		t.SetCell(r, 0, textCell(format.Trunc(p.ProjectPath, 24)))
		t.SetCell(r, 1, textCell(format.Trunc(p.Ref, 15)))
		t.SetCell(r, 2, statusCell(p.Status))
		t.SetCell(r, 3, numCell(format.Duration(p.Duration)))
		t.SetCell(r, 4, numCell(format.Ago(p.Finished)))
	}
}

func fillTopPipelines(t *tview.Table, aggs []gitlab.PipelineAgg) {
	setHeader(t, "PROJECT", "REF", "RUNS", "AVG", "SUCCESS")
	if len(aggs) == 0 {
		emptyRow(t, 5)
		return
	}
	for i, a := range aggs {
		r := i + 1
		t.SetCell(r, 0, textCell(format.Trunc(a.ProjectPath, 26)))
		t.SetCell(r, 1, textCell(format.Trunc(a.Ref, 16)))
		t.SetCell(r, 2, numCell(strconv.Itoa(a.Count)))
		t.SetCell(r, 3, numCell(format.Duration(a.AvgDuration)))
		t.SetCell(r, 4, numCell(fmt.Sprintf("%.0f%%", a.SuccessRate()*100)))
	}
}
