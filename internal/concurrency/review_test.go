package concurrency

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestSameDeviceWaiterDoesNotConsumeGlobalSlot(t *testing.T) {
	controller, _ := NewController(2)
	first, err := controller.Acquire(context.Background(), "device-a")
	if err != nil {
		t.Fatal(err)
	}
	secondResult := make(chan *Lease, 1)
	secondError := make(chan error, 1)
	go func() {
		lease, acquireErr := controller.Acquire(context.Background(), "device-a")
		if acquireErr != nil {
			secondError <- acquireErr
			return
		}
		secondResult <- lease
	}()
	waitFor(t, func() bool { return controller.Snapshot().Devices.Waiting == 1 }, "same-device waiter was not queued")
	if snapshot := controller.Snapshot(); snapshot.Global.Active != 1 || snapshot.Global.Waiting != 0 {
		t.Fatalf("same-device waiter consumed global capacity: %+v", snapshot)
	}
	other, err := controller.Acquire(context.Background(), "device-b")
	if err != nil {
		t.Fatalf("unrelated device could not use free global slot: %v", err)
	}
	other.Release()
	first.Release()
	select {
	case lease := <-secondResult:
		lease.Release()
	case err := <-secondError:
		t.Fatalf("second acquire error = %v", err)
	case <-time.After(time.Second):
		t.Fatal("same-device waiter did not proceed")
	}
}

func TestDeviceLockEntriesAreCleaned(t *testing.T) {
	controller, _ := NewController(5)
	for index := 0; index < 1000; index++ {
		lease, err := controller.Acquire(context.Background(), fmt.Sprintf("device-%d", index))
		if err != nil {
			t.Fatal(err)
		}
		lease.Release()
	}
	if snapshot := controller.Snapshot(); snapshot.Devices.Entries != 0 || snapshot.Devices.Active != 0 || snapshot.Devices.Waiting != 0 {
		t.Fatalf("device lock entries leaked: %+v", snapshot)
	}
}

func TestSameDeviceWaitersAreFIFO(t *testing.T) {
	controller, _ := NewController(1)
	held, _ := controller.Acquire(context.Background(), "device-a")
	var mu sync.Mutex
	order := make([]int, 0, 3)
	done := make(chan struct{}, 3)
	for index := 0; index < 3; index++ {
		index := index
		go func() {
			lease, err := controller.Acquire(context.Background(), "device-a")
			if err != nil {
				t.Errorf("Acquire() error = %v", err)
				done <- struct{}{}
				return
			}
			mu.Lock()
			order = append(order, index)
			mu.Unlock()
			lease.Release()
			done <- struct{}{}
		}()
		want := index + 1
		waitFor(t, func() bool { return controller.Snapshot().Devices.Waiting == want }, "device waiter was not queued")
	}
	held.Release()
	for range 3 {
		<-done
	}
	if fmt.Sprint(order) != "[0 1 2]" {
		t.Fatalf("device FIFO order = %v", order)
	}
}
