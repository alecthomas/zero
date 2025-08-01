// Package cron provides support for Zero cron jobs.
package cron

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/alecthomas/errors"
	"github.com/alecthomas/zero/providers/leases"
)

type Schedule struct {
	name    string
	lastRun time.Time
	period  time.Duration
	run     Job
}

// NextRun returns the next time the job should run.
func (s *Schedule) NextRun() time.Time {
	return nextRun(s.period, s.lastRun)
}

func (s *Schedule) String() string {
	return fmt.Sprintf("Schedule(%q, nextRun=%s)", s.name, time.Until(s.NextRun()))
}

// Job represents a cron job.
type Job func(ctx context.Context) error

type Scheduler struct {
	lock      sync.Mutex
	logger    *slog.Logger
	leaser    leases.Leaser
	schedules []*Schedule
}

// NewScheduler creates a new cron scheduler.
//
// The [Scheduler] uses [leases.Leaser] to prevent cron jobs from running concurrently.
//
//zero:provider weak
func NewScheduler(ctx context.Context, logger *slog.Logger, leaser leases.Leaser) *Scheduler {
	s := &Scheduler{logger: logger, leaser: leaser}
	go s.run(ctx)
	return s
}

// Register a new cron job.
func (s *Scheduler) Register(name string, schedule time.Duration, job Job) error {
	if schedule < 5*time.Second {
		return errors.New("schedule duration must be at least 5 seconds")
	}
	s.lock.Lock()
	defer s.lock.Unlock()
	sched := &Schedule{name: name, period: schedule, run: job, lastRun: time.Now()}
	s.schedules = append(s.schedules, sched)
	s.logger.Debug("Scheduled new cron job", "job", sched.name)
	s.sortSchedulesNoLock()
	return nil
}

func (s *Scheduler) run(ctx context.Context) {
	ticker := time.NewTicker(time.Millisecond * 100)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		now := time.Now()
		s.lock.Lock()
		for _, schedule := range s.schedules {
			if !schedule.NextRun().Before(now) {
				continue
			}
			release, err := s.leaser.Acquire(ctx, "cron/"+schedule.name, schedule.period/2)
			if err != nil {
				s.logger.Error("Failed to acquire lease for cron job", "job", schedule.name, "error", err)
			}
			schedule.lastRun = now
			if err := schedule.run(ctx); err != nil {
				s.logger.Error("Cron job failed", "job", schedule.name, "error", err)
			}
			if err = release(ctx); err != nil {
				s.logger.Error("Failed to release lease for cron job", "job", schedule.name, "error", err)
			}
		}
		s.sortSchedulesNoLock()
		s.lock.Unlock()
	}
}

func (s *Scheduler) sortSchedulesNoLock() {
	slices.SortFunc(s.schedules, func(a, b *Schedule) int { return a.NextRun().Compare(b.NextRun()) })
}

// NullScheduler is a no-op scheduler used when no cron jobs are defined.
type NullScheduler struct{}

// Register does nothing for the null scheduler.
func (n *NullScheduler) Register(name string, schedule time.Duration, job Job) error {
	return nil
}

// NewNullScheduler creates a new null scheduler.
//
//zero:provider weak
func NewNullScheduler() *Scheduler {
	return &Scheduler{}
}

// Calculate the next time a cron job should run.
//
// eg. If period=5m, and lastRun=5:01 it will return 5:05.
func nextRun(period time.Duration, lastRun time.Time) time.Time {
	// Floor the current time to the nearest period boundary.
	lastRunDurationSinceEpoch := time.Duration(lastRun.UnixNano()) / period * period
	nextRun := lastRunDurationSinceEpoch + period
	return time.Unix(0, nextRun.Nanoseconds()).UTC()
}
