//go:build postgres

package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/alecthomas/assert/v2"
	"github.com/alecthomas/zero/providers/pubsub/postgres/internal"
	"github.com/alecthomas/zero/providers/sql/sqltest"
)

//nolint:tparallel // Subtests share database state and cannot run in parallel
func TestCreateTopic(t *testing.T) {
	t.Parallel()
	db, _ := sqltest.NewForTesting(t, sqltest.PostgresDSN, Migrations())
	queries := internal.New(db)
	ctx := context.Background()

	tests := []struct {
		name   string
		params internal.CreateTopicParams
	}{
		{
			name: "BasicTopic",
			params: internal.CreateTopicParams{
				Name:              "test-topic",
				MaxRetries:        3,
				InitialBackoff:    internal.Duration(time.Minute),
				BackoffMax:        internal.Duration(5 * time.Minute),
				BackoffMultiplier: 2.0,
				DlqEnabled:        false,
				DlqMaxAge:         internal.Duration(7 * 24 * time.Hour),
			},
		},
		{
			name: "TopicWithDLQ",
			params: internal.CreateTopicParams{
				Name:              "dlq-topic",
				MaxRetries:        5,
				InitialBackoff:    internal.Duration(30 * time.Second),
				BackoffMax:        internal.Duration(10 * time.Minute),
				BackoffMultiplier: 2.5,
				DlqEnabled:        true,
				DlqMaxAge:         internal.Duration(3 * 24 * time.Hour),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			topic, err := queries.CreateTopic(ctx, tt.params)
			assert.NoError(t, err)
			assert.Equal(t, tt.params.Name, topic.Name)
			assert.Equal(t, tt.params.MaxRetries, topic.MaxRetries)
			assert.Equal(t, tt.params.InitialBackoff, topic.InitialBackoff)
			assert.Equal(t, tt.params.BackoffMax, topic.BackoffMax)
			assert.Equal(t, tt.params.BackoffMultiplier, topic.BackoffMultiplier)
			assert.Equal(t, tt.params.DlqEnabled, topic.DlqEnabled)
			assert.Equal(t, tt.params.DlqMaxAge, topic.DlqMaxAge)
			assert.True(t, topic.ID > 0)
			assert.True(t, !topic.CreatedAt.IsZero())
		})
	}
}

func TestCreateTopicUpsert(t *testing.T) {
	t.Parallel()
	db, _ := sqltest.NewForTesting(t, sqltest.PostgresDSN, Migrations())
	queries := internal.New(db)
	ctx := context.Background()

	// Create initial topic
	params1 := internal.CreateTopicParams{
		Name:              "upsert-topic",
		MaxRetries:        3,
		InitialBackoff:    internal.Duration(time.Minute),
		BackoffMax:        internal.Duration(5 * time.Minute),
		BackoffMultiplier: 3.0,
		DlqEnabled:        false,
		DlqMaxAge:         internal.Duration(7 * 24 * time.Hour),
	}

	topic1, err := queries.CreateTopic(ctx, params1)
	assert.NoError(t, err)

	// Update the same topic
	params2 := internal.CreateTopicParams{
		Name:              "upsert-topic", // same name
		MaxRetries:        5,              // different values
		InitialBackoff:    internal.Duration(30 * time.Second),
		BackoffMax:        internal.Duration(10 * time.Minute),
		BackoffMultiplier: 1.5,
		DlqEnabled:        true,
		DlqMaxAge:         internal.Duration(3 * 24 * time.Hour),
	}

	topic2, err := queries.CreateTopic(ctx, params2)
	assert.NoError(t, err)

	// Should be the same topic (same ID) but with updated values
	assert.Equal(t, topic1.ID, topic2.ID)
	assert.Equal(t, params2.MaxRetries, topic2.MaxRetries)
	assert.Equal(t, params2.DlqEnabled, topic2.DlqEnabled)
}

