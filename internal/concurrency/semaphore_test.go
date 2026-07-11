package concurrency

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

func waitFor(t *testing.T, condition func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal(message)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestSemaphoreFIFO(t *testing.T) {
	semaphore, err := NewSemaphore(1)
	if err != nil {
		t.Fatal(err)
	}
	held, err := semaphore.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	order := make([]int, 0, 3)
	done := make(chan struct{}, 3)
	for index := 0; index < 3; index++ {
		index := index
		go func() {
			permit, acquireErr := semaphore.Acquire(context.Background())
			if acquireErr != nil {
				t.Errorf("Acquire() error = %v", acquireErr)
				done <- struct{}{}
				return
			}
			mu.Lock()
			order = append(order, index)
			mu.Unlock()
			permit.Release()
			done <- struct{}{}
		}()
		wantWaiting := index + 1
		waitFor(t, func() bool { return semaphore.Stats().Waiting == wantWaiting }, "waiter was not queued")
	}
	held.Release()
	for range 3 {
		<-done
	}
	if !reflect.DeepEqual(order, []int{0, 1, 2}) {
		t.Fatalf("FIFO order = %v", order)
	}
}

func TestSemaphoreCancellationRemovesWaiter(t *testing.T) {
	semaphore, _ := NewSemaphore(1)
	held, _ := semaphore.Acquire(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := semaphore.Acquire(ctx)
		result <- err
	}()
	waitFor(t, func() bool { return semaphore.Stats().Waiting == 1 }, "canceled waiter was not queued")
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire() error = %v", err)
	}
	waitFor(t, func() bool { return semaphore.Stats().Waiting == 0 }, "canceled waiter was not removed")
	held.Release()
	permit, err := semaphore.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	permit.Release()
}

func TestPermitReleaseIsIdempotent(t *testing.T) {
	semaphore, _ := NewSemaphore(1)
	permit, _ := semaphore.Acquire(context.Background())
	permit.Release()
	permit.Release()
	stats := semaphore.Stats()
	if stats.Active != 0 || stats.Waiting != 0 {
		t.Fatalf("stats = %+v", stats)
	}
}
