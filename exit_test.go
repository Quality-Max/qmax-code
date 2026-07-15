package main

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestExitWithReceiptFinalizesBeforeProcessExit(t *testing.T) {
	exitMu.Lock()
	oldFinalize := finalizeBeforeExit
	oldProcessExit := processExit
	exitMu.Unlock()
	t.Cleanup(func() {
		exitMu.Lock()
		finalizeBeforeExit = oldFinalize
		processExit = oldProcessExit
		exitMu.Unlock()
	})

	var order []string
	configureExitFinalizer(func() { order = append(order, "finalize") })
	processExit = func(code int) {
		if code != 7 {
			t.Errorf("exit code = %d, want 7", code)
		}
		order = append(order, "exit")
	}

	exitWithReceipt(7)
	if len(order) != 2 || order[0] != "finalize" || order[1] != "exit" {
		t.Fatalf("order = %v, want [finalize exit]", order)
	}
}

func TestExitWithReceiptConcurrentFinalizerReconfiguration(t *testing.T) {
	exitMu.Lock()
	oldFinalize := finalizeBeforeExit
	oldProcessExit := processExit
	processExit = func(int) {}
	exitMu.Unlock()
	t.Cleanup(func() {
		exitMu.Lock()
		finalizeBeforeExit = oldFinalize
		processExit = oldProcessExit
		exitMu.Unlock()
	})

	var finalizations atomic.Int32
	configureExitFinalizer(func() { finalizations.Add(1) })

	const calls = 100
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range calls {
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			configureExitFinalizer(func() { finalizations.Add(1) })
		}()
		go func() {
			defer wg.Done()
			<-start
			exitWithReceipt(0)
		}()
	}
	close(start)
	wg.Wait()

	if got := finalizations.Load(); got != calls {
		t.Errorf("finalizations = %d, want %d", got, calls)
	}
}
