package ui

import (
	"strings"
	"testing"
)

// TestCurrentShowsPipelineIDsNotJobIDs checks both Current panels lead with the
// pipeline's id — the number the team quotes as a build number — on pipeline
// rows, roots and downstream children alike, and leave it blank on job rows,
// where a column of job ids would only be noise.
func TestCurrentShowsPipelineIDsNotJobIDs(t *testing.T) {
	d := newSampleDashboard()
	out := renderToText(t, d, 130, 32)
	t.Logf("Current tab:\n%s", out)

	tree := d.current.table
	if got := tree.GetCell(0, curColID).Text; got != "ID" {
		t.Errorf("tree header column %d = %q, want ID", curColID, got)
	}
	// Sample rows: 1 root pipeline 101, 2–4 its jobs, 5 the child pipeline 201
	// (marked ↳), 6 the child's job, 7 the next root, 102.
	want := map[int]string{1: "101", 2: "", 3: "", 4: "", 5: "201", 6: "", 7: "102"}
	for row, id := range want {
		if got := tree.GetCell(row, curColID).Text; got != id {
			t.Errorf("tree row %d ID = %q, want %q", row, got, id)
		}
	}
	if !strings.Contains(tree.GetCell(5, curColName).Text, "↳") {
		t.Errorf("row 5 should be the ↳ child pipeline; the row map above is stale")
	}

	fin := d.current.finished.table
	if got := fin.GetCell(0, 0).Text; got != "ID" {
		t.Errorf("finished header column 0 = %q, want ID", got)
	}
	if got := fin.GetCell(1, 0).Text; got != "98" { // newest finished: pipeline 98
		t.Errorf("finished row 1 ID = %q, want 98", got)
	}

	// No job id anywhere on the tab, even though every sample job has one.
	for _, jobID := range []string{"5111", "5112", "5113", "5201", "5102"} {
		if strings.Contains(out, jobID) {
			t.Errorf("job id %q is rendered; job rows should show no id", jobID)
		}
	}
}
