package session

import "sync"

// PromptQueue is a thread-safe FIFO for prompts submitted while the agent
// is already processing a previous request.
type PromptQueue struct {
	mu    sync.Mutex
	items []string
}

func (q *PromptQueue) Push(s string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = append(q.items, s)
}

func (q *PromptQueue) Pop() (string, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return "", false
	}
	s := q.items[0]
	q.items = q.items[1:]
	return s, true
}

func (q *PromptQueue) Peek() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]string, len(q.items))
	copy(out, q.items)
	return out
}

func (q *PromptQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}
