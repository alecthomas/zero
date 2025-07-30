package cron

import (
	"context"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/alecthomas/assert/v2"
	"github.com/alecthomas/zero/providers/leases"
)

func TestNextRun(t *testing.T) {
	previous := time.Date(2023, 1, 1, 0, 0, 2, 0, time.UTC)
	next := nextRun(time.Second*5, previous)
	assert.Equal(t, time.Date(2023, 1, 1, 0, 0, 5, 0, time.UTC), next)

	previous = time.Date(2023, 1, 1, 0, 0, 4, int(time.Second-1), time.UTC)
	next = nextRun(time.Second*5, previous)
	assert.Equal(t, time.Date(2023, 1, 1, 0, 0, 5, 0, time.UTC), next)

	previous = time.Date(2023, 1, 1, 0, 0, 5, 1, time.UTC)
	next = nextRun(time.Second*5, previous)
	assert.Equal(t, time.Date(2023, 1, 1, 0, 0, 10, 0, time.UTC), next)
}

func TestScheduler(t *testing.T) {
	t.Skip("Blocked on https://github.com/golang/go/issues/74837")
	synctest.Run(func() {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
		leaser := leases.NewMemoryLeaser()
		s := NewScheduler(ctx, logger, leaser)

		var aliceRuns atomic.Int32
		err := s.Register("alice", time.Second*5, func(ctx context.Context) error {
			aliceRuns.Add(1)
			return nil
		})
		assert.NoError(t, err)

		time.Sleep(time.Second * 6)
		assert.Equal(t, 1, aliceRuns.Load())

		var bobRuns atomic.Int32
		err = s.Register("bob", time.Second*10, func(ctx context.Context) error {
			bobRuns.Add(1)
			return nil
		})
		assert.NoError(t, err)

		time.Sleep(time.Second * 6)

		assert.Equal(t, 1, bobRuns.Load())
		assert.Equal(t, 2, aliceRuns.Load())

		cancel()

		t.Log("Waiting for bubble")
		synctest.Wait()
		t.Log("Finished waiting")
	})
}
