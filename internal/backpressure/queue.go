package backpressure

import (
	"context"
	"errors"
	"sync"
)

// ErrFull is returned when the queue is saturated and policy is reject.
var ErrFull = errors.New("backpressure: queue full")

// Policy decides what to do when the queue is full.
type Policy string

const (
	// Reject drops the incoming item immediately (surfaced to metrics).
	Reject Policy = "reject"
	// Block waits up to the context deadline for room.
	Block Policy = "block"
	// DropOldest evicts the head to make room for the newest item.
	DropOldest Policy = "drop_oldest"
)

// Queue is a bounded work queue. This is the second half of host protection:
// even if the rate limiter is generous, the queue depth caps how much
// in-flight work can pile up, so memory cannot balloon under overload.
type Queue struct {
	ch     chan any
	policy Policy
	mu     sync.Mutex
	closed bool

	dropped int64
	pushed  int64
}

func New(size int, policy Policy) *Queue {
	if size < 1 {
		size = 1
	}
	if policy == "" {
		policy = Reject
	}
	return &Queue{ch: make(chan any, size), policy: policy}
}

// Push enqueues v according to policy. On success it owns the item.
func (q *Queue) Push(ctx context.Context, v any) error {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return errors.New("backpressure: queue closed")
	}
	q.mu.Unlock()

	switch q.policy {
	case Block:
		select {
		case q.ch <- v:
			q.countPush()
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	case DropOldest:
		for {
			select {
			case q.ch <- v:
				q.countPush()
				return nil
			default:
				select {
				case <-q.ch:
					q.countDrop()
				default:
				}
			}
		}
	default: // Reject
		select {
		case q.ch <- v:
			q.countPush()
			return nil
		default:
			q.countDrop()
			return ErrFull
		}
	}
}

func (q *Queue) countPush() {
	q.mu.Lock()
	q.pushed++
	q.mu.Unlock()
}

func (q *Queue) countDrop() {
	q.mu.Lock()
	q.dropped++
	q.mu.Unlock()
}

// Pop blocks for the next item until ctx is done or the queue is closed.
func (q *Queue) Pop(ctx context.Context) (any, bool) {
	select {
	case v, ok := <-q.ch:
		if !ok {
			return nil, false
		}
		return v, true
	case <-ctx.Done():
		return nil, false
	}
}

// Close drains and closes the queue. Workers exit their Pop loops.
func (q *Queue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.closed = true
	close(q.ch)
}

// Len is the current depth.
func (q *Queue) Len() int { return len(q.ch) }

// Cap is the configured capacity.
func (q *Queue) Cap() int { return cap(q.ch) }

// Stats returns pushed and dropped counters.
func (q *Queue) Stats() (pushed, dropped int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.pushed, q.dropped
}
