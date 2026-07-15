package main

import "testing"

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