func TestGetTopicByName(t *testing.T) {
	t.Parallel()
	db, _ := sqltest.NewForTesting(t, sqltest.PostgresDSN, Migrations())
	queries := internal.New(db)
	ctx := context.Background()

	// Create a topic first
	params := internal.CreateTopicParams{
		Name:              "get-topic",
		MaxRetries:        3,
		InitialBackoff:    internal.Duration(time.Minute),
		BackoffMax:        internal.Duration(5 * time.Minute),
		BackoffMultiplier: 2.0,
		DlqEnabled:        true,
		DlqMaxAge:         internal.Duration(7 * 24 * time.Hour),
	}

	createdTopic, err := queries.CreateTopic(ctx, params)
	assert.NoError(t, err)

	// Retrieve the topic by name
	retrievedTopic, err := queries.GetTopicByName(ctx, "get-topic")
	assert.NoError(t, err)
	assert.Equal(t, createdTopic, retrievedTopic)

	// Try to get non-existent topic
	_, err = queries.GetTopicByName(ctx, "non-existent")
	assert.Error(t, err)
}

func TestPublishEvent(t *testing.T) {
	t.Parallel()
	db, _ := sqltest.NewForTesting(t, sqltest.PostgresDSN, Migrations())
	queries := internal.New(db)
	ctx := context.Background()

	// Create a topic first
	topic, err := queries.CreateTopic(ctx, internal.CreateTopicParams{
		Name:              "publish-topic",
		MaxRetries:        3,
		InitialBackoff:    internal.Duration(time.Minute),
		BackoffMax:        internal.Duration(5 * time.Minute),
		BackoffMultiplier: 2.0,
		DlqEnabled:        false,
		DlqMaxAge:         internal.Duration(7 * 24 * time.Hour),
	})
	assert.NoError(t, err)

	message := json.RawMessage(`{"type": "test", "data": "hello world"}`)
	headers := json.RawMessage(`{"source": "test"}`)

	eventID, err := queries.PublishEvent(ctx, topic.ID, "test-event-123", message, headers)
	assert.NoError(t, err)
	assert.True(t, eventID > 0)

	// Test duplicate CloudEvents ID should fail
	_, err = queries.PublishEvent(ctx, topic.ID, "test-event-123", message, headers)
	assert.Error(t, err) // Should fail due to unique constraint
}

func TestClaimNextEvent(t *testing.T) {
	t.Parallel()
	db, _ := sqltest.NewForTesting(t, sqltest.PostgresDSN, Migrations())
	queries := internal.New(db)
	ctx := context.Background()

	// Create a topic
	topic, err := queries.CreateTopic(ctx, internal.CreateTopicParams{
		Name:              "claim-topic",
		MaxRetries:        3,
		InitialBackoff:    internal.Duration(time.Minute),
		BackoffMax:        internal.Duration(5 * time.Minute),
		BackoffMultiplier: 2.0,
		DlqEnabled:        false,
		DlqMaxAge:         internal.Duration(7 * 24 * time.Hour),
	})
	assert.NoError(t, err)

	// Publish some events
	message1 := json.RawMessage(`{"data": "event1"}`)
	message2 := json.RawMessage(`{"data": "event2"}`)
	headers := json.RawMessage(`{}`)

	eventID1, err := queries.PublishEvent(ctx, topic.ID, "event-1", message1, headers)
	assert.NoError(t, err)

	eventID2, err := queries.PublishEvent(ctx, topic.ID, "event-2", message2, headers)
	assert.NoError(t, err)

	// Claim first event
	claimedEvent, err := queries.ClaimNextEvent(ctx, topic.ID)
	assert.NoError(t, err)
	assert.Equal(t, eventID1, claimedEvent.ID)
	assert.Equal(t, internal.PubsubEventStateActive, claimedEvent.State)
	assert.Equal(t, "event-1", claimedEvent.CloudeventsID)
	assert.Equal(t, message1, claimedEvent.Message)

	// Claim second event
	claimedEvent2, err := queries.ClaimNextEvent(ctx, topic.ID)
	assert.NoError(t, err)
	assert.Equal(t, eventID2, claimedEvent2.ID)

	// No more events to claim
	_, err = queries.ClaimNextEvent(ctx, topic.ID)
	assert.Error(t, err) // Should return no rows
}

