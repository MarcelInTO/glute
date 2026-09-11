package format

import (
	"testing"
	"time"
)

func TestHMS(t *testing.T) {
	cases := map[time.Duration]string{
		0:                             "—",
		-5 * time.Second:              "—",
		5 * time.Second:               "       5",
		42 * time.Second:              "      42",
		90 * time.Second:              "    1:30",
		9*time.Minute + 5*time.Second: "    9:05",
		62 * time.Minute:              " 1:02:00",
		time.Hour + 2*time.Minute + 3*time.Second:      " 1:02:03",
		12*time.Hour + 34*time.Minute + 56*time.Second: "12:34:56",
		// Sub-second remainders truncate toward the second.
		90*time.Second + 900*time.Millisecond: "    1:30",
	}
	for in, want := range cases {
		if got := HMS(in); got != want {
			t.Errorf("HMS(%v) = %q, want %q", in, got, want)
		}
	}
	// Every non-empty value is a fixed 8 columns wide so the column lines up.
	for _, d := range []time.Duration{5 * time.Second, 90 * time.Second, 12*time.Hour + 34*time.Minute + 56*time.Second} {
		if got := HMS(d); len([]rune(got)) != 8 {
			t.Errorf("HMS(%v) = %q, width %d, want 8", d, got, len([]rune(got)))
		}
	}
}

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

func TestWindow(t *testing.T) {
	cases := map[time.Duration]string{
		24 * time.Hour:      "1d",
		7 * 24 * time.Hour:  "7d",
		30 * 24 * time.Hour: "30d",
		36 * time.Hour:      "36h",
		90 * time.Minute:    "1h30m0s",
		0:                   "—",
	}
	for in, want := range cases {
		if got := Window(in); got != want {
			t.Errorf("Window(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestElide(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		// Short enough to keep whole — no ellipsis, at the boundary either.
		{"repo", 10, "repo"},
		{"exactlyten", 10, "exactlyten"},
		// The point of the helper: a shared long prefix stays distinguishable,
		// because the tail (which a plain Trunc would drop) survives.
		{"bigproduct-service-frontend", 16, "bigprod…frontend"},
		{"bigproduct-service-backend", 16, "bigprod…-backend"},
		// Odd budgets give the extra rune to the tail.
		{"abcdefghij", 6, "ab…hij"},
		{"abcdefghij", 7, "abc…hij"},
		// Degenerate widths.
		{"abcdefghij", 2, "…j"},
		{"abcdefghij", 1, "…"},
		{"abcdefghij", 0, ""},
		{"abcdefghij", -3, ""},
		// The budget counts runes, not bytes, so multi-byte names aren't cut short.
		{"héllo-wörld", 8, "hél…örld"},
	}
	for _, c := range cases {
		if got := Elide(c.in, c.n); got != c.want {
			t.Errorf("Elide(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
		if n := len([]rune(Elide(c.in, c.n))); c.n > 0 && n > c.n {
			t.Errorf("Elide(%q, %d) = %q: %d runes, over budget", c.in, c.n, Elide(c.in, c.n), n)
		}
	}
}
