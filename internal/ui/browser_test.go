package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/MarcelInTO/glute/internal/gitlab"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// linkDashboard is the sample dashboard with the browser stood in for: every
// URL glute would launch lands in *opened instead, and unreachable says what
// the machine is like (as browserUnreachable would).
func linkDashboard(t *testing.T, unreachable string) (*Dashboard, *[]string) {
	t.Helper()
	d, _ := newTreeDashboard()
	var opened []string
	d.opener = func(url string) error { opened = append(opened, url); return nil }
	d.unreachable = func() string { return unreachable }
	return d, &opened
}

// TestIDCellsAreLinks checks every pipeline id — tree roots and children, the
// finished panel, the detail view — is an underlined terminal hyperlink to the
// pipeline's page, and job rows (which show no id) carry none.
func TestIDCellsAreLinks(t *testing.T) {
	d, trees := newTreeDashboard()
	want := "https://gitlab.example.com/acme/payments/api/-/pipelines/101"
	cell := d.current.table.GetCell(1, curColID)
	if l, ok := cell.Reference.(link); !ok || l.url != want {
		t.Errorf("tree root id links to %+v, want %s", cell.Reference, want)
	}
	if _, _, attr := cell.Style.Decompose(); attr&tcell.AttrUnderline == 0 {
		t.Error("a linked id should be underlined")
	}
	if cell.Style != curNumCell("").Style.Underline(true).Url(want) {
		t.Error("the id's style should carry the URL, for the terminal's OSC 8 link")
	}
	if _, ok := d.current.table.GetCell(5, curColID).Reference.(link); !ok {
		t.Error("the ↳ child pipeline's id should link too")
	}
	if _, ok := d.current.table.GetCell(2, curColID).Reference.(link); ok {
		t.Error("a job row shows no id, so it should carry no link")
	}
	if l, ok := d.current.finished.table.GetCell(2, 0).Reference.(link); !ok || !strings.HasSuffix(l.url, "/pipelines/97") {
		t.Errorf("finished row 2 id links to %+v, want pipeline 97", d.current.finished.table.GetCell(2, 0).Reference)
	}
	p := d.snapshot.RecentPipelines[0]
	d.openDetail(p)
	d.detail.show(trees[p.ID], nil)
	if l, ok := d.detail.table.GetCell(1, detColID).Reference.(link); !ok || !strings.HasSuffix(l.url, "/pipelines/98") {
		t.Errorf("detail root id links to %+v, want pipeline 98", d.detail.table.GetCell(1, detColID).Reference)
	}
}

// TestClickOnIDOpensBrowser: a plain click on an id opens that pipeline in the
// browser — on the tree, and on the finished panel, where it opens the
// browser *instead of* the detail view; a click elsewhere on the finished row
// still opens the detail view.
func TestClickOnIDOpensBrowser(t *testing.T) {
	d, opened := linkDashboard(t, "")
	_ = renderToText(t, d, 130, 32)
	click := func(x, y int) {
		d.onMouse(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone), tview.MouseLeftClick)
	}
	tx, ty, _, _ := d.current.table.GetInnerRect()
	click(tx+1, ty+1) // the tree's first row, its id "101"
	fx, fy, _, _ := d.current.finished.table.GetInnerRect()
	click(fx, fy+2) // finished row 2's id, "97"
	if len(*opened) != 2 || !strings.HasSuffix((*opened)[0], "/pipelines/101") || !strings.HasSuffix((*opened)[1], "/pipelines/97") {
		t.Fatalf("opened %v, want pipelines 101 then 97", *opened)
	}
	if d.detailOpen() {
		t.Error("a click on a finished id should open the browser, not the detail view")
	}
	if !strings.Contains(d.footer.GetText(true), "opened in your browser") {
		t.Errorf("footer should say the page was opened, got %q", d.footer.GetText(true))
	}
	click(fx+10, fy+2) // the same row's project name
	if !d.detailOpen() || len(*opened) != 2 {
		t.Errorf("a click elsewhere on the row should open the detail view, not the browser")
	}
}

// TestOKeyOpensSelectedRow: `o` opens the selected row's page — a pipeline's
// on a pipeline row, the job's own on a job row — in the tree, the finished
// list and the detail view alike.
func TestOKeyOpensSelectedRow(t *testing.T) {
	d, opened := linkDashboard(t, "")
	pressKey(d, 'o') // tree row 1: pipeline 101
	d.current.table.Select(2, 0)
	pressKey(d, 'o') // tree row 2: job 5111 (compile)
	pressKey(d, 'f')
	pressKey(d, 'o') // finished row 1: pipeline 98
	d.current.finished.table.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(tview.Primitive) {})
	d.detail.show(gitlab.SampleTrees(d.snapshot)[98], nil) // what the fetch delivers
	d.detail.table.Select(2, 0)
	pressKey(d, 'o') // detail row 2: job 9801 (compile)
	want := []string{"/pipelines/101", "/-/jobs/5111", "/pipelines/98", "/-/jobs/9801"}
	if len(*opened) != len(want) {
		t.Fatalf("opened %v, want %d pages", *opened, len(want))
	}
	for i, w := range want {
		if !strings.HasSuffix((*opened)[i], w) {
			t.Errorf("open %d = %s, want …%s", i, (*opened)[i], w)
		}
	}
	if !d.detailOpen() {
		t.Error("o in the detail view shouldn't close it")
	}
}

// TestOpenLinkWhereNoBrowserReaches: over SSH (or with no desktop) glute
// launches nothing — the browser would open on the wrong machine — and shows
// the URL with a pointer at the terminal's own link instead.
func TestOpenLinkWhereNoBrowserReaches(t *testing.T) {
	d, opened := linkDashboard(t, "over SSH")
	pressKey(d, 'o')
	if len(*opened) != 0 {
		t.Fatalf("launched %v over SSH", *opened)
	}
	foot := d.footer.GetText(true)
	if !strings.Contains(foot, "/pipelines/101") || !strings.Contains(foot, "over SSH") {
		t.Errorf("footer should show the URL and why it wasn't opened, got %q", foot)
	}
}

func TestOpenLinkReportsFailure(t *testing.T) {
	d, _ := linkDashboard(t, "")
	d.opener = func(string) error { return errors.New(`exec: "xdg-open": executable file not found in $PATH`) }
	pressKey(d, 'o')
	if foot := d.footer.GetText(true); !strings.Contains(foot, "couldn't start a browser") || !strings.Contains(foot, "/pipelines/101") {
		t.Errorf("footer should report the failure with the URL, got %q", foot)
	}
}
