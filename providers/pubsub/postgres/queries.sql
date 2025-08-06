-- CreateTopic creates or updates a topic with the given configuration.
-- name: CreateTopic :one
INSERT INTO pubsub_topics (name, max_retries, initial_backoff, backoff_max, backoff_multiplier, dlq_enabled, dlq_max_age)
VALUES (sqlc.arg(name), sqlc.arg(max_retries), sqlc.arg(initial_backoff), sqlc.arg(backoff_max), sqlc.arg(backoff_multiplier), sqlc.arg(dlq_enabled), sqlc.arg(dlq_max_age))
ON CONFLICT (name) DO UPDATE SET
  max_retries = EXCLUDED.max_retries,
  initial_backoff = EXCLUDED.initial_backoff,
  backoff_max = EXCLUDED.backoff_max,
  backoff_multiplier = EXCLUDED.backoff_multiplier,
  dlq_enabled = EXCLUDED.dlq_enabled,
  dlq_max_age = EXCLUDED.dlq_max_age
RETURNING *;

-- GetTopicByName retrieves a topic by its name.
-- name: GetTopicByName :one
SELECT * FROM pubsub_topics WHERE name = sqlc.arg(name);

-- PublishEvent publishes a new event to the specified topic and returns the event ID.
-- name: PublishEvent :one
SELECT pubsub_publish_event(sqlc.arg(topic_id), sqlc.arg(cloudevents_id), sqlc.arg(message), sqlc.arg(headers)) as event_id;

-- ClaimNextEvent atomically claims the next pending event for processing using UPDATE SKIP LOCKED
-- to avoid lock contention. It finds the oldest pending event (or retry-ready event), marks it as
-- 'active', and returns the event data only.
-- name: ClaimNextEvent :one
SELECT
  id::BIGINT,
  created_at::TIMESTAMP,
  last_updated::TIMESTAMP,
  topic_id::BIGINT,
  state::pubsub_event_state,
  cloudevents_id::VARCHAR(64),
  message::JSONB,
  headers::JSONB
FROM pubsub_claim_next_event(sqlc.arg(topic_id));

-- CompleteEvent marks an event as successfully processed.
-- name: CompleteEvent :one
SELECT pubsub_complete_event(sqlc.arg(event_id)) as success;

-- FailEvent handles event failure with configurable retry logic and dead lettering.
-- It checks the topic's retry configuration and current retry count to determine if the
-- event should be retried (with exponential backoff) or dead lettered. Returns the action
-- taken: 'retrying' (event moved to pending with next_attempt time), 'dead_lettered'
-- (moved to dead letter queue), or 'failed' (marked as failed without dead lettering).
-- name: FailEvent :one
SELECT pubsub_fail_event(sqlc.arg(event_id), sqlc.arg(error_message))::pubsub_fail_action as action_taken;



-- CleanupOldDeadLetters removes dead letter entries that have exceeded their max age.
-- name: CleanupOldDeadLetters :exec
DELETE FROM pubsub_dead_letters
WHERE id IN (
  SELECT dl.id
  FROM pubsub_dead_letters dl
  JOIN pubsub_events e ON dl.event_id = e.id
  JOIN pubsub_topics t ON e.topic_id = t.id
  WHERE dl.created_at < CURRENT_TIMESTAMP - t.dlq_max_age
);

-- GetPendingEventCount returns the number of events ready for processing in a topic.
-- name: GetPendingEventCount :one
SELECT COUNT(*) as count
FROM pubsub_events e
LEFT JOIN pubsub_retries r ON e.id = r.event_id
WHERE e.state = 'pending'
  AND e.topic_id = sqlc.arg(topic_id)
  AND (r.id IS NULL OR r.next_attempt <= CURRENT_TIMESTAMP);

-- GetEventStats returns comprehensive statistics for a topic.
-- name: GetEventStats :one
SELECT
  COUNT(*) FILTER (WHERE e.state = 'pending') as pending_count,
  COUNT(*) FILTER (WHERE e.state = 'active') as active_count,
  COUNT(*) FILTER (WHERE e.state = 'succeeded') as succeeded_count,
  COUNT(*) FILTER (WHERE e.state = 'failed') as failed_count,
  COUNT(*) FILTER (WHERE e.state = 'active' AND e.last_updated < (CURRENT_TIMESTAMP - sqlc.arg(stuck_threshold)::INTERVAL)) as stuck_count,
  COUNT(dl.id) as dead_letter_count
FROM pubsub_events e
LEFT JOIN pubsub_dead_letters dl ON e.id = dl.event_id
WHERE e.topic_id = sqlc.arg(topic_id);

-- ClearStuckEvents transitions stuck active events back to pending state.
-- This is used to recover from crashed subscribers that left events in active state.
-- name: ClearStuckEvents :one
SELECT pubsub_clear_stuck_events(sqlc.arg(topic_id), sqlc.arg(count), sqlc.arg(older_than)) as cleared_count;
