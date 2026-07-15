package main

import (
	"os"
	"sync"
)

// exitWithReceipt is the only process-exit path used after main has installed
// the session receipt. os.Exit skips deferred functions, so finalize must run
// explicitly for command failures that occur after an outbound request.
var (
	exitMu             sync.RWMutex
	finalizeBeforeExit func()
	processExit        = os.Exit
)

func configureExitFinalizer(finalize func()) {
	exitMu.Lock()
	finalizeBeforeExit = finalize
	exitMu.Unlock()
}

func exitWithReceipt(code int) {
	exitMu.RLock()
	finalize := finalizeBeforeExit
	exitMu.RUnlock()
	if finalize != nil {
		finalize()
	}
	processExit(code)
}
