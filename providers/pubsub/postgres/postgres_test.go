//go:build postgres

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/alecthomas/assert/v2"
	"github.com/alecthomas/errors"
	"github.com/alecthomas/zero/providers/logging/loggingtest"
	"github.com/alecthomas/zero/providers/pubsub"
	"github.com/alecthomas/zero/providers/pubsub/pubsubtest"
	"github.com/alecthomas/zero/providers/sql/sqltest"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresPubSubBaseline(t *testing.T) {
	t.Parallel()
	logger := loggingtest.NewForTesting()
	db, _ := sqltest.NewForTesting(t, sqltest.PostgresDSN, Migrations())
	listener, err := NewListener(t.Context(), logger, db)
	assert.NoError(t, err)
	topic, err := New(t.Context(), logger, listener, db, DefaultConfig[pubsubtest.User]())
	assert.NoError(t, err)
	pubsubtest.RunPubSubTest(t, topic)
}

func TestErrDiscardHandling(t *testing.T) {
	t.Parallel()
	logger := loggingtest.NewForTesting()
	db, _ := sqltest.NewForTesting(t, sqltest.PostgresDSN, Migrations())
	listener, err := NewListener(t.Context(), logger, db)
	assert.NoError(t, err)
	defer listener.listenConn.Close(context.Background())

	topic, err := New(t.Context(), logger, listener, db, DefaultConfig[pubsubtest.User]())
	assert.NoError(t, err)
	defer topic.Close()

	// Subscribe with a handler that returns ErrDiscard
	err = topic.Subscribe(t.Context(), func(ctx context.Context, event pubsub.Event[pubsubtest.User]) error {
		return pubsub.ErrDiscard
	})
	assert.NoError(t, err)

	// Publish an event
	event := pubsub.NewEvent(pubsubtest.User{Name: "test", Age: 30})
	err = topic.Publish(t.Context(), event)
	assert.NoError(t, err)

	// Give some time for processing (retry until event is processed)
	for range 10 {
		stats, err := topic.(*Topic[pubsubtest.User]).queries.GetEventStats(t.Context(), 0, topic.(*Topic[pubsubtest.User]).topicID)
		assert.NoError(t, err)

		// If all counts are zero, the event was successfully discarded
		totalEvents := stats.PendingCount + stats.RetryCount + stats.ActiveCount + stats.SucceededCount + stats.FailedCount
		if totalEvents == 0 {
			// Event was successfully discarded - verify no dead letter either
			assert.Equal(t, int64(0), stats.DeadLetterCount)
			return
		}

		time.Sleep(100 * time.Millisecond)
	}

	// If we get here, the event wasn't discarded properly
	stats, err := topic.(*Topic[pubsubtest.User]).queries.GetEventStats(t.Context(), 0, topic.(*Topic[pubsubtest.User]).topicID)
	assert.NoError(t, err)
	t.Fatalf("Event was not discarded properly. Stats: pending=%d, retry=%d, active=%d, succeeded=%d, failed=%d",
		stats.PendingCount, stats.RetryCount, stats.ActiveCount, stats.SucceededCount, stats.FailedCount)
}

func TestRetryDeadLetter(t *testing.T) {
	t.Parallel()
	logger := loggingtest.NewForTesting()
	db, _ := sqltest.NewForTesting(t, sqltest.PostgresDSN, Migrations())
	listener, err := NewListener(t.Context(), logger, db)
	assert.NoError(t, err)
	defer listener.listenConn.Close(context.Background())

	// Create topic with DLQ enabled and no retries (immediate dead lettering)
	config := DefaultConfig[pubsubtest.User]()
	config.RetryConfig.Retries = 0
	config.DeadLetterConfig.Enabled = true

	topic, err := New(t.Context(), logger, listener, db, config)
	assert.NoError(t, err)
	defer topic.Close()

	// Subscribe with a handler that always fails
	failCalls := 0
	err = topic.Subscribe(t.Context(), func(ctx context.Context, event pubsub.Event[pubsubtest.User]) error {
		failCalls++
		return errors.New("test failure to trigger dead lettering")
	})
	assert.NoError(t, err)

	// Publish an event
	event := pubsub.NewEvent(pubsubtest.User{Name: "test", Age: 30})
	err = topic.Publish(t.Context(), event)
	assert.NoError(t, err)

	// Wait for the event to be processed and dead lettered
	for range 10 {
		stats, err := topic.(*Topic[pubsubtest.User]).queries.GetEventStats(t.Context(), 0, topic.(*Topic[pubsubtest.User]).topicID)
		assert.NoError(t, err)

		if stats.DeadLetterCount > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Verify the event is dead lettered
	stats, err := topic.(*Topic[pubsubtest.User]).queries.GetEventStats(t.Context(), 0, topic.(*Topic[pubsubtest.User]).topicID)
	assert.NoError(t, err)
	assert.Equal(t, int64(1), stats.DeadLetterCount)
	assert.Equal(t, int64(1), stats.FailedCount)
	assert.Equal(t, int64(0), stats.PendingCount)

	// Reset fail counter
	failCalls = 0

	// Retry the dead lettered event
	err = topic.(*Topic[pubsubtest.User]).RetryDeadLetter(t.Context(), event.ID())
	assert.NoError(t, err)

	// Wait for the event to be processed again
	for range 10 {
		if failCalls > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Verify the event was retried (handler called again)
	assert.True(t, failCalls > 0, "Handler should have been called again after retry")

	// Test retrying non-existent event
	err = topic.(*Topic[pubsubtest.User]).RetryDeadLetter(t.Context(), "non-existent-event-id")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found or not in dead letter queue")
}
