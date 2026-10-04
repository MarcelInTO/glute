package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/MarcelInTO/glute/internal/gitlab"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// TestFailingPanelListsRefs checks the unresolved-failures panel sits beside
// the finished list and shows each failing ref and for how long it has been
// failing, most recent failure first.
func TestFailingPanelListsRefs(t *testing.T) {
	d, _ := newTreeDashboard()
	out := renderToText(t, d, 120, 30)
	t.Logf("Current tab:\n%s", out)

	if !strings.Contains(out, "Unresolved failures · u to select") {
		t.Errorf("no unresolved-failures panel")
	}
	fin, fail := d.current.finished.table, d.current.failing.table
	fx, fy, fw, _ := fin.GetRect()
	ux, uy, uw, _ := fail.GetRect()
	if uy != fy || ux != fx+fw || uw >= fw {
		t.Errorf("failures panel at x=%d y=%d w=%d; want beside the finished list (x=%d y=%d) and narrower than its %d",
			ux, uy, uw, fx+fw, fy, fw)
	}
	want := [][3]string{
		{"gateway", "feat/rate-limit", "38m"},
		{"nightly-backup", "main", "4d"}, // failing 4 days, failed again 2 days ago
		{"web", "feat/old-checkout", "12d"},
		{"reports", "main", "26d"},
	}
	for i, w := range want {
		for c := range w {
			cell := fail.GetCell(i+1, c)
			got := cell.Text
			if full, ok := cell.Reference.(string); ok {
				got = full // a name column's full text, whatever fits
			}
			if got != w[c] {
				t.Errorf("row %d column %d = %q, want %q", i+1, c, got, w[c])
			}
		}
	}
}

// TestFailingPanelEmpty shows "(none)" when nothing is failing, and u then
// leaves the keyboard on the tree.
func TestFailingPanelEmpty(t *testing.T) {
	snap := gitlab.SampleSnapshot()
	snap.FailingRefs = nil
	d := newDashboardShowing(gitlab.FakeService{Snap: snap}, snap)
	if out := renderToText(t, d, 120, 30); !strings.Contains(out, "(none)") {
		t.Errorf("an empty failures panel should say (none):\n%s", out)
	}
	pressKey(d, 'u')
	if d.current.keyboard != curTree || d.app.GetFocus() != d.current.table {
		t.Errorf("u with nothing failing moved the keyboard to %v", d.current.keyboard)
	}
}

// TestFailingKeys checks u hands the keyboard to the failures list and back,
// f moves across to the finished list, only the list with the keyboard is
// highlighted, Enter opens the selected ref's latest failure, and Esc comes
// back to the list.
func TestFailingKeys(t *testing.T) {
	d, _ := newTreeDashboard()
	tree, fin, fail := d.current.table, d.current.finished.table, d.current.failing.table
	selectable := func(tb *tview.Table) bool { rows, _ := tb.GetSelectable(); return rows }

	pressKey(d, 'u')
	if d.app.GetFocus() != fail || !selectable(fail) || selectable(tree) || selectable(fin) {
		t.Fatalf("after u the failures list should have focus and the only highlight")
	}
	if !strings.Contains(fail.GetTitle(), "u back") {
		t.Errorf("focused title %q should say how to get back", fail.GetTitle())
	}
	if got := d.footer.GetText(true); !strings.Contains(got, "acme/platform/gateway") {
		t.Errorf("footer should reveal the selected project's path, got %q", got)
	}
	if got := d.current.selectedURL(); !strings.HasSuffix(got, "/acme/platform/gateway/-/pipelines/97") {
		t.Errorf("o would open %q, want the latest failure's page", got)
	}

	fail.InputHandler()(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), func(tview.Primitive) {})
	fail.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(tview.Primitive) {})
	if !d.detailOpen() {
		t.Fatal("Enter on a failing ref should open the detail view")
	}
	if out := renderToText(t, d, 120, 30); !strings.Contains(out, "Pipeline #94 · nightly-backup") {
		t.Errorf("Enter should open nightly-backup's latest failure, #94:\n%s", out)
	}
	pressSpecial(d, tcell.KeyEscape)
	if d.detailOpen() || d.app.GetFocus() != fail {
		t.Fatal("Esc should close the view and return focus to the failures list")
	}

	pressKey(d, 'f')
	if d.app.GetFocus() != fin || !selectable(fin) || selectable(fail) {
		t.Fatalf("f from the failures list should move to the finished list")
	}
	pressKey(d, 'u')
	pressKey(d, 'u')
	if d.app.GetFocus() != tree || !selectable(tree) || selectable(fail) {
		t.Fatalf("u on the failures list should hand the keyboard back to the tree")
	}
}

