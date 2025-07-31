// Package leases provides an API for acquiring and releasing leases.
package leases

import (
	"context"
	"errors"
	"time"
)

// ErrLeaseHeld is returned when the lease acquisition times out because the lease is already held.
var ErrLeaseHeld = errors.New("lease is held")

// ErrLeaseNotHeld is returned by Release when the lease is not held.
var ErrLeaseNotHeld = errors.New("lease is not held")

// Release an acquired lease.
type Release func(ctx context.Context) error

// Leaser is an interface for lease acquisition and release.
type Leaser interface {
	// Acquire acquires a lease for the given key.
	//
	// It will block until the lease is acquired, timeout is reached, or context is cancelled. Once acquired, the lease
	// will be automatically released when the context is canceled, or when the Release function is called.
	//
	// Leases are automatically renewed, but if renewal fails after a period of retries, the process will be terminated
	// to avoid split-brain.
	Acquire(ctx context.Context, key string, timeout time.Duration) (Release, error)

	// Close releases all resources associated with the leaser.
	Close() error
}
