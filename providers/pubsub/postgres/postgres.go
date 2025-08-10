//go:build postgres

package postgres

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"io/fs"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/alecthomas/errors"
	"github.com/alecthomas/kong"
	zerointernal "github.com/alecthomas/zero/internal"
	"github.com/alecthomas/zero/providers/pubsub"
	"github.com/alecthomas/zero/providers/pubsub/postgres/internal"
	zerosql "github.com/alecthomas/zero/providers/sql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jpillora/backoff"
)

//go:embed migrations/*.sql
var pgMigrations embed.FS

// Migrations returns a set of migrations for the PostgreSQL PubSub provider.
//
//zero:provider multi
func Migrations() zerosql.Migrations {
	sub, err := fs.Sub(pgMigrations, "migrations")
	if err != nil {
		panic(err)
	}
	return zerosql.Migrations{sub}
}

type ListenerCallback func(ctx context.Context, notification Notification) error

// Listener issues a LISTEN command to the PostgreSQL database and fans out notifications to individual topics.
//
// It consumes a single connection.
type Listener struct {
	db         *sql.DB
	listenConn *pgx.Conn
	logger     *slog.Logger
	lock       sync.Mutex
	listeners  map[int64]ListenerCallback
}

// NewListener issues a LISTEN command to the PostgreSQL database and fans out notifications to local listeners.
//
// It consumes a single connection.
//
//zero:provider
func NewListener(ctx context.Context, logger *slog.Logger, db *sql.DB) (*Listener, error) {
	// We need a pgx.Conn to wait for notifications, so we need to explicitly unwrap the underlying connection.
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	var pgxConn *pgx.Conn
	err = conn.Raw(func(driverConn any) error {
		conn, ok := driverConn.(*stdlib.Conn)
		if !ok {
			return errors.Errorf("unexpected driver connection type %T, expected *pgx/v5/stdlib.Conn", driverConn)
		}
		pgxConn = conn.Conn()
		return nil
	})
	if err != nil {
		return nil, errors.WithStack(err)
	}
	_, err = pgxConn.Exec(ctx, "LISTEN pubsub_listener")
	if err != nil {
		return nil, errors.WithStack(err)
	}
	pgl := &Listener{listenConn: pgxConn, logger: logger, listeners: map[int64]ListenerCallback{}}
	go pgl.waitForNotifications(ctx)
	return pgl, nil
}

// Notification represents a notification received from the PostgreSQL database.
type Notification struct {
	ID    int64 `json:"id"`
	Topic int64 `json:"topic"`
}

// Listen registers a listener for a given topic.
//
// If a listener is already registered for the topic, an error is returned.
func (l *Listener) Listen(ctx context.Context, topic int64, listener ListenerCallback) error {
	l.lock.Lock()
	defer l.lock.Unlock()
	_, ok := l.listeners[topic]
	if ok {
		return errors.Errorf("listener already registered for topic %q", topic)
	}
	l.listeners[topic] = listener
	return nil
}

func (l *Listener) Unlisten(ctx context.Context, topic int64) error {
	l.lock.Lock()
	defer l.lock.Unlock()
	_, ok := l.listeners[topic]
	if !ok {
		return errors.Errorf("no listener registered for topic %q", topic)
	}
	delete(l.listeners, topic)
	return nil
}

func (l *Listener) waitForNotifications(ctx context.Context) {
	retry := backoff.Backoff{Min: time.Second * 5, Max: time.Second * 30}
	for {
		pgn, err := l.listenConn.WaitForNotification(ctx)
		if err != nil {
			// Context cancelled, just terminate.
			if ctx.Err() != nil {
				return
			}
			l.logger.Error("Error waiting for notification", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(retry.Duration()):
				continue
			}
		} else {
			retry.Reset()
		}

		var notification Notification
		err = json.Unmarshal([]byte(pgn.Payload), &notification)
		if err != nil {
			l.logger.Error("Invalid notification structure on PG pubsub_listener topic", "error", err, "payload", pgn.Payload)
			continue
		}
		l.lock.Lock()
		listener, ok := l.listeners[notification.Topic]
		l.lock.Unlock()
		if !ok {
			l.logger.Error("No listener registered for topic", "topic", notification.Topic)
			continue
		}
		err = listener(ctx, notification)
		if err != nil {
			l.logger.Error("Error processing notification", "error", err)
		}
	}
}

