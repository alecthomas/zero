CREATE TYPE pubsub_event_state AS ENUM ('pending', 'active', 'succeeded', 'failed');

CREATE TYPE pubsub_fail_action AS ENUM ('retrying', 'dead_lettered', 'failed');

CREATE TABLE pubsub_topics (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  name VARCHAR(255) NOT NULL UNIQUE,
  -- Retry configuration
  max_retries BIGINT NOT NULL DEFAULT 0 CHECK (max_retries >= 0),
  initial_backoff INTERVAL NOT NULL DEFAULT '1 minute' CHECK (initial_backoff > INTERVAL '0'),
  backoff_max INTERVAL NOT NULL DEFAULT '5 minutes' CHECK (backoff_max > INTERVAL '0'),
  backoff_multiplier DOUBLE PRECISION NOT NULL DEFAULT 2.0 CHECK (backoff_multiplier >= 1.0),
  -- Dead letter queue configuration
  dlq_enabled BOOLEAN NOT NULL DEFAULT false,
  dlq_max_age INTERVAL NOT NULL DEFAULT '7 days' CHECK (dlq_max_age > INTERVAL '0')
);

-- Topic events
CREATE TABLE pubsub_events (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  last_updated TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  topic_id BIGINT NOT NULL REFERENCES pubsub_topics(id) ON DELETE CASCADE,
  state pubsub_event_state NOT NULL DEFAULT 'pending',
  -- Unique CloudEvents ID
  cloudevents_id VARCHAR(64) NOT NULL UNIQUE,
  message JSONB NOT NULL CHECK (pg_column_size(message) < 1048576),
  headers JSONB NOT NULL DEFAULT '{}' CHECK (pg_column_size(headers) < 65536)
);

-- Events that are being retried
CREATE TABLE pubsub_retries (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  event_id BIGINT NOT NULL UNIQUE REFERENCES pubsub_events(id) ON DELETE RESTRICT,
  retry_count BIGINT NOT NULL DEFAULT 0,
  next_attempt TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP + INTERVAL '1 minute'
);

-- Failed messages will end up in here.
CREATE TABLE pubsub_dead_letters (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  event_id BIGINT NOT NULL REFERENCES pubsub_events(id) ON DELETE RESTRICT,
  error_message TEXT NOT NULL
);

-- Trigger to notify the "pubsub_listener" topic that a new row has arrived in "pubsub_events" or when an event transitions to pending state. It will send id + topic name.
CREATE OR REPLACE FUNCTION pubsub_notify_listener() RETURNS TRIGGER AS $$
DECLARE
  topic_name VARCHAR(255);
BEGIN
  -- Notify on INSERT or when state changes to 'pending'
  IF TG_OP = 'INSERT' OR (TG_OP = 'UPDATE' AND NEW.state = 'pending' AND OLD.state != 'pending') THEN
    PERFORM pg_notify('pubsub_listener', json_build_object('id', NEW.id, 'topic', NEW.topic_id)::text);
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER notify_pubsub_listener_trigger
AFTER INSERT OR UPDATE ON pubsub_events
FOR EACH ROW EXECUTE PROCEDURE pubsub_notify_listener();

-- Function to publish an event, topic must already exist
CREATE OR REPLACE FUNCTION pubsub_publish_event(
  p_topic_id BIGINT,
  p_cloudevents_id VARCHAR(64),
  p_message JSONB,
  p_headers JSONB DEFAULT '{}'
) RETURNS BIGINT AS $$
DECLARE
  v_event_id BIGINT;
BEGIN
  -- Insert event
  INSERT INTO pubsub_events (topic_id, cloudevents_id, message, headers)
  VALUES (p_topic_id, p_cloudevents_id, p_message, p_headers)
  RETURNING id INTO v_event_id;

  RETURN v_event_id;
END;
$$ LANGUAGE plpgsql;

-- Function to claim the next pending event for processing
CREATE OR REPLACE FUNCTION pubsub_claim_next_event(p_topic_id BIGINT)
RETURNS TABLE (
  id BIGINT,
  created_at TIMESTAMP,
  last_updated TIMESTAMP,
  topic_id BIGINT,
  state pubsub_event_state,
  cloudevents_id VARCHAR(64),
  message JSONB,
  headers JSONB
) AS $$
DECLARE
  v_event_id BIGINT;
