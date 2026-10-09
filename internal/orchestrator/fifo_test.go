package orchestrator

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestFifoSemServesInArrivalOrder(t *testing.T) {
	s := newFifoSem(1)
	first := s.Enqueue() // granted at once
	if err := first.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Ten tickets are taken in a fixed order while the slot is held; however the
	// goroutines that wait on them are scheduled, they are served in that order.
	var tickets []*ticket
	for i := 0; i < 10; i++ {
		tickets = append(tickets, s.Enqueue())
	}
	if s.Queued() != 10 {
		t.Fatalf("queued = %d", s.Queued())
	}
	var mu sync.Mutex
	var served []int
	var wg sync.WaitGroup
	for i := len(tickets) - 1; i >= 0; i-- { // start the waiters in reverse order
		wg.Add(1)
		go func(i int, tk *ticket) {
			defer wg.Done()
			if err := tk.Wait(context.Background()); err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			served = append(served, i)
			mu.Unlock()
			tk.Release()
		}(i, tickets[i])
	}
	time.Sleep(50 * time.Millisecond)
	first.Release()
	wg.Wait()
	for i, got := range served {
		if got != i {
			t.Fatalf("served %v, want arrival order 0..9", served)
		}
	}
}

func TestFifoSemGivesBackWhenAWaiterLeaves(t *testing.T) {
	s := newFifoSem(1)
	holder := s.Enqueue()
	_ = holder.Wait(context.Background())
	leaver := s.Enqueue()
	stayer := s.Enqueue()

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- leaver.Wait(ctx) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-errc; err == nil {
		t.Fatal("a canceled wait must fail")
	}
	if s.Queued() != 1 {
		t.Fatalf("the leaver must leave the line: queued = %d", s.Queued())
	}
	holder.Release()
	if err := stayer.Wait(context.Background()); err != nil {
		t.Fatalf("the next in line gets the slot: %v", err)
	}
	stayer.Release()
	if s.free != 1 {
		t.Fatalf("free slots = %d, want 1: nothing may leak", s.free)
	}
	leaver.Release() // releasing a ticket that never held a slot is harmless
	if s.free != 1 {
		t.Fatalf("free slots = %d after a stray release", s.free)
	}
}
