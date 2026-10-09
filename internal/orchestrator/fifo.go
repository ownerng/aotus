package orchestrator

import (
	"container/list"
	"context"
	"sync"
)

// fifoSem is a counting semaphore that serves waiters in the order they
// arrived, so that turns queue fairly when the daemon is busy.
type fifoSem struct {
	mu      sync.Mutex
	free    int
	waiters list.List // of chan struct{}
}

func newFifoSem(n int) *fifoSem { return &fifoSem{free: n} }

// ticket is a place in line. The order of tickets is the order in which
// Enqueue was called, so a caller that enqueues synchronously fixes its turn
// before doing anything else, whatever the scheduler does afterwards.
type ticket struct {
	s     *fifoSem
	ready chan struct{}
	elem  *list.Element // nil once the slot is granted
	held  bool          // the slot was granted and not yet released
}

// Enqueue takes a place in line. If a slot is free and nobody is waiting, the
// ticket is granted at once.
func (s *fifoSem) Enqueue() *ticket {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := &ticket{s: s, ready: make(chan struct{})}
	if s.free > 0 && s.waiters.Len() == 0 {
		s.free--
		t.held = true
		close(t.ready)
		return t
	}
	t.elem = s.waiters.PushBack(t)
	return t
}

// Wait blocks until the ticket is granted a slot. It returns ctx.Err() if ctx
// ends first, in which case the ticket holds nothing.
func (t *ticket) Wait(ctx context.Context) error {
	select {
	case <-t.ready:
		return nil
	case <-ctx.Done():
		t.s.mu.Lock()
		if t.held { // granted just as we gave up: hand the slot on
			t.held = false
			t.s.mu.Unlock()
			t.s.release()
			return ctx.Err()
		}
		t.s.waiters.Remove(t.elem)
		t.elem = nil
		t.s.mu.Unlock()
		return ctx.Err()
	}
}

// Release gives the slot back. Call it once, after Wait succeeded.
func (t *ticket) Release() {
	t.s.mu.Lock()
	held := t.held
	t.held = false
	t.s.mu.Unlock()
	if held {
		t.s.release()
	}
}

// release returns a slot, handing it to the longest-waiting ticket if any.
func (s *fifoSem) release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if front := s.waiters.Front(); front != nil {
		next := s.waiters.Remove(front).(*ticket)
		next.elem = nil
		next.held = true
		close(next.ready)
		return
	}
	s.free++
}

// Queued is how many callers are waiting.
func (s *fifoSem) Queued() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.waiters.Len()
}
