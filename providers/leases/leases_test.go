package leases

import (
	"testing"
	"time"

	"github.com/alecthomas/assert/v2"
)

func testLeases(t *testing.T, leaser Leaser) { //nolint
	t.Run("AlreadyHeld", func(t *testing.T) {
		release, err := leaser.Acquire(t.Context(), "lease", time.Second)
		assert.NoError(t, err)
		defer release(t.Context()) //nolint
		_, err = leaser.Acquire(t.Context(), "lease", time.Second)
		assert.IsError(t, err, ErrLeaseHeld)
	})

	t.Run("Release", func(t *testing.T) {
		release, err := leaser.Acquire(t.Context(), "lease", time.Second)
		assert.NoError(t, err)
		assert.NoError(t, release(t.Context()))
	})

	t.Run("ReleaseTwice", func(t *testing.T) {
		release, err := leaser.Acquire(t.Context(), "lease", time.Second)
		assert.NoError(t, err)
		assert.NoError(t, release(t.Context()))
		assert.IsError(t, release(t.Context()), ErrLeaseNotHeld)
	})

	t.Run("ReacquireAfterRelease", func(t *testing.T) {
		release, err := leaser.Acquire(t.Context(), "lease", time.Second)
		assert.NoError(t, err)
		assert.NoError(t, release(t.Context()))
		release, err = leaser.Acquire(t.Context(), "lease", time.Second)
		assert.NoError(t, err)
		assert.NoError(t, release(t.Context()))
	})
}