// TestFailingSelectionFollowsRef checks the selection stays on its ref (a
// project and a ref, not the project alone) when a refresh brings another ref
// in on top, and when its own ref fails again and jumps to the top; and that
// when its ref recovers, it stays near where it was.
func TestFailingSelectionFollowsRef(t *testing.T) {
	d, _ := newTreeDashboard()
	pressKey(d, 'u')
	_ = renderToText(t, d, 120, 30)
	fail := d.current.failing.table
	fail.Select(3, 0) // web feat/old-checkout
	selected := func() string {
		row, _ := fail.GetSelection()
		f, _ := d.current.failingAt(row)
		return f.ProjectPath + " " + f.Ref
	}
	snap := d.snapshot
	refresh := func(refs ...gitlab.FailingRef) {
		snap.FailingRefs = refs
		d.current.update(snap)
		_ = renderToText(t, d, 120, 30)
	}
	base := snap.FailingRefs

	// Another ref of the same project fails: it lands on top, and the
	// selection stays on feat/old-checkout, not on web's other row.
	fresh := gitlab.FailingRef{ProjectPath: "acme/payments/web", Ref: "main", Since: time.Now()}
	refresh(append([]gitlab.FailingRef{fresh}, base...)...)
	if got := selected(); got != "acme/payments/web feat/old-checkout" {
		t.Errorf("after a ref was added on top, the selection is on %q", got)
	}
	if off, _ := fail.GetOffset(); off != 0 {
		t.Errorf("the view left the top (offset %d), hiding the new arrival", off)
	}

	// feat/old-checkout fails again: it moves to the top, and so does the selection.
	again := base[2]
	refresh(again, fresh, base[0], base[1], base[3])
	if row, _ := fail.GetSelection(); selected() != "acme/payments/web feat/old-checkout" || row != 1 {
		t.Errorf("after its ref failed again, the selection is on %q (row %d), want row 1", selected(), row)
	}

	// It recovers: the selection stays on the row it was on.
	refresh(fresh, base[0], base[1], base[3])
	if got := selected(); got != "acme/payments/web main" {
		t.Errorf("after the selected ref recovered, the selection is on %q, want the row that took its place", got)
	}
}

// TestClickOpensFailingRef clicks the third failing ref: it opens its latest
// failure, with the failures list holding the keyboard.
func TestClickOpensFailingRef(t *testing.T) {
	d, _ := newTreeDashboard()
	_ = renderToText(t, d, 120, 30)
	fail := d.current.failing.table
	x, y, _, _ := fail.GetInnerRect()
	mouse := func(a tview.MouseAction) {
		d.onMouse(tcell.NewEventMouse(x+2, y+3, tcell.Button1, tcell.ModNone), a)
	}
	mouse(tview.MouseLeftDown)
	mouse(tview.MouseLeftClick)
	if !d.detailOpen() {
		t.Fatal("clicking a failing ref should open the detail view")
	}
	if out := renderToText(t, d, 120, 30); !strings.Contains(out, "Pipeline #70 · web · feat/old-checkout") {
		t.Errorf("the click should open feat/old-checkout's latest failure, #70:\n%s", out)
	}
	if row, _ := fail.GetSelection(); row != 3 || d.current.keyboard != curFailing {
		t.Errorf("the click should select row 3 and give the list the keyboard, got row %d, keyboard %v", row, d.current.keyboard)
	}
}

// TestFailingWidth: three eighths of the row, at least wide enough for the panel's
// focused title, at most what three columns can use, and never over half.
func TestFailingWidth(t *testing.T) {
	if n := len([]rune(" " + failingTitle(true) + " ")); n+2 > failingWidth(120) {
		t.Errorf("the focused title needs %d columns with its border, the panel has %d at 120", n+2, failingWidth(120))
	}
	for w, want := range map[int]int{60: 30, 100: 40, 120: 45, 140: 52, 200: 56} {
		if got := failingWidth(w); got != want {
			t.Errorf("failingWidth(%d) = %d, want %d", w, got, want)
		}
	}
}
