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

// Compute renders a summed runner-time total compactly, trading precision for
// width as the magnitude grows: seconds ("15s") and minutes ("42m") below an
// hour, one decimal hour up to ten hours ("1.5h", "9.9h"), then whole hours
// ("212h") where the dropped minutes are negligible. Non-positive → em dash.
func Compute(d time.Duration) string {
	switch {
	case d <= 0:
		return "—"
	case d >= 10*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	case d >= time.Hour:
		return fmt.Sprintf("%.1fh", d.Hours())
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	default:
		return fmt.Sprintf("%ds", int(d/time.Second))
	}
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

// ElapsedSince returns how long since work began, preferring started over
// created, clamped at zero. It returns zero when neither time is known.
func ElapsedSince(started, created time.Time) time.Duration {
	t := started
	if t.IsZero() {
		t = created
	}
	if t.IsZero() {
		return 0
	}
	return max(time.Since(t), 0)
}

// HMS renders a duration as hh:mm:ss, blanking any leading zero fields (and
// their colons) to spaces so a column of these lines up on the right. Every
// value is 8 characters wide (e.g. " 1:02:03", "    5:09", "      42"). A
// non-positive duration renders as an em dash.
func HMS(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	total := int(d / time.Second)
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%2d:%02d:%02d", h, m, s)
	case m > 0:
		return fmt.Sprintf("   %2d:%02d", m, s)
	default:
		return fmt.Sprintf("      %2d", s)
	}
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