type RetryConfig struct {
	Retries  int           `help:"Maximum number of retries for failed messages (0 is disabled)." default:"0"`
	Min      time.Duration `help:"Minimum backoff duration for failed messages." default:"5s"`
	Max      time.Duration `help:"Maximum backoff duration for failed messages." default:"15s"`
	Exponent float64       `help:"Exponent for backoff duration for failed messages." default:"1.2"`
}

type DeadLetterConfig struct {
	Enabled  bool          `help:"Enable dead letter queue for failed messages." negatable:""`
	Lifetime time.Duration `help:"Maximum age for messages in the dead letter queue." default:"120h"`
}

// Config for a Postgres topic.
//
//zero:config prefix="topic-${type}-"
type Config[T any] struct {
	RetryConfig      `prefix:"backoff-"`
	DeadLetterConfig `prefix:"dlq-"`
}

// DefaultConfig creates a default configuration for a Postgres topic.
func DefaultConfig[T any]() Config[T] {
	config := Config[T]{}
	err := kong.ApplyDefaults(&config)
	if err != nil {
		panic(err)
	}
	return config
}

type Topic[T any] struct {
	logger      *slog.Logger
	topic       string
	topicID     int64
	listener    *Listener
	queries     *internal.Queries
	lock        sync.RWMutex
	subscribers []func(context.Context, pubsub.Event[T]) error
}

var _ pubsub.Topic[string] = (*Topic[string])(nil)

// New creates a new [pubsub.Topic] backed by Postgres.
//
//zero:provider
func New[T any](
	ctx context.Context,
	logger *slog.Logger,
	listener *Listener,
	db *sql.DB,
	config Config[T],
) (pubsub.Topic[T], error) {
	topic := pubsub.TopicName[T]()
	logger.Debug("Registered topic",
		"topic", topic,
		"backoff-retries", config.RetryConfig.Retries,
		"backoff-min", config.RetryConfig.Min,
		"backoff-max", config.RetryConfig.Max,
		"backoff-exponent", config.RetryConfig.Exponent,
		"dlq-enabled", config.DeadLetterConfig.Enabled,
		"dlq-lifetime", config.DeadLetterConfig.Lifetime,
	)
	queries := internal.New(db)
	topicRow, err := queries.CreateTopic(ctx, internal.CreateTopicParams{
		Name:              topic,
		MaxRetries:        int64(config.RetryConfig.Retries),
		InitialBackoff:    internal.Duration(config.RetryConfig.Min),
		BackoffMax:        internal.Duration(config.RetryConfig.Max),
		BackoffMultiplier: config.RetryConfig.Exponent,
		DlqEnabled:        config.DeadLetterConfig.Enabled,
		DlqMaxAge:         internal.Duration(config.DeadLetterConfig.Lifetime),
	})
	if err != nil {
		return nil, errors.Errorf("failed to create topic %q: %w", topic, err)
	}
	t := &Topic[T]{
		logger:   logger,
		queries:  queries,
		topic:    topic,
		topicID:  topicRow.ID,
		listener: listener,
	}

	// Start the listener
	err = listener.Listen(ctx, topicRow.ID, t.notified)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	// Start periodic check for backlog
	go t.processBacklog(ctx)

	return t, nil
}

