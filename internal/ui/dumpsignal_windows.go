//go:build windows

package ui

import "os"

// dumpSignals is empty on Windows, which has no spare app-defined signal like
// SIGUSR1; watchDumpSignal becomes a no-op there.
var dumpSignals []os.Signal
