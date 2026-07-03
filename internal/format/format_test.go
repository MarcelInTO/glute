package format

import "testing"

func TestBase(t *testing.T) {
	cases := map[string]string{
		"org/team/repo":      "repo",
		"fread/fread-ingest": "fread-ingest",
		"solo":               "solo",
		"":                   "",
		"a/b/":               "",
	}
	for in, want := range cases {
		if got := Base(in); got != want {
			t.Errorf("Base(%q) = %q, want %q", in, got, want)
		}
	}
}