BEGIN
  -- Find and lock next available event
  -- We need to use a subquery to avoid FOR UPDATE with LEFT JOIN
  SELECT e.id INTO v_event_id
  FROM pubsub_events e
  WHERE e.state = 'pending'
    AND e.topic_id = p_topic_id
    AND e.id IN (
      SELECT ev.id
      FROM pubsub_events ev
      LEFT JOIN pubsub_retries r ON ev.id = r.event_id
      WHERE ev.state = 'pending'
        AND ev.topic_id = p_topic_id
        AND (r.id IS NULL OR r.next_attempt <= CURRENT_TIMESTAMP)
    )
  ORDER BY e.created_at ASC
  LIMIT 1
  FOR UPDATE SKIP LOCKED;

  IF v_event_id IS NULL THEN
    RETURN;
  END IF;

  -- Mark as active
  UPDATE pubsub_events SET state = 'active' WHERE pubsub_events.id = v_event_id;

  -- Return the event data only
  RETURN QUERY
  SELECT
    e.id,
    e.created_at,
    e.last_updated,
    e.topic_id,
    e.state,
    e.cloudevents_id,
    e.message,
    e.headers
  FROM pubsub_events e
  WHERE e.id = v_event_id;
END;
$$ LANGUAGE plpgsql;

-- Function to mark an event as successfully processed
CREATE OR REPLACE FUNCTION pubsub_complete_event(p_event_id BIGINT)
RETURNS BOOLEAN AS $$
DECLARE
  v_row_count BIGINT;
BEGIN
  -- Clean up any retry records
  DELETE FROM pubsub_retries WHERE event_id = p_event_id;

  -- Mark event as succeeded
  UPDATE pubsub_events
  SET state = 'succeeded'
  WHERE id = p_event_id AND state = 'active';

  GET DIAGNOSTICS v_row_count = ROW_COUNT;
  RETURN v_row_count > 0;
END;
$$ LANGUAGE plpgsql;

-- Function to handle event failure with retry logic and dead lettering
CREATE OR REPLACE FUNCTION pubsub_fail_event(
  p_event_id BIGINT,
  p_error_message TEXT
) RETURNS pubsub_fail_action AS $$
DECLARE
  v_topic_config RECORD;
  v_current_retry_count BIGINT := 0;
  v_can_retry BOOLEAN := FALSE;
  v_next_attempt TIMESTAMP;
BEGIN
  -- Get topic configuration and current retry count
  SELECT
    t.max_retries,
    t.initial_backoff,
    t.backoff_max,
    t.backoff_multiplier,
    t.dlq_enabled,
    COALESCE(r.retry_count, 0) as current_retries
  INTO v_topic_config
  FROM pubsub_events e
  JOIN pubsub_topics t ON e.topic_id = t.id
  LEFT JOIN pubsub_retries r ON e.id = r.event_id
  WHERE e.id = p_event_id;

  IF NOT FOUND THEN
    RAISE EXCEPTION 'Event not found: %', p_event_id;
  END IF;

  v_current_retry_count := v_topic_config.current_retries;
  v_can_retry := v_current_retry_count < v_topic_config.max_retries AND v_topic_config.max_retries > 0;

  IF v_can_retry THEN
    -- Calculate next attempt time with exponential backoff
    v_next_attempt := CURRENT_TIMESTAMP + LEAST(
      v_topic_config.initial_backoff * (v_topic_config.backoff_multiplier ^ v_current_retry_count),
      v_topic_config.backoff_max
    );

    -- Insert or update retry record
    INSERT INTO pubsub_retries (event_id, retry_count, next_attempt)
    VALUES (p_event_id, v_current_retry_count + 1, v_next_attempt)
    ON CONFLICT (event_id) DO UPDATE SET
      retry_count = EXCLUDED.retry_count,
      next_attempt = EXCLUDED.next_attempt;

    -- Keep event as pending for retry
    UPDATE pubsub_events SET state = 'pending' WHERE id = p_event_id AND state = 'active';

    RETURN 'retrying'::pubsub_fail_action;
  ELSE
    -- No more retries, handle dead lettering if enabled
    IF v_topic_config.dlq_enabled THEN
      INSERT INTO pubsub_dead_letters (event_id, error_message)
      VALUES (p_event_id, p_error_message);

      -- Clean up retry records and mark as failed
      DELETE FROM pubsub_retries WHERE event_id = p_event_id;
      UPDATE pubsub_events SET state = 'failed' WHERE id = p_event_id AND state = 'active';

      RETURN 'dead_lettered'::pubsub_fail_action;
    ELSE
      -- Clean up retry records and mark as failed
      DELETE FROM pubsub_retries WHERE event_id = p_event_id;
      UPDATE pubsub_events SET state = 'failed' WHERE id = p_event_id AND state = 'active';

      RETURN 'failed'::pubsub_fail_action;
    END IF;
  END IF;