func TestCompleteEvent(t *testing.T) {
	t.Parallel()
	db, _ := sqltest.NewForTesting(t, sqltest.PostgresDSN, Migrations())
	queries := internal.New(db)
	ctx := context.Background()

	// Create topic and publish event
	topic, err := queries.CreateTopic(ctx, internal.CreateTopicParams{
		Name:              "complete-topic",
		MaxRetries:        3,
		InitialBackoff:    internal.Duration(time.Minute),
		BackoffMax:        internal.Duration(5 * time.Minute),
		BackoffMultiplier: 2.0,
		DlqEnabled:        false,
		DlqMaxAge:         internal.Duration(7 * 24 * time.Hour),
	})
	assert.NoError(t, err)

	eventID, err := queries.PublishEvent(ctx, topic.ID, "complete-event", json.RawMessage(`{}`), json.RawMessage(`{}`))
	assert.NoError(t, err)

	// Claim the event
	claimedEvent, err := queries.ClaimNextEvent(ctx, topic.ID)
	assert.NoError(t, err)
	assert.Equal(t, internal.PubsubEventStateActive, claimedEvent.State)

	// Complete the event
	success, err := queries.CompleteEvent(ctx, eventID)
	assert.NoError(t, err)
	assert.True(t, success)

	// Trying to complete again should return false
	success, err = queries.CompleteEvent(ctx, eventID)
	assert.NoError(t, err)
	assert.False(t, success)
}

//nolint:tparallel // Subtests share database state and cannot run in parallel
func TestFailEvent(t *testing.T) {
	t.Parallel()
	db, _ := sqltest.NewForTesting(t, sqltest.PostgresDSN, Migrations())
	queries := internal.New(db)
	ctx := context.Background()

	tests := []struct {
		name           string
		maxRetries     int64
		dlqEnabled     bool
		expectedAction internal.PubsubFailAction
	}{
		{
			name:           "RetryingWithRetriesLeft",
			maxRetries:     3,
			dlqEnabled:     false,
			expectedAction: internal.PubsubFailActionRetrying,
		},
		{
			name:           "DeadLetteredWhenDLQEnabled",
			maxRetries:     0, // No retries
			dlqEnabled:     true,
			expectedAction: internal.PubsubFailActionDeadLettered,
		},
		{
			name:           "FailedWhenNoDLQ",
			maxRetries:     0, // No retries
			dlqEnabled:     false,
			expectedAction: internal.PubsubFailActionFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create topic with specific configuration
			topic, err := queries.CreateTopic(ctx, internal.CreateTopicParams{
				Name:              "fail-topic-" + tt.name,
				MaxRetries:        tt.maxRetries,
				InitialBackoff:    internal.Duration(time.Minute),
				BackoffMax:        internal.Duration(5 * time.Minute),
				BackoffMultiplier: 2.0,
				DlqEnabled:        tt.dlqEnabled,
				DlqMaxAge:         internal.Duration(7 * 24 * time.Hour),
			})
			assert.NoError(t, err)

			// Publish and claim event
			eventID, err := queries.PublishEvent(ctx, topic.ID, "fail-event-"+tt.name, json.RawMessage(`{}`), json.RawMessage(`{}`))
			assert.NoError(t, err)

			_, err = queries.ClaimNextEvent(ctx, topic.ID)
			assert.NoError(t, err)

			// Fail the event
			action, err := queries.FailEvent(ctx, eventID, "test error message")
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedAction, action)
		})
	}
}

