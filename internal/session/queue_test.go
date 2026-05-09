package session

import (
	"sync"
	"testing"
)

func TestPromptQueue_EmptyByDefault(t *testing.T) {
	var q PromptQueue
	if q.Len() != 0 {
		t.Errorf("new queue len: got %d, want 0", q.Len())
	}
}

func TestPromptQueue_PushIncreasesLen(t *testing.T) {
	var q PromptQueue
	q.Push("a")
	if q.Len() != 1 {
		t.Errorf("len after one push: got %d, want 1", q.Len())
	}
	q.Push("b")
	if q.Len() != 2 {
		t.Errorf("len after two pushes: got %d, want 2", q.Len())
	}
}

func TestPromptQueue_PopFIFO(t *testing.T) {
	var q PromptQueue
	q.Push("first")
	q.Push("second")
	q.Push("third")

	got, ok := q.Pop()
	if !ok || got != "first" {
		t.Errorf("pop 1: got (%q, %v), want (\"first\", true)", got, ok)
	}
	got, ok = q.Pop()
	if !ok || got != "second" {
		t.Errorf("pop 2: got (%q, %v), want (\"second\", true)", got, ok)
	}
	got, ok = q.Pop()
	if !ok || got != "third" {
		t.Errorf("pop 3: got (%q, %v), want (\"third\", true)", got, ok)
	}
}

func TestPromptQueue_PopEmptyReturnsFalse(t *testing.T) {
	var q PromptQueue
	got, ok := q.Pop()
	if ok || got != "" {
		t.Errorf("pop on empty: got (%q, %v), want (\"\", false)", got, ok)
	}
}

func TestPromptQueue_PopDecrementsLen(t *testing.T) {
	var q PromptQueue
	q.Push("x")
	q.Push("y")
	q.Pop()
	if q.Len() != 1 {
		t.Errorf("len after pop: got %d, want 1", q.Len())
	}
	q.Pop()
	if q.Len() != 0 {
		t.Error("queue should be empty after all items popped")
	}
}

func TestPromptQueue_PeekDoesNotRemove(t *testing.T) {
	var q PromptQueue
	q.Push("a")
	q.Push("b")

	snap := q.Peek()
	if len(snap) != 2 || snap[0] != "a" || snap[1] != "b" {
		t.Errorf("peek: got %v, want [a b]", snap)
	}
	if q.Len() != 2 {
		t.Errorf("len after peek: got %d, want 2 (peek must not remove items)", q.Len())
	}
}

func TestPromptQueue_PeekReturnsCopy(t *testing.T) {
	var q PromptQueue
	q.Push("a")

	snap := q.Peek()
	snap[0] = "mutated"

	// The queue itself must be unaffected.
	second := q.Peek()
	if second[0] != "a" {
		t.Errorf("modifying peek result mutated the queue; got %q, want \"a\"", second[0])
	}
}

func TestPromptQueue_PeekEmpty(t *testing.T) {
	var q PromptQueue
	snap := q.Peek()
	if snap == nil {
		t.Error("peek on empty queue returned nil, want empty slice")
	}
	if len(snap) != 0 {
		t.Errorf("peek on empty queue: got len %d, want 0", len(snap))
	}
}

func TestPromptQueue_ConcurrentPush(t *testing.T) {
	var q PromptQueue
	const goroutines = 50
	const perGoroutine = 20

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				q.Push("item")
			}
		}()
	}
	wg.Wait()

	want := goroutines * perGoroutine
	if got := q.Len(); got != want {
		t.Errorf("concurrent push len: got %d, want %d", got, want)
	}
}

func TestPromptQueue_ConcurrentPushPop(t *testing.T) {
	var q PromptQueue
	const count = 200

	var wg sync.WaitGroup

	// producers
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < count; i++ {
			q.Push("item")
		}
	}()

	// consumers — drain whatever is available without blocking
	popped := make(chan int, count)
	wg.Add(1)
	go func() {
		defer wg.Done()
		total := 0
		for total < count {
			if _, ok := q.Pop(); ok {
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
