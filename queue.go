package main

import "sync"

// promptQueue is a thread-safe FIFO for prompts submitted while the agent
// is already processing a previous request.
type promptQueue struct {
	mu    sync.Mutex
	items []string
}

func (q *promptQueue) push(s string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = append(q.items, s)
}

func (q *promptQueue) pop() (string, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return "", false
	}
	s := q.items[0]
	q.items = q.items[1:]
	return s, true
}

func (q *promptQueue) peek() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]string, len(q.items))
	copy(out, q.items)
	return out
}

func (q *promptQueue) len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