func TestFailEventRetryExhaustion(t *testing.T) {
	t.Parallel()
	db, _ := sqltest.NewForTesting(t, sqltest.PostgresDSN, Migrations())
	queries := internal.New(db)
	ctx := context.Background()

	// Create topic with 2 max retries and DLQ enabled
	topic, err := queries.CreateTopic(ctx, internal.CreateTopicParams{
		Name:              "retry-exhaustion-topic",
		MaxRetries:        2,
		InitialBackoff:    internal.Duration(100 * time.Millisecond), // Very short for testing
		BackoffMax:        internal.Duration(200 * time.Millisecond),
		BackoffMultiplier: 1.1, // Minimal multiplier
		DlqEnabled:        true,
		DlqMaxAge:         internal.Duration(7 * 24 * time.Hour),
	})
	assert.NoError(t, err)

	// Publish and claim event
	eventID, err := queries.PublishEvent(ctx, topic.ID, "retry-event", json.RawMessage(`{}`), json.RawMessage(`{}`))
	assert.NoError(t, err)

	_, err = queries.ClaimNextEvent(ctx, topic.ID)
	assert.NoError(t, err)

	// First failure - should retry
	action, err := queries.FailEvent(ctx, eventID, "first failure")
	assert.NoError(t, err)
	assert.Equal(t, internal.PubsubFailActionRetrying, action)

	// Wait a bit and claim again - should retry
	time.Sleep(150 * time.Millisecond)
	_, err = queries.ClaimNextEvent(ctx, topic.ID)
	assert.NoError(t, err)

	action, err = queries.FailEvent(ctx, eventID, "second failure")
	assert.NoError(t, err)
	assert.Equal(t, internal.PubsubFailActionRetrying, action)

	// Wait a bit and claim third time - should dead letter (retries exhausted)
	time.Sleep(250 * time.Millisecond)
	_, err = queries.ClaimNextEvent(ctx, topic.ID)
	assert.NoError(t, err)

	action, err = queries.FailEvent(ctx, eventID, "third failure")
	assert.NoError(t, err)
	assert.Equal(t, internal.PubsubFailActionDeadLettered, action)
}

func TestGetPendingEventCount(t *testing.T) {
	t.Parallel()
	db, _ := sqltest.NewForTesting(t, sqltest.PostgresDSN, Migrations())
	queries := internal.New(db)
	ctx := context.Background()

	// Create topic
	topic, err := queries.CreateTopic(ctx, internal.CreateTopicParams{
		Name:              "pending-count-topic",
		MaxRetries:        3,
		InitialBackoff:    internal.Duration(time.Minute),
		BackoffMax:        internal.Duration(5 * time.Minute),
		BackoffMultiplier: 2.0,
		DlqEnabled:        false,
		DlqMaxAge:         internal.Duration(7 * 24 * time.Hour),
	})
	assert.NoError(t, err)

	// Initially should be 0
	count, err := queries.GetPendingEventCount(ctx, topic.ID)
	assert.NoError(t, err)
	assert.Equal(t, int64(0), count)

	// Publish 3 events
	for i := range 3 {
		_, err = queries.PublishEvent(ctx, topic.ID, "pending-"+string(rune('1'+i)), json.RawMessage(`{}`), json.RawMessage(`{}`))
		assert.NoError(t, err)
	}

	// Should have 3 pending events
	count, err = queries.GetPendingEventCount(ctx, topic.ID)
	assert.NoError(t, err)
	assert.Equal(t, int64(3), count)

	// Claim one event
	_, err = queries.ClaimNextEvent(ctx, topic.ID)
	assert.NoError(t, err)

	// Should have 2 pending events
	count, err = queries.GetPendingEventCount(ctx, topic.ID)
	assert.NoError(t, err)
	assert.Equal(t, int64(2), count)
}

