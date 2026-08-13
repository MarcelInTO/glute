package ui

import (
	"strconv"

	"github.com/MarcelInTO/glute/internal/format"
	"github.com/MarcelInTO/glute/internal/gitlab"
	"github.com/rivo/tview"
)

// infraView is the Infrastructure tab: how the CI *capacity* behaved over the
// Top window (default 30d), as opposed to the Work tab's per-project view of the
// work that ran on it. Its two panels are two keyings of the same job
// population — by the runner tags jobs asked for, and by the runner that
// answered — so they sit side by side and are read against each other: a tag
// whose queue wait dwarfs its runners' points at capacity that's under-provided.
type infraView struct {
	root    *tview.Flex
	tags    *panelTable
	runners *panelTable
	aliases map[string]string // runner full name → short display label
}

func newInfraView(aliases map[string]string) *infraView {
	tags := newPanelTable("Tag performance · last 30d")
	runners := newPanelTable("Runner performance · last 30d")

	root := tview.NewFlex().SetDirection(tview.FlexColumn)
	root.AddItem(tags.table, 0, 1, false)
	root.AddItem(runners.table, 0, 1, false)

	return &infraView{root: root, tags: tags, runners: runners, aliases: aliases}
}

func (v *infraView) update(s gitlab.Snapshot) {
	fillJobStatsPanel(v.tags, "TAG", s.TagStats, func(tag string) string { return tag })
	fillJobStatsPanel(v.runners, "RUNNER", s.RunnerStats, v.displayRunner)
}

// displayRunner maps a runner's full name to its configured short label, or
// returns the name unchanged when no alias is set (mirrors the Current tab).
func (v *infraView) displayRunner(runner string) string {
	if short, ok := v.aliases[runner]; ok {
		return short
	}
	return runner
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
