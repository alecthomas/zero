package leases

import (
	"context"
	"sync"
	"time"

	"github.com/alecthomas/errors"
)

type MemoryLeaser struct {
	lock   sync.RWMutex
	leases map[string]bool
}

var _ Leaser = (*MemoryLeaser)(nil)

// NewMemoryLeaser creates a [Leaser] that holds leases using an in-memory map.
//
// On the upside, it can never fail.
//
//zero:provider weak
func NewMemoryLeaser() *MemoryLeaser {
	return &MemoryLeaser{leases: make(map[string]bool)}
}

func (m *MemoryLeaser) Acquire(ctx context.Context, key string, timeout time.Duration) (Release, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tick := time.NewTicker(time.Millisecond * 100)
	defer tick.Stop()

	for {
		m.lock.RLock()
		ok := m.leases[key]
		m.lock.RUnlock()

		// Lease is held, sleep then try again.
		if ok {
			select {
			case <-tick.C:
			case <-ctx.Done():
				return nil, errors.Errorf("%s: %w", key, ErrLeaseHeld)
			}
			continue
		}

		// Try to acquire the lease
		m.lock.Lock()
		ok = m.leases[key]
		if ok {
			m.lock.Unlock()
			continue
		}

		// We have the lease
		defer m.lock.Unlock()
		m.leases[key] = true
		return func(ctx context.Context) error {
			m.lock.Lock()
			defer m.lock.Unlock()
			held := m.leases[key]
			if !held {
				return errors.Errorf("%s: %w", key, ErrLeaseNotHeld)
			}
			delete(m.leases, key)
			return nil
		}, nil
	}
}

func (m *MemoryLeaser) Close() error { return nil }