func TestGetEventStats(t *testing.T) {
	t.Parallel()
	db, _ := sqltest.NewForTesting(t, sqltest.PostgresDSN, Migrations())
	queries := internal.New(db)
	ctx := context.Background()

	// Create topic
	topic, err := queries.CreateTopic(ctx, internal.CreateTopicParams{
		Name:              "stats-topic",
		MaxRetries:        1,
		InitialBackoff:    internal.Duration(100 * time.Millisecond),
		BackoffMax:        internal.Duration(200 * time.Millisecond),
		BackoffMultiplier: 2.0,
		DlqEnabled:        true,
		DlqMaxAge:         internal.Duration(7 * 24 * time.Hour),
	})
	assert.NoError(t, err)

	// Publish events and manipulate their states
	eventID1, err := queries.PublishEvent(ctx, topic.ID, "stats-event-1", json.RawMessage(`{}`), json.RawMessage(`{}`))
	assert.NoError(t, err)

	_, err = queries.PublishEvent(ctx, topic.ID, "stats-event-2", json.RawMessage(`{}`), json.RawMessage(`{}`))
	assert.NoError(t, err)

	eventID3, err := queries.PublishEvent(ctx, topic.ID, "stats-event-3", json.RawMessage(`{}`), json.RawMessage(`{}`))
	assert.NoError(t, err)

	// Claim and complete one event
	_, err = queries.ClaimNextEvent(ctx, topic.ID)
	assert.NoError(t, err)
	_, err = queries.CompleteEvent(ctx, eventID1)
	assert.NoError(t, err)

	// Claim one event (leave it active) - this claims eventID2
	_, err = queries.ClaimNextEvent(ctx, topic.ID)
	assert.NoError(t, err)

	// Claim and fail one event multiple times until dead lettered
	_, err = queries.ClaimNextEvent(ctx, topic.ID)
	assert.NoError(t, err)

	// Fail twice to exhaust retries and dead letter
	_, err = queries.FailEvent(ctx, eventID3, "first failure")
	assert.NoError(t, err)

	// Wait for retry backoff time
	time.Sleep(150 * time.Millisecond)
	_, err = queries.ClaimNextEvent(ctx, topic.ID)
	assert.NoError(t, err)

	_, err = queries.FailEvent(ctx, eventID3, "second failure")
	assert.NoError(t, err)

	// Get stats
	stats, err := queries.GetEventStats(ctx, internal.Duration(5*time.Minute), topic.ID)
	assert.NoError(t, err)

	assert.Equal(t, int64(0), stats.PendingCount)    // No pending events
	assert.Equal(t, int64(1), stats.ActiveCount)     // One active event (eventID2)
	assert.Equal(t, int64(1), stats.SucceededCount)  // One completed event (eventID1)
	assert.Equal(t, int64(1), stats.FailedCount)     // One failed event (eventID3)
	assert.Equal(t, int64(1), stats.DeadLetterCount) // One dead lettered event (eventID3)
}

func TestCleanupOldDeadLetters(t *testing.T) {
	t.Parallel()
	db, _ := sqltest.NewForTesting(t, sqltest.PostgresDSN, Migrations())
	queries := internal.New(db)
	ctx := context.Background()

	// Create topic with very short DLQ max age for testing
	topic, err := queries.CreateTopic(ctx, internal.CreateTopicParams{
		Name:              "cleanup-topic",
		MaxRetries:        0, // No retries, direct to DLQ
		InitialBackoff:    internal.Duration(time.Minute),
		BackoffMax:        internal.Duration(5 * time.Minute),
		BackoffMultiplier: 2.0,
		DlqEnabled:        true,
		DlqMaxAge:         internal.Duration(time.Second), // 1 second for testing
	})
	assert.NoError(t, err)

	// Publish and immediately fail an event to create a dead letter
	eventID, err := queries.PublishEvent(ctx, topic.ID, "cleanup-event", json.RawMessage(`{}`), json.RawMessage(`{}`))
	assert.NoError(t, err)

	_, err = queries.ClaimNextEvent(ctx, topic.ID)
	assert.NoError(t, err)

	action, err := queries.FailEvent(ctx, eventID, "test failure")
	assert.NoError(t, err)
	assert.Equal(t, internal.PubsubFailActionDeadLettered, action)

	// Wait for the DLQ max age to pass
	time.Sleep(2 * time.Second)

	// Run cleanup
	err = queries.CleanupOldDeadLetters(ctx)
	assert.NoError(t, err)

	// Verify the dead letter was cleaned up by checking stats
	stats, err := queries.GetEventStats(ctx, internal.Duration(5*time.Minute), topic.ID)
	assert.NoError(t, err)
	assert.Equal(t, int64(0), stats.DeadLetterCount)
}

