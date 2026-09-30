package ui

import (
	"os"
	"os/exec"
	"runtime"
)

// Opening a pipeline or job in GitLab. Two mechanisms cover each other's gaps:
//
//   - The id cells are terminal hyperlinks (OSC 8, see linkCell), which the
//     user's own terminal opens — on their own machine, even when glute runs
//     over SSH on a headless box. But glute captures the mouse, so most
//     terminals only take a *modifier*-click as theirs (Cmd-, Ctrl-, or
//     Shift-click, depending on the terminal), and some don't do hyperlinks.
//   - A plain click on an id, or `o` on the selected row, has glute launch the
//     system's default browser itself (openInBrowser) — which works in any
//     terminal, but only where glute runs on the desktop the user is sitting
//     at. Where it can't be (browserUnreachable), glute shows the URL instead
//     and points at the terminal's own link.

// openInBrowser hands url to the platform's default-browser opener. It runs
// the opener with no stdio attached — the terminal is in raw mode under the
// TUI, and an opener that prints (xdg-open's helpers often do) would scribble
// over the screen — and doesn't wait for it, since a browser it starts can
// outlive glute. It returns an error only if the opener couldn't be started;
// whether a browser then appears is up to the desktop.
func openInBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		// Not `cmd /c start`, whose quoting mangles the & in a query string.
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // reap it whenever it exits
	return nil
}

// browserUnreachable says why a browser glute launched wouldn't reach the
// user, or "" if it would. Over SSH it would open on the remote machine (or
// fail there), not on the user's desktop; on Linux and the BSDs, with neither
// an X nor a Wayland display, there's no desktop to open it on at all. macOS
// and Windows always have one.
func browserUnreachable() string {
	for _, v := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
		if os.Getenv(v) != "" {
			return "over SSH"
		}
	}
	switch runtime.GOOS {
	case "darwin", "windows":
		return ""
	}
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return "no desktop session"
	}
	return ""
}
