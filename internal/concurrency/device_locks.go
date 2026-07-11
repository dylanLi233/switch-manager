package concurrency

import (
	"context"
	"errors"
	"strings"
	"sync"
)

var ErrInvalidDeviceID = errors.New("device ID is required")

type deviceEntry struct {
	semaphore  *Semaphore
	references int
}

// DeviceLockStats is a point-in-time view of keyed device locks.
type DeviceLockStats struct {
	Entries int
	Active  int
	Waiting int
}

// DeviceLocks serializes all operations targeting the same device and removes
// unused lock entries so arbitrary device IDs cannot grow the map forever.
type DeviceLocks struct {
	mu      sync.Mutex
	entries map[string]*deviceEntry
}

// NewDeviceLocks creates an empty keyed lock table.
func NewDeviceLocks() *DeviceLocks {
	return &DeviceLocks{entries: make(map[string]*deviceEntry)}
}

// DevicePermit holds one device lock. Release is idempotent.
type DevicePermit struct {
	once    sync.Once
	release func()
	id      string
}

// DeviceID returns the canonical device ID protected by the permit.
func (p *DevicePermit) DeviceID() string {
	if p == nil {
		return ""
	}
	return p.id
}

// Release unlocks the device.
func (p *DevicePermit) Release() {
	if p == nil {
		return
	}
	p.once.Do(func() {
		if p.release != nil {
			p.release()
		}
	})
}

// Acquire waits for exclusive access to deviceID.
func (d *DeviceLocks) Acquire(ctx context.Context, deviceID string) (*DevicePermit, error) {
	if d == nil {
		return nil, ErrInvalidDeviceID
	}
	if ctx == nil {
		return nil, ErrNilContext
	}
	canonical := strings.TrimSpace(deviceID)
	if canonical == "" {
		return nil, ErrInvalidDeviceID
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	d.mu.Lock()
	entry := d.entries[canonical]
	if entry == nil {
		semaphore, _ := NewSemaphore(1)
		entry = &deviceEntry{semaphore: semaphore}
		d.entries[canonical] = entry
	}
	entry.references++
	d.mu.Unlock()

	permit, err := entry.semaphore.Acquire(ctx)
	if err != nil {
		d.releaseReference(canonical, entry)
		return nil, err
	}

	return &DevicePermit{
		id: canonical,
		release: func() {
			permit.Release()
			d.releaseReference(canonical, entry)
		},
	}, nil
}

func (d *DeviceLocks) releaseReference(deviceID string, entry *deviceEntry) {
	d.mu.Lock()
	defer d.mu.Unlock()
	current := d.entries[deviceID]
	if current != entry {
		return
	}
	if entry.references > 0 {
		entry.references--
	}
	if entry.references == 0 {
		delete(d.entries, deviceID)
	}
}

// Stats returns aggregate device lock usage.
func (d *DeviceLocks) Stats() DeviceLockStats {
	if d == nil {
		return DeviceLockStats{}
	}
	d.mu.Lock()
	entries := make([]*deviceEntry, 0, len(d.entries))
	for _, entry := range d.entries {
		entries = append(entries, entry)
	}
	d.mu.Unlock()

	stats := DeviceLockStats{Entries: len(entries)}
	for _, entry := range entries {
		semaphore := entry.semaphore.Stats()
		stats.Active += semaphore.Active
		stats.Waiting += semaphore.Waiting
	}
	return stats
}
