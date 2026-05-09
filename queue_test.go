package main

import (
	"sync"
	"testing"
)

func TestPromptQueue_EmptyByDefault(t *testing.T) {
	var q promptQueue
	if q.len() != 0 {
		t.Errorf("new queue len: got %d, want 0", q.len())
	}
}

func TestPromptQueue_PushIncreasesLen(t *testing.T) {
	var q promptQueue
	q.push("a")
	if q.len() != 1 {
		t.Errorf("len after one push: got %d, want 1", q.len())
	}
	q.push("b")
	if q.len() != 2 {
		t.Errorf("len after two pushes: got %d, want 2", q.len())
	}
}

func TestPromptQueue_PopFIFO(t *testing.T) {
	var q promptQueue
	q.push("first")
	q.push("second")
	q.push("third")

	got, ok := q.pop()
	if !ok || got != "first" {
		t.Errorf("pop 1: got (%q, %v), want (\"first\", true)", got, ok)
	}
	got, ok = q.pop()
	if !ok || got != "second" {
		t.Errorf("pop 2: got (%q, %v), want (\"second\", true)", got, ok)
	}
	got, ok = q.pop()
	if !ok || got != "third" {
		t.Errorf("pop 3: got (%q, %v), want (\"third\", true)", got, ok)
	}
}

func TestPromptQueue_PopEmptyReturnsFalse(t *testing.T) {
	var q promptQueue
	got, ok := q.pop()
	if ok || got != "" {
		t.Errorf("pop on empty: got (%q, %v), want (\"\", false)", got, ok)
	}
}

func TestPromptQueue_PopDecrementsLen(t *testing.T) {
	var q promptQueue
	q.push("x")
	q.push("y")
	q.pop()
	if q.len() != 1 {
		t.Errorf("len after pop: got %d, want 1", q.len())
	}
	q.pop()
	if q.len() != 0 {
		t.Error("queue should be empty after all items popped")
	}
}

func TestPromptQueue_PeekDoesNotRemove(t *testing.T) {
	var q promptQueue
	q.push("a")
	q.push("b")

	snap := q.peek()
	if len(snap) != 2 || snap[0] != "a" || snap[1] != "b" {
		t.Errorf("peek: got %v, want [a b]", snap)
	}
	if q.len() != 2 {
		t.Errorf("len after peek: got %d, want 2 (peek must not remove items)", q.len())
	}
}

func TestPromptQueue_PeekReturnsCopy(t *testing.T) {
	var q promptQueue
	q.push("a")

	snap := q.peek()
	snap[0] = "mutated"

	// The queue itself must be unaffected.
	second := q.peek()
	if second[0] != "a" {
		t.Errorf("modifying peek result mutated the queue; got %q, want \"a\"", second[0])
	}
}

func TestPromptQueue_PeekEmpty(t *testing.T) {
	var q promptQueue
	snap := q.peek()
	if snap == nil {
		t.Error("peek on empty queue returned nil, want empty slice")
	}
	if len(snap) != 0 {
		t.Errorf("peek on empty queue: got len %d, want 0", len(snap))
	}
}

func TestPromptQueue_ConcurrentPush(t *testing.T) {
	var q promptQueue
	const goroutines = 50
	const perGoroutine = 20

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				q.push("item")
			}
		}()
	}
	wg.Wait()

	want := goroutines * perGoroutine
	if got := q.len(); got != want {
		t.Errorf("concurrent push len: got %d, want %d", got, want)
	}
}

func TestPromptQueue_ConcurrentPushPop(t *testing.T) {
	var q promptQueue
	const count = 200

	var wg sync.WaitGroup

	// producers
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < count; i++ {
			q.push("item")
		}
	}()

	// consumers — drain whatever is available without blocking
	popped := make(chan int, count)
	wg.Add(1)
	go func() {
		defer wg.Done()
		total := 0
		for total < count {
			if _, ok := q.pop(); ok {
				total++
			}
		}
		popped <- total
	}()

	wg.Wait()
	got := <-popped
	if got != count {
		t.Errorf("concurrent push/pop: consumer got %d items, want %d", got, count)
	}
}