// Periodically check for any events that are unprocessed. This can occur if subscribers are offline during publishing,
// or if PG NOTIFY's are dropped.
func (t *Topic[T]) processBacklog(ctx context.Context) {
	retry := backoff.Backoff{Min: time.Second * 5, Max: time.Second * 30}
	for {
		delay := zerointernal.Jitter(time.Second * 5)

		processed, err := t.processOneBacklogEvent(ctx)
		if err != nil {
			t.logger.Error("Backlog processing failed", "error", err)
			delay = retry.Duration()
		} else if processed {
			// If we successfully process an event, immediately try to process another one under the assumption
			// that there's more in the backlog. Once we hit the end of the backlog the delay will kick in.
			continue
		}

		select {
		case <-ctx.Done():
			return

		case <-time.After(delay):
		}
	}
}

func (t *Topic[T]) processOneBacklogEvent(ctx context.Context) (processed bool, err error) {
	eventRow, err := t.queries.ClaimNextEvent(ctx, t.topicID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, errors.Errorf("failed to get pending events for topic %s: %w", t.topic, err)
	}
	var event pubsub.Event[T]
	if err := json.Unmarshal(eventRow.Message, &event); err != nil {
		return false, errors.Errorf("failed to unmarshal event %d on topic %s: %w", eventRow.ID, t.topic, err)
	}
	if err := t.processEvent(ctx, eventRow.ID, event); err != nil {
		return false, errors.Errorf("failed to process event %d on topic %s: %w", eventRow.ID, t.topic, err)
	}
	return true, nil
}

// Called when the LISTENER receives a notification
func (t *Topic[T]) notified(ctx context.Context, notification Notification) error {
	if notification.Topic != t.topicID {
		return nil
	}
	// Pick a random subscriber
	t.lock.RLock()
	subscribers := len(t.subscribers)
	t.lock.RUnlock()
	if subscribers == 0 {
		return nil
	}

	// Claim an event
	eventRow, err := t.queries.ClaimNextEvent(ctx, t.topicID)
	if err != nil {
		return errors.Errorf("failed to claim next event from topic %q: %w", t.topic, err)
	}
	var event pubsub.Event[T]
	err = json.Unmarshal(eventRow.Message, &event)
	if err != nil {
		return errors.Errorf("failed to unmarshal event %d from topic %q: %w", eventRow.ID, t.topic, err)
	}
	return errors.WithStack(t.processEvent(ctx, eventRow.ID, event))
}

func (t *Topic[T]) processEvent(ctx context.Context, eventID int64, event pubsub.Event[T]) error {
	t.lock.RLock()
	if len(t.subscribers) == 0 {
		t.lock.RUnlock()
		return errors.New("no subscribers")
	}
	subscriber := t.subscribers[rand.IntN(len(t.subscribers))] //nolint
	t.lock.RUnlock()

	// Have the event, send it to a subscriber
	err := subscriber(ctx, event)
	if err != nil {
		_, ferr := t.queries.FailEvent(ctx, eventID, err.Error())
		if ferr != nil {
			err = errors.Join(err, errors.Wrapf(ferr, "failed to mark event %d as failed", eventID))
		}
		return errors.Errorf("failed to send event %d to subscriber: %w", eventID, err)
	}
	_, err = t.queries.CompleteEvent(ctx, eventID)
	return errors.Wrapf(err, "failed to mark event %d as complete", eventID)
}

func (t *Topic[T]) Close() error {
	return errors.WithStack(t.listener.Unlisten(context.Background(), t.topicID))
}

func (t *Topic[T]) Publish(ctx context.Context, event pubsub.Event[T]) error {
	data, err := json.Marshal(event)
	if err != nil {
		return errors.Wrapf(err, "failed to marshal event %s", event.ID())
	}
	_, err = t.queries.PublishEvent(ctx, t.topicID, event.ID(), data, []byte("{}"))
	return errors.Wrapf(err, "failed to publish event %s to topic %s", event.ID(), t.topic)
}

func (t *Topic[T]) Subscribe(ctx context.Context, handler func(context.Context, pubsub.Event[T]) error) error {
	t.lock.Lock()
	defer t.lock.Unlock()
	t.subscribers = append(t.subscribers, handler)
	return nil
}
