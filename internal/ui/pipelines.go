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
	running *panelTable
	recent  *panelTable
	top     *panelTable
}

func newPipelineView() *pipelineView {
	running := newPanelTable("Running pipelines")
	recent := newPanelTable("Recent · failures & successes")
	top := newPanelTable("Top pipelines · avg length")

	bottom := tview.NewFlex().SetDirection(tview.FlexColumn)
	bottom.AddItem(recent.table, 0, 1, false)
	bottom.AddItem(top.table, 0, 1, false)

	root := tview.NewFlex().SetDirection(tview.FlexRow)
	root.AddItem(running.table, 0, 1, false)
	root.AddItem(bottom, 0, 1, false)

	return &pipelineView{root: root, running: running, recent: recent, top: top}
}

func (v *pipelineView) update(s gitlab.Snapshot) {
	fillRunningPipelines(v.running, s.RunningPipelines)
	fillRecentPipelines(v.recent, s.RecentPipelines)
	fillTopPipelines(v.top, s.TopPipelines)
}

// hoverPathAt returns the full project path under (x, y) across this tab's
// panels, if any.
func (v *pipelineView) hoverPathAt(x, y int) (string, bool) {
	for _, p := range []*panelTable{v.running, v.recent, v.top} {
		if path, ok := p.hoverAt(x, y); ok {
			return path, true
		}
	}
	return "", false
}

func fillRunningPipelines(p *panelTable, pipes []gitlab.Pipeline) {
	p.reset("PROJECT", "REF", "STATUS", "ELAPSED")
	if len(pipes) == 0 {
		emptyRow(p.table, 4)
		return
	}
	for i, pipe := range pipes {
		r := i + 1
		p.addPath(pipe.ProjectPath)
		p.table.SetCell(r, 0, textCell(format.Trunc(format.Base(pipe.ProjectPath), 28)))
		p.table.SetCell(r, 1, textCell(format.Trunc(pipe.Ref, 22)))
		p.table.SetCell(r, 2, statusCell(pipe.Status))
		p.table.SetCell(r, 3, numCell(format.Elapsed(pipe.Started, pipe.Created)))
	}
}

func fillRecentPipelines(p *panelTable, pipes []gitlab.Pipeline) {
	p.reset("PROJECT", "REF", "STATUS", "DURATION", "WHEN")
	if len(pipes) == 0 {
		emptyRow(p.table, 5)
		return
	}
	for i, pipe := range pipes {
		r := i + 1
		p.addPath(pipe.ProjectPath)
		p.table.SetCell(r, 0, textCell(format.Trunc(format.Base(pipe.ProjectPath), 18)))
		p.table.SetCell(r, 1, textCell(format.Trunc(pipe.Ref, 15)))
		p.table.SetCell(r, 2, statusCell(pipe.Status))
		p.table.SetCell(r, 3, numCell(format.Duration(pipe.Duration)))
		p.table.SetCell(r, 4, numCell(format.Ago(pipe.Finished)))
	}
}

func fillTopPipelines(p *panelTable, aggs []gitlab.PipelineAgg) {
	p.reset("PROJECT", "REF", "RUNS", "AVG", "SUCCESS")
	if len(aggs) == 0 {
		emptyRow(p.table, 5)
		return
	}
	for i, a := range aggs {
		r := i + 1
		p.addPath(a.ProjectPath)
		p.table.SetCell(r, 0, textCell(format.Trunc(format.Base(a.ProjectPath), 18)))
		p.table.SetCell(r, 1, textCell(format.Trunc(a.Ref, 15)))
		p.table.SetCell(r, 2, numCell(strconv.Itoa(a.Count)))
		p.table.SetCell(r, 3, numCell(format.Duration(a.AvgDuration)))
		p.table.SetCell(r, 4, numCell(fmt.Sprintf("%.0f%%", a.SuccessRate()*100)))
	}
}
