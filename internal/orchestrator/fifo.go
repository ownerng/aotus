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

// Acquire takes a slot, waiting in line if none is free. It returns ctx.Err()
// if ctx ends first, in which case no slot is held.
func (s *fifoSem) Acquire(ctx context.Context) error {
	s.mu.Lock()
	if s.free > 0 && s.waiters.Len() == 0 {
		s.free--
		s.mu.Unlock()
		return nil
	}
	ready := make(chan struct{})
	elem := s.waiters.PushBack(ready)
	s.mu.Unlock()

	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		s.mu.Lock()
		select {
		case <-ready: // the slot was handed to us just as we gave up: pass it on
			s.mu.Unlock()
			s.Release()
		default:
			s.waiters.Remove(elem)
			s.mu.Unlock()
		}
		return ctx.Err()
	}
}

// Release returns a slot, handing it to the longest-waiting caller if any.
func (s *fifoSem) Release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if front := s.waiters.Front(); front != nil {
		s.waiters.Remove(front)
		close(front.Value.(chan struct{}))
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
