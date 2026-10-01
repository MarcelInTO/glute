package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// liveTimeout bounds every wait on the event loop. A loop stuck in a key
// handler never answers, so a wait that runs out is the failure being tested.
const liveTimeout = 3 * time.Second

// liveApp runs a dashboard's real event loop on an in-memory screen, so keys
// go through tview's own dispatch (the app's input capture, the pages, the
// focused widget) instead of straight to onKey.
type liveApp struct {
	t      *testing.T
	d      *Dashboard
	screen tcell.SimulationScreen
	done   chan error
}

func startLive(t *testing.T, d *Dashboard) *liveApp {
	t.Helper()
	screen := tcell.NewSimulationScreen("")
	d.app.SetScreen(screen) // initializes it
	screen.SetSize(120, 30)
	l := &liveApp{t: t, d: d, screen: screen, done: make(chan error, 1)}
	go func() { l.done <- d.app.Run() }()
	return l
}

// do runs f on the event loop and waits for it.
func (l *liveApp) do(f func()) {
	l.t.Helper()
	ran := make(chan struct{})
	go l.d.app.QueueUpdate(func() { f(); close(ran) })
	select {
	case <-ran:
	case err := <-l.done:
		l.t.Fatalf("glute stopped (Run returned %v) before it ran a queued update", err)
	case <-time.After(liveTimeout):
		l.t.Fatal("the event loop is stuck: it never ran a queued update")
	}
}

// waitFor waits until the screen shows want. Keys and queued updates reach the
// loop by different channels, so a check after a key has to poll.
func (l *liveApp) waitFor(want string) {
	l.t.Helper()
	deadline := time.Now().Add(liveTimeout)
	for {
		var b strings.Builder
		l.do(func() { // on the loop, so it can't race a draw
			cells, w, _ := l.screen.GetContents()
			for i, c := range cells {
				if len(c.Runes) == 0 || c.Runes[0] == 0 {
					b.WriteByte(' ')
				} else {
					b.WriteRune(c.Runes[0])
				}
				if (i+1)%w == 0 {
					b.WriteByte('\n')
				}
			}
		})
		if strings.Contains(b.String(), want) {
			return
		}
		if time.Now().After(deadline) {
			l.t.Fatalf("the screen never showed %q:\n%s", want, b.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// quit presses Ctrl-C and waits for the loop to stop. Ctrl-C is queued behind
// every key pressed before it, so a key handler that never returns fails here.
func (l *liveApp) quit() {
	l.t.Helper()
	l.screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	select {
	case err := <-l.done:
		if err != nil {
			l.t.Fatalf("Run: %v", err)
		}
	case <-time.After(liveTimeout):
		l.t.Fatal("Ctrl-C didn't stop glute: the event loop is stuck in a key handler")
	}
}
