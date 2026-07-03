// Package format holds small display helpers shared by the CLI and the TUI.
package format

import (
	"fmt"
	"strings"
	"time"
)

// Duration renders a duration rounded to the second, or an em dash when unknown.
func Duration(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	return d.Round(time.Second).String()
}

// Ago renders how long ago t was, coarsely (s/m/h/d), or "?" for the zero time.
func Ago(t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	d := max(time.Since(t), 0)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// Elapsed renders how long ago work started, preferring started over created.
func Elapsed(started, created time.Time) string {
	if !started.IsZero() {
		return Ago(started)
	}
	return Ago(created)
}

// Trunc shortens s to at most n runes, adding an ellipsis when it cuts.
func Trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

// Base returns the last segment of a slash-separated path (e.g. "org/team/repo"
// -> "repo"); paths without a slash are returned unchanged.
func Base(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
