package cron

import (
	"context"
	"log/slog"
	"os"
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
	synctest.Run(func() {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
		leaser := leases.NewMemoryLeaser()
		s := NewSchedulerForTesting(ctx, logger, leaser, time.Now)
		aliceRuns := 0
		err := s.Register("alice", time.Second*5, func(ctx context.Context) error {
			aliceRuns++
			return nil
		})
		assert.NoError(t, err)

		time.Sleep(time.Second * 6)
		assert.Equal(t, 1, aliceRuns)

		bobRuns := 0
		err = s.Register("bob", time.Second*10, func(ctx context.Context) error {
			bobRuns++
			return nil
		})
		assert.NoError(t, err)

		time.Sleep(time.Second * 6)

		assert.Equal(t, 1, bobRuns)
		assert.Equal(t, 2, aliceRuns)

		cancel()

		t.Log("Waiting for bubble")
		synctest.Wait()
		t.Log("Finished waiting")
	})
}
