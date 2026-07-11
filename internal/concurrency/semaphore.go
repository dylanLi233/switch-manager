// Package concurrency provides cancellation-aware execution guards.
package concurrency

import (
	"context"
	"errors"
	"sync"
)

var (
	// ErrInvalidCapacity is returned when a semaphore has no usable slots.
	ErrInvalidCapacity = errors.New("semaphore capacity must be positive")
	// ErrNilContext is returned instead of panicking on a nil context.
	ErrNilContext = errors.New("context is required")
)

// SemaphoreStats is a point-in-time view of a semaphore.
type SemaphoreStats struct {
	Capacity int
	Active   int
	Waiting  int
}

type waiter struct {
	ready   chan struct{}
	granted bool
}

// Semaphore is a FIFO, context-aware counting semaphore.
type Semaphore struct {
	mu       sync.Mutex
	capacity int
	active   int
	queue    []*waiter
}

// NewSemaphore creates a semaphore with capacity concurrent holders.
func NewSemaphore(capacity int) (*Semaphore, error) {
	if capacity <= 0 {
		return nil, ErrInvalidCapacity
	}
	return &Semaphore{capacity: capacity}, nil
}

// Permit represents one acquired semaphore slot. Release is idempotent.
type Permit struct {
	once    sync.Once
	release func()
}

// Release returns the slot to the semaphore.
func (p *Permit) Release() {
	if p == nil {
		return
	}
	p.once.Do(func() {
		if p.release != nil {
			p.release()
		}
	})
}

// Acquire waits in FIFO order for one slot or for ctx cancellation.
func (s *Semaphore) Acquire(ctx context.Context) (*Permit, error) {
	if s == nil {
		return nil, ErrInvalidCapacity
	}
	if ctx == nil {
		return nil, ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	if s.active < s.capacity && len(s.queue) == 0 {
		s.active++
		s.mu.Unlock()
		return s.newPermit(), nil
	}
	queued := &waiter{ready: make(chan struct{})}
	s.queue = append(s.queue, queued)
	s.mu.Unlock()

	select {
	case <-queued.ready:
		// Cancellation may race with a grant. Prefer cancellation and return the
		// granted slot immediately rather than handing a canceled caller a permit.
		if err := ctx.Err(); err != nil {
			s.releaseOne()
			return nil, err
		}
		return s.newPermit(), nil
	case <-ctx.Done():
		s.mu.Lock()
		if queued.granted {
			s.active--
			s.grantLocked()
		} else {
			s.removeWaiterLocked(queued)
		}
		s.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (s *Semaphore) newPermit() *Permit {
	return &Permit{release: s.releaseOne}
}

func (s *Semaphore) releaseOne() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == 0 {
		return
	}
	s.active--
	s.grantLocked()
}

func (s *Semaphore) grantLocked() {
	for s.active < s.capacity && len(s.queue) > 0 {
		next := s.queue[0]
		copy(s.queue, s.queue[1:])
		s.queue[len(s.queue)-1] = nil
		s.queue = s.queue[:len(s.queue)-1]
		next.granted = true
		s.active++
		close(next.ready)
	}
}

func (s *Semaphore) removeWaiterLocked(target *waiter) {
	for index, candidate := range s.queue {
		if candidate != target {
			continue
		}
		copy(s.queue[index:], s.queue[index+1:])
		s.queue[len(s.queue)-1] = nil
		s.queue = s.queue[:len(s.queue)-1]
		return
	}
}

// Stats returns a consistent snapshot.
func (s *Semaphore) Stats() SemaphoreStats {
	if s == nil {
		return SemaphoreStats{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return SemaphoreStats{Capacity: s.capacity, Active: s.active, Waiting: len(s.queue)}
}