func TestClearStuckEvents(t *testing.T) {
	t.Parallel()
	db, _ := sqltest.NewForTesting(t, sqltest.PostgresDSN, Migrations())
	queries := internal.New(db)
	ctx := context.Background()

	// Create topic
	topic, err := queries.CreateTopic(ctx, internal.CreateTopicParams{
		Name:              "test-stuck-events",
		MaxRetries:        3,
		InitialBackoff:    internal.Duration(time.Minute),
		BackoffMax:        internal.Duration(5 * time.Minute),
		BackoffMultiplier: 2.0,
		DlqEnabled:        false,
		DlqMaxAge:         internal.Duration(7 * 24 * time.Hour),
	})
	assert.NoError(t, err)

	// Publish multiple events
	events := make([]int64, 3)
	for i := range 3 {
		eventID, err := queries.PublishEvent(ctx, topic.ID, fmt.Sprintf("stuck-event-%d", i), []byte(`{"message": "test"}`), []byte(`{}`))
		assert.NoError(t, err)
		events[i] = eventID
	}

	// Claim all events to make them active
	for i := range 3 {
		event, err := queries.ClaimNextEvent(ctx, topic.ID)
		assert.NoError(t, err)
		assert.Equal(t, internal.PubsubEventStateActive, event.State)
		assert.Equal(t, events[i], event.ID)
	}

	// Temporarily disable the last_updated trigger to allow manual timestamp manipulation
	_, err = db.ExecContext(ctx, `ALTER TABLE pubsub_events DISABLE TRIGGER update_pubsub_events_last_updated`)
	assert.NoError(t, err)

	// Manually update last_updated to simulate old stuck events
	// Update 2 events to be old enough to be considered stuck
	result, err := db.ExecContext(ctx, `
		UPDATE pubsub_events
		SET last_updated = CURRENT_TIMESTAMP - INTERVAL '10 minutes'
		WHERE id IN ($1, $2)
	`, events[0], events[1])
	assert.NoError(t, err)
	rowsAffected, err := result.RowsAffected()
	assert.NoError(t, err)
	assert.Equal(t, int64(2), rowsAffected, "Should have updated 2 events")

	// Re-enable the trigger
	_, err = db.ExecContext(ctx, `ALTER TABLE pubsub_events ENABLE TRIGGER update_pubsub_events_last_updated`)
	assert.NoError(t, err)

	// Verify the events are actually stuck by checking their state
	var stuckCount int
	err = db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM pubsub_events
		WHERE topic_id = $1 AND state = 'active' AND last_updated < CURRENT_TIMESTAMP - INTERVAL '5 minutes'
	`, topic.ID).Scan(&stuckCount)
	assert.NoError(t, err)
	assert.Equal(t, 2, stuckCount, "Should have 2 stuck events before clearing")

	// Clear stuck events (older than 5 minutes, max 2 events)
	clearedCount, err := queries.ClearStuckEvents(ctx, topic.ID, 2, internal.Duration(5*time.Minute))
	assert.NoError(t, err)
	assert.Equal(t, 2, clearedCount)

	// Verify the events are now pending
	pendingCount, err := queries.GetPendingEventCount(ctx, topic.ID)
	assert.NoError(t, err)
	assert.Equal(t, int64(2), pendingCount)

	// Verify event stats
	stats, err := queries.GetEventStats(ctx, internal.Duration(5*time.Minute), topic.ID)
	assert.NoError(t, err)
	assert.Equal(t, int64(0), stats.PendingCount)
	assert.Equal(t, int64(2), stats.RetryCount)  // Two events moved to retry state
	assert.Equal(t, int64(1), stats.ActiveCount) // One event should still be active
	assert.Equal(t, int64(0), stats.StuckCount)  // No events should be stuck (recent active event)
}

func TestClearStuckEventsWithLimits(t *testing.T) {
	t.Parallel()
	db, _ := sqltest.NewForTesting(t, sqltest.PostgresDSN, Migrations())
	queries := internal.New(db)
	ctx := context.Background()

	// Create topic
	topic, err := queries.CreateTopic(ctx, internal.CreateTopicParams{
		Name:              "test-stuck-events-limits",
		MaxRetries:        3,
		InitialBackoff:    internal.Duration(time.Minute),
		BackoffMax:        internal.Duration(5 * time.Minute),
		BackoffMultiplier: 2.0,
		DlqEnabled:        false,
		DlqMaxAge:         internal.Duration(7 * 24 * time.Hour),
	})
	assert.NoError(t, err)

	// Publish and claim 5 events
	events := make([]int64, 5)
	for i := range 5 {
		eventID, err := queries.PublishEvent(ctx, topic.ID, fmt.Sprintf("stuck-event-limit-%d", i), []byte(`{"message": "test"}`), []byte(`{}`))
		assert.NoError(t, err)
		events[i] = eventID

		// Claim the event
		_, err = queries.ClaimNextEvent(ctx, topic.ID)
		assert.NoError(t, err)
	}

	// Temporarily disable the last_updated trigger to allow manual timestamp manipulation
	_, err = db.ExecContext(ctx, `ALTER TABLE pubsub_events DISABLE TRIGGER update_pubsub_events_last_updated`)
	assert.NoError(t, err)

	// Make all events old enough to be stuck
	_, err = db.ExecContext(ctx, `
		UPDATE pubsub_events
		SET last_updated = CURRENT_TIMESTAMP - INTERVAL '10 minutes'
		WHERE topic_id = $1
	`, topic.ID)
	assert.NoError(t, err)

	// Re-enable the trigger
	_, err = db.ExecContext(ctx, `ALTER TABLE pubsub_events ENABLE TRIGGER update_pubsub_events_last_updated`)
	assert.NoError(t, err)

	// Clear only 3 stuck events (even though 5 are stuck)
	clearedCount, err := queries.ClearStuckEvents(ctx, topic.ID, 3, internal.Duration(5*time.Minute))
	assert.NoError(t, err)
	assert.Equal(t, 3, clearedCount)

	// Verify counts
	stats, err := queries.GetEventStats(ctx, internal.Duration(5*time.Minute), topic.ID)
	assert.NoError(t, err)
	assert.Equal(t, int64(0), stats.PendingCount)
	assert.Equal(t, int64(3), stats.RetryCount)  // 3 events moved to retry state
	assert.Equal(t, int64(2), stats.ActiveCount) // 2 events should still be active
	assert.Equal(t, int64(2), stats.StuckCount)  // 2 events should be stuck
}

func TestClearStuckEventsNoMatches(t *testing.T) {
	t.Parallel()
	db, _ := sqltest.NewForTesting(t, sqltest.PostgresDSN, Migrations())
	queries := internal.New(db)
	ctx := context.Background()

	// Create topic
	topic, err := queries.CreateTopic(ctx, internal.CreateTopicParams{
		Name:              "test-no-stuck-events",
		MaxRetries:        3,
		InitialBackoff:    internal.Duration(time.Minute),
		BackoffMax:        internal.Duration(5 * time.Minute),
		BackoffMultiplier: 2.0,
		DlqEnabled:        false,
		DlqMaxAge:         internal.Duration(7 * 24 * time.Hour),
	})
	assert.NoError(t, err)

	// Clear stuck events when there are none
	clearedCount, err := queries.ClearStuckEvents(ctx, topic.ID, 10, internal.Duration(5*time.Minute))
	assert.NoError(t, err)
	assert.Equal(t, 0, clearedCount)

	// Publish and claim an event, but it's recent (not stuck)
	_, err = queries.PublishEvent(ctx, topic.ID, "recent-event", []byte(`{"message": "test"}`), []byte(`{}`))
	assert.NoError(t, err)

	_, err = queries.ClaimNextEvent(ctx, topic.ID)
	assert.NoError(t, err)

	// Try to clear stuck events with a short duration (should not match the recent event)
	clearedCount, err = queries.ClearStuckEvents(ctx, topic.ID, 10, internal.Duration(5*time.Minute))
	assert.NoError(t, err)
	assert.Equal(t, 0, clearedCount)

	// Verify the event is still active
	stats, err := queries.GetEventStats(ctx, internal.Duration(5*time.Minute), topic.ID)
	assert.NoError(t, err)
	assert.Equal(t, int64(0), stats.PendingCount)
	assert.Equal(t, int64(1), stats.ActiveCount)
	assert.Equal(t, int64(0), stats.StuckCount) // Recent event should not be stuck
}

func TestRetryDeadLetterEvent(t *testing.T) {
	t.Parallel()
	db, _ := sqltest.NewForTesting(t, sqltest.PostgresDSN, Migrations())
	queries := internal.New(db)
	ctx := context.Background()

	tests := []struct {
		name            string
		dlqEnabled      bool
		setupDeadLetter bool
		expectSuccess   bool
	}{
		{
			name:            "RetryDeadLetteredEventSuccess",
			dlqEnabled:      true,
			setupDeadLetter: true,
			expectSuccess:   true,
		},
		{
			name:            "RetryNonExistentEvent",
			dlqEnabled:      true,
			setupDeadLetter: false,
			expectSuccess:   false,
		},
		{
			name:            "RetryEventWithoutDLQ",
			dlqEnabled:      false,
			setupDeadLetter: false,
			expectSuccess:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Create topic with DLQ configuration
			topic, err := queries.CreateTopic(ctx, internal.CreateTopicParams{
				Name:              "retry-dlq-topic-" + tt.name,
				MaxRetries:        0, // No retries to force dead lettering
				InitialBackoff:    internal.Duration(time.Minute),
				BackoffMax:        internal.Duration(5 * time.Minute),
				BackoffMultiplier: 2.0,
				DlqEnabled:        tt.dlqEnabled,
				DlqMaxAge:         internal.Duration(7 * 24 * time.Hour),
			})
			assert.NoError(t, err)

			cloudEventsID := "retry-test-event-" + tt.name
			var eventID int64

			if tt.setupDeadLetter {
				// Publish and claim event
				eventID, err = queries.PublishEvent(ctx, topic.ID, cloudEventsID, json.RawMessage(`{"test": "data"}`), json.RawMessage(`{"header": "value"}`))
				assert.NoError(t, err)

				_, err = queries.ClaimNextEvent(ctx, topic.ID)
				assert.NoError(t, err)

				// Fail the event to send it to dead letter queue
				action, err := queries.FailEvent(ctx, eventID, "test error for dead lettering")
				assert.NoError(t, err)
				if tt.dlqEnabled {
					assert.Equal(t, internal.PubsubFailActionDeadLettered, action)
				} else {
					assert.Equal(t, internal.PubsubFailActionFailed, action)
				}
			}

			// Try to retry the dead letter event
			success, err := queries.RetryDeadLetterEvent(ctx, cloudEventsID)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectSuccess, success)

			if tt.expectSuccess {
				// Verify event is back in pending state
				stats, err := queries.GetEventStats(ctx, internal.Duration(5*time.Minute), topic.ID)
				assert.NoError(t, err)
				assert.Equal(t, int64(1), stats.PendingCount)
				assert.Equal(t, int64(0), stats.FailedCount)
				assert.Equal(t, int64(0), stats.DeadLetterCount)

				// Verify we can claim and process the event again
				event, err := queries.ClaimNextEvent(ctx, topic.ID)
				assert.NoError(t, err)
				assert.Equal(t, cloudEventsID, event.CloudeventsID)
				assert.Equal(t, internal.PubsubEventStateActive, event.State)
			}
		})
	}
}
