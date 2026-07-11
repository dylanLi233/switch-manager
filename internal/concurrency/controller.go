package concurrency

import (
	"context"
	"errors"
	"sync"
)

const DefaultGlobalLimit = 5

var ErrNilOperation = errors.New("operation function is required")

// Snapshot is a point-in-time view of combined execution guards.
type Snapshot struct {
	Global  SemaphoreStats
	Devices DeviceLockStats
}

// Controller combines per-device serialization with a global concurrency cap.
type Controller struct {
	global  *Semaphore
	devices *DeviceLocks
}

// NewController creates execution guards with maxGlobal concurrent operations.
func NewController(maxGlobal int) (*Controller, error) {
	global, err := NewSemaphore(maxGlobal)
	if err != nil {
		return nil, err
	}
	return &Controller{global: global, devices: NewDeviceLocks()}, nil
}

// NewDefaultController creates the V1 global limit of five operations.
func NewDefaultController() *Controller {
	controller, err := NewController(DefaultGlobalLimit)
	if err != nil {
		panic(err)
	}
	return controller
}

// Lease holds both a device lock and one global slot. Release is idempotent.
type Lease struct {
	once   sync.Once
	global *Permit
	device *DevicePermit
}

// DeviceID returns the protected device ID.
func (l *Lease) DeviceID() string {
	if l == nil || l.device == nil {
		return ""
	}
	return l.device.DeviceID()
}

// Release returns both guards. The global slot is released first so unrelated
// devices can proceed while the keyed entry is being cleaned up.
func (l *Lease) Release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		if l.global != nil {
			l.global.Release()
		}
		if l.device != nil {
			l.device.Release()
		}
	})
}

// Acquire serializes deviceID first, then obtains a global slot. This order
// prevents same-device waiters from consuming scarce global slots while idle.
func (c *Controller) Acquire(ctx context.Context, deviceID string) (*Lease, error) {
	if c == nil || c.global == nil || c.devices == nil {
		return nil, ErrInvalidCapacity
	}
	devicePermit, err := c.devices.Acquire(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	globalPermit, err := c.global.Acquire(ctx)
	if err != nil {
		devicePermit.Release()
		return nil, err
	}
	return &Lease{global: globalPermit, device: devicePermit}, nil
}

// Do runs operation while holding both guards. Deferred release means a panic
// still frees the global slot and device lock; the panic is intentionally not
// recovered here and continues to the caller's recovery boundary.
func (c *Controller) Do(ctx context.Context, deviceID string, operation func(context.Context) error) error {
	if operation == nil {
		return ErrNilOperation
	}
	lease, err := c.Acquire(ctx, deviceID)
	if err != nil {
		return err
	}
	defer lease.Release()
	return operation(ctx)
}

// Snapshot returns current guard usage.
func (c *Controller) Snapshot() Snapshot {
	if c == nil || c.global == nil || c.devices == nil {
		return Snapshot{}
	}
	return Snapshot{Global: c.global.Stats(), Devices: c.devices.Stats()}
}
