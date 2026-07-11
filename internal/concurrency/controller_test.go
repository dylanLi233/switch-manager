package concurrency

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func updatePeak(peak *atomic.Int64, value int64) {
	for {
		current := peak.Load()
		if value <= current || peak.CompareAndSwap(current, value) {
			return
		}
	}
}

func TestDefaultControllerCapsTenTasksAtFive(t *testing.T) {
	controller := NewDefaultController()
	var active atomic.Int64
	var peak atomic.Int64
	entered := make(chan struct{}, 10)
	release := make(chan struct{})
	var group sync.WaitGroup

	for index := 0; index < 10; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			err := controller.Do(context.Background(), fmt.Sprintf("device-%d", index), func(context.Context) error {
				current := active.Add(1)
				updatePeak(&peak, current)
				entered <- struct{}{}
				<-release
				active.Add(-1)
				return nil
			})
			if err != nil {
				t.Errorf("Do() error = %v", err)
			}
		}(index)
	}
	for range DefaultGlobalLimit {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("five tasks did not enter")
		}
	}
	select {
	case <-entered:
		t.Fatal("more than five tasks entered concurrently")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	group.Wait()
	if got := peak.Load(); got != DefaultGlobalLimit {
		t.Fatalf("peak = %d, want %d", got, DefaultGlobalLimit)
	}
	if snapshot := controller.Snapshot(); snapshot.Global.Active != 0 || snapshot.Devices.Entries != 0 {
		t.Fatalf("snapshot after completion = %+v", snapshot)
	}
}

func TestSameDeviceOperationsNeverOverlap(t *testing.T) {
	controller, _ := NewController(5)
	var active atomic.Int64
	var peak atomic.Int64
	var overlap atomic.Bool
	var group sync.WaitGroup
	for range 20 {
		group.Add(1)
		go func() {
			defer group.Done()
			err := controller.Do(context.Background(), "device-one", func(context.Context) error {
				current := active.Add(1)
				updatePeak(&peak, current)
				if current != 1 {
					overlap.Store(true)
				}
				time.Sleep(2 * time.Millisecond)
				active.Add(-1)
				return nil
			})
			if err != nil {
				t.Errorf("Do() error = %v", err)
			}
		}()
	}
	group.Wait()
	if overlap.Load() || peak.Load() != 1 {
		t.Fatalf("same-device peak=%d overlap=%v", peak.Load(), overlap.Load())
	}
}

func TestDifferentDevicesCanOverlap(t *testing.T) {
	controller, _ := NewController(2)
	entered := make(chan string, 2)
	release := make(chan struct{})
	var group sync.WaitGroup
	for _, deviceID := range []string{"a", "b"} {
		deviceID := deviceID
		group.Add(1)
		go func() {
			defer group.Done()
			_ = controller.Do(context.Background(), deviceID, func(context.Context) error {
				entered <- deviceID
				<-release
				return nil
			})
		}()
	}
	for range 2 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("different devices did not overlap")
		}
	}
	close(release)
	group.Wait()
}

func TestPanicDoesNotLeakGuards(t *testing.T) {
	controller, _ := NewController(1)
	func() {
		defer func() {
			if recovered := recover(); recovered == nil {
				t.Fatal("expected panic")
			}
		}()
		_ = controller.Do(context.Background(), "device-panic", func(context.Context) error {
			panic("boom")
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	lease, err := controller.Acquire(ctx, "device-panic")
	if err != nil {
		t.Fatalf("Acquire() after panic error = %v", err)
	}
	lease.Release()
	if snapshot := controller.Snapshot(); snapshot.Global.Active != 0 || snapshot.Devices.Entries != 0 {
		t.Fatalf("snapshot after panic recovery = %+v", snapshot)
	}
}

func TestCancellationWhileWaitingGlobalReleasesDevice(t *testing.T) {
	controller, _ := NewController(1)
	held, err := controller.Acquire(context.Background(), "device-b")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, acquireErr := controller.Acquire(ctx, "device-a")
		result <- acquireErr
	}()
	waitFor(t, func() bool {
		snapshot := controller.Snapshot()
		return snapshot.Global.Waiting == 1 && snapshot.Devices.Active == 2
	}, "device-a did not acquire its device lock before waiting globally")
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire() error = %v", err)
	}
	held.Release()

	acquireCtx, acquireCancel := context.WithTimeout(context.Background(), time.Second)
	defer acquireCancel()
	lease, err := controller.Acquire(acquireCtx, "device-a")
	if err != nil {
		t.Fatalf("device lock leaked after cancellation: %v", err)
	}
	lease.Release()
}

func TestDeviceWaiterCancellationDoesNotBlockFollowingTask(t *testing.T) {
	controller, _ := NewController(2)
	held, _ := controller.Acquire(context.Background(), "device-a")
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() {
		_, err := controller.Acquire(ctx, "device-a")
		first <- err
	}()
	waitFor(t, func() bool { return controller.Snapshot().Devices.Waiting == 1 }, "device waiter was not queued")
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("first waiter error = %v", err)
	}
	held.Release()
	lease, err := controller.Acquire(context.Background(), "device-a")
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
}

func TestInvalidInputs(t *testing.T) {
	if _, err := NewController(0); !errors.Is(err, ErrInvalidCapacity) {
		t.Fatalf("NewController() error = %v", err)
	}
	controller, _ := NewController(1)
	if _, err := controller.Acquire(nil, "device"); !errors.Is(err, ErrNilContext) {
		t.Fatalf("nil context error = %v", err)
	}
	if _, err := controller.Acquire(context.Background(), "  "); !errors.Is(err, ErrInvalidDeviceID) {
		t.Fatalf("empty device error = %v", err)
	}
	if err := controller.Do(context.Background(), "device", nil); !errors.Is(err, ErrNilOperation) {
		t.Fatalf("nil operation error = %v", err)
	}
}
