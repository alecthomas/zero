package leases

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"math/rand/v2"
	"os"
	"time"

	"github.com/alecthomas/errors"
	zerosql "github.com/alecthomas/zero/providers/sql"
)

//go:embed migrations/*.sql
var migrations embed.FS

//zero:provider weak multi
func SQLLeaserMigrations() zerosql.Migrations {
	sub, _ := fs.Sub(migrations, "migrations")
	return zerosql.Migrations{sub}
}

type SQLLeaser struct {
	holder string
	stop   chan struct{}
	db     *sql.DB
	q      func(string) string
	driver zerosql.Driver
	log    *slog.Logger
}

var _ Leaser = (*SQLLeaser)(nil)

// NewSQLLeaser creates a [Leaser] backed by an SQL database.
//
//zero:provider weak require=SQLLeaserMigrations
func NewSQLLeaser(
	ctx context.Context,
	logger *slog.Logger,
	driver zerosql.Driver,
	db *sql.DB,
) (*SQLLeaser, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, errors.Errorf("failed to get hostname: %w", err)
	}
	pid := os.Getpid()
	s := &SQLLeaser{
		stop:   make(chan struct{}),
		holder: fmt.Sprintf("%s:%d", hostname, pid),
		db:     db,
		q:      driver.Denormalise,
		driver: driver,
		log:    logger,
	}
	go s.renewLoop(ctx)
	return s, nil
}

func (s *SQLLeaser) renewLoop(ctx context.Context) {
	for {
		select {
		case <-s.stop:
			return
		case <-ctx.Done():
			return
		case <-time.After(time.Second * 1):
			s.renew(ctx)
		}
	}
}

// Renew all leases
func (s *SQLLeaser) renew(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, time.Second*2)
	defer cancel()

	// Attempt to renew the leases until the context times out, at which point we take the nuclear
	// option of terminating the process.
	for {
		nextExpires := time.Now().UTC().Add(time.Second * 5)
		_, err := s.db.ExecContext(ctx, s.q(`
			UPDATE leases
			SET expires = ?
			WHERE holder = ?
		`), nextExpires, s.holder)
		select {
		case <-ctx.Done():
			if err == nil {
				return
			}
			s.log.Error("FATAL: failed to renew leases, terminating to avoid split brain", "err", err)
			os.Exit(1)

		case <-time.After(jitter(time.Millisecond * 100)):
		}
	}
}

func (s *SQLLeaser) Acquire(ctx context.Context, key string, timeout time.Duration) (Release, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Try to acquire the lease until the timeout is reached
retry:
	for {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				break
			}
			return nil, errors.Errorf("lease %s: failed to begin transaction: %w", key, err)
		}
		if err := s.acquireTx(ctx, tx, key); err != nil {
			s.log.Debug("Failed to acquire lease, will retry", "err", err)
			// Failed to acquire lease, rollback and fallthrough to the retry.
			_ = tx.Rollback()
			if errors.Is(err, context.DeadlineExceeded) {
				break
			}
		} else if err := tx.Commit(); err != nil {
			return nil, errors.WithStack(err)
		} else {
			return func(ctx context.Context) error {
				return errors.WithStack(s.releaseLease(ctx, key))
			}, nil
		}

		select {
		case <-ctx.Done():
			break retry
		case <-time.After(jitter(time.Second)):
		}
	}

	// Couldn't acquire the lease, try to find the holder.
	ctx, cancel = context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	row := s.db.QueryRowContext(ctx, s.q(`SELECT holder FROM leases WHERE lease = ?`), key) //nolint
	if row.Err() != nil {
		return nil, errors.Errorf("lease %s: failed to query lease holder: %w", key, row.Err())
	}
	var holder string
	if err := row.Scan(&holder); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.Errorf("%s: %w: by unknown", key, ErrLeaseHeld)
		}
		return nil, errors.WithStack(err)
	}
	return nil, errors.Errorf("%s: %w: by %s", key, ErrLeaseHeld, holder)
}

func (s *SQLLeaser) releaseLease(ctx context.Context, key string) error {
	result, err := s.db.ExecContext(ctx, s.q(`
		DELETE FROM leases
		WHERE lease = ? AND holder = ?
	`), key, s.holder)
	if err != nil {
		return errors.WithStack(s.driver.TranslateError(err))
	}
	count, err := result.RowsAffected()
	if err != nil {
		return errors.WithStack(s.driver.TranslateError(err))
	}
	if count == 0 {
		return errors.Errorf("lease %s: %w", key, ErrLeaseNotHeld)
	}
	return nil
}

func (s *SQLLeaser) acquireTx(ctx context.Context, tx *sql.Tx, key string) error {
	// Delete lease if it exists and is expired
	now := time.Now().UTC()
	_, err := tx.ExecContext(ctx, s.q(`
		DELETE FROM leases
		WHERE lease = ? AND expires < ?;
	`), key, now)
	if err != nil {
		return errors.WithStack(s.driver.TranslateError(err))
	}

	// If we can insert successfully, we have the lease
	expires := time.Now().UTC().Add(time.Second * 5)
	_, err = tx.ExecContext(ctx, s.q(`
		INSERT INTO leases (lease, holder, expires)
		VALUES (?, ?, ?)
	`), key, s.holder, expires)
	if err != nil {
		return errors.WithStack(s.driver.TranslateError(err))
	}

	return nil
}

func (s *SQLLeaser) Close() error { close(s.stop); return nil }

// Jitter n ± 10%
func jitter(n time.Duration) time.Duration {
	ni := int64(n)
	return time.Duration(ni + rand.Int64N(ni/10) - ni/20) //nolint
}
