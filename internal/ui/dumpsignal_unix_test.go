//go:build !windows

package ui

import (
	"bytes"
	"context"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// syncBuf is an io.Writer safe for the concurrent writes the log package makes
// from the watchDumpSignal goroutine while the test reads it.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func TestDumpGoroutinesLogsStacks(t *testing.T) {
	var sb syncBuf
	old := log.Writer()
	log.SetOutput(&sb)
	defer log.SetOutput(old)

	dumpGoroutines(syscall.SIGUSR1)

	out := sb.String()
	for _, want := range []string{"goroutine dump", "goroutine ", "end goroutine dump"} {
		if !strings.Contains(out, want) {
			t.Errorf("dump missing %q; got:\n%s", want, out)
		}
	}
}

// TestWatchDumpSignalRespondsToSIGUSR1 proves the real signal path: a SIGUSR1
// sent to this process makes watchDumpSignal write a goroutine dump to the log,
// which is exactly how `kill -USR1 <pid>` diagnoses a wedged glute.
func TestWatchDumpSignalRespondsToSIGUSR1(t *testing.T) {
	var sb syncBuf
	old := log.Writer()
	log.SetOutput(&sb)
	defer log.SetOutput(old)

	// Register a guard for SIGUSR1 *first* so its default action (terminate the
	// process) is disarmed for the whole test — no send below can kill us.
	guard := make(chan os.Signal, 1)
	signal.Notify(guard, syscall.SIGUSR1)
	defer signal.Stop(guard)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watchDumpSignal(ctx)

	// Resend on each poll so the test doesn't depend on watchDumpSignal having
	// registered before the first signal (a lost signal isn't queued).
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
			t.Fatalf("kill: %v", err)
		}
		time.Sleep(30 * time.Millisecond)
		if strings.Contains(sb.String(), "goroutine dump") {
			return // watchDumpSignal caught the signal and dumped
		}
	}
	t.Fatalf("watchDumpSignal did not log a dump within 3s; got:\n%s", sb.String())
}