END;
$$ LANGUAGE plpgsql;

-- Function to get pending events for a given topic
CREATE OR REPLACE FUNCTION pubsub_get_pending_events(p_topic_id BIGINT, p_limit BIGINT DEFAULT 1000)
RETURNS TABLE (
  id BIGINT,
  created_at TIMESTAMP,
  last_updated TIMESTAMP,
  topic_id BIGINT,
  state pubsub_event_state,
  cloudevents_id VARCHAR(64),
  message JSONB,
  headers JSONB
) AS $$
BEGIN
  RETURN QUERY
    SELECT
      e.id,
      e.created_at,
      e.last_updated,
      e.topic_id,
      e.state,
      e.cloudevents_id,
      e.message,
      e.headers
    FROM pubsub_events e
    LEFT JOIN pubsub_retries r ON e.id = r.event_id
    WHERE e.state = 'pending'
      AND e.topic_id = p_topic_id
      AND (r.id IS NULL OR r.next_attempt <= CURRENT_TIMESTAMP)
    ORDER BY e.created_at ASC
    LIMIT p_limit;
END;
$$ LANGUAGE plpgsql;

-- Function to clear stuck events that may have been left active by crashed subscribers
CREATE OR REPLACE FUNCTION pubsub_clear_stuck_events(
  p_topic_id BIGINT,
  p_count BIGINT,
  p_older_than INTERVAL
) RETURNS BIGINT AS $$
DECLARE
  v_updated_count BIGINT;
BEGIN
  -- Update active events that haven't been updated in the specified duration
  -- Add a 1-minute grace period to prevent race conditions with active processing
  UPDATE pubsub_events
  SET state = 'pending'
  WHERE id IN (
    SELECT id
    FROM pubsub_events
    WHERE topic_id = p_topic_id
      AND state = 'active'
      AND last_updated < CURRENT_TIMESTAMP - GREATEST(p_older_than, INTERVAL '1 minute')
    ORDER BY last_updated ASC
    LIMIT p_count
  );

  GET DIAGNOSTICS v_updated_count = ROW_COUNT;
  RETURN v_updated_count;
END;
$$ LANGUAGE plpgsql;

-- Function to automatically update last_updated column
CREATE OR REPLACE FUNCTION pubsub_update_last_updated() RETURNS TRIGGER AS $$
BEGIN
  NEW.last_updated = CURRENT_TIMESTAMP;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Trigger to automatically update last_updated column on pubsub_events updates
CREATE TRIGGER update_pubsub_events_last_updated
BEFORE UPDATE ON pubsub_events
FOR EACH ROW EXECUTE PROCEDURE pubsub_update_last_updated();

-- Performance indexes
CREATE INDEX idx_pubsub_events_topic_state_created
  ON pubsub_events(topic_id, state, created_at);

CREATE INDEX idx_pubsub_events_active_last_updated
  ON pubsub_events(topic_id, last_updated);

CREATE INDEX idx_pubsub_retries_next_attempt
  ON pubsub_retries(next_attempt);

CREATE INDEX idx_pubsub_events_state_topic
  ON pubsub_events(state, topic_id);
