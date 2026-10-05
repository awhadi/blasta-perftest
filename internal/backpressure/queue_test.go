package backpressure

import (
	"context"
	"testing"
	"time"
)

func time_sleep_short() { time.Sleep(20 * time.Millisecond) }

func TestRejectWhenFull(t *testing.T) {
	q := New(2, Reject)
	ctx := context.Background()
	if err := q.Push(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := q.Push(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if err := q.Push(ctx, 3); err != ErrFull {
		t.Fatalf("expected ErrFull, got %v", err)
	}
	pushed, dropped := q.Stats()
	if pushed != 2 || dropped != 1 {
		t.Fatalf("pushed=%d dropped=%d", pushed, dropped)
	}
}

func TestBlockWaitsForRoom(t *testing.T) {
	q := New(1, Block)
	ctx := context.Background()
	_ = q.Push(ctx, 1)

	done := make(chan error, 1)
	go func() { done <- q.Push(ctx, 2) }()

	// Free a slot; the blocked push should complete.
	if v, ok := q.Pop(ctx); !ok || v.(int) != 1 {
		t.Fatal("unexpected pop")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("push failed: %v", err)
		}
	case <-context.Background().Done():
		t.Fatal("unreachable")
	}
}

func TestDropOldestKeepsNewest(t *testing.T) {
	q := New(1, DropOldest)
	ctx := context.Background()
	_ = q.Push(ctx, 1)
	_ = q.Push(ctx, 2)
	v, _ := q.Pop(ctx)
	if v.(int) != 2 {
		t.Fatalf("expected newest item 2, got %v", v)
	}
}

func TestPopUnblocksOnContextCancel(t *testing.T) {
	q := New(1, Block)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time_sleep_short()
		cancel()
	}()
	if _, ok := q.Pop(ctx); ok {
		t.Fatal("Pop should return false when context is canceled")
	}
}
