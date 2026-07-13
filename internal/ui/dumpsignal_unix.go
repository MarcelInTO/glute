//go:build !windows

package ui

import (
	"os"
	"syscall"
)

// dumpSignals are the signals that trigger a goroutine dump to the log. SIGUSR1
// is a spare, app-defined signal on Unix — safe to reserve for diagnostics.
var dumpSignals = []os.Signal{syscall.SIGUSR1}
