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
