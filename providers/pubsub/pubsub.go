// Package pubsub contains implementations for eventing providers.
package pubsub

import (
	"context"
	"encoding/json"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/alecthomas/errors"
	"github.com/alecthomas/zero/internal/cloudevent"
	"github.com/alecthomas/zero/internal/strcase"
	"go.jetify.com/typeid/v2"
)

// DeadLetter represents a dead-lettered event.
//
// It is itself an event.
type DeadLetter[T any] struct {
	// Error that resulted in the event being dead-lettered.
	Error string `json:"error"`
	Event T      `json:"event"`
}

func (d DeadLetter[T]) EventID() string {
	if e, ok := any(d.Event).(EventPayload); ok {
		return e.EventID()
	}
	return NewID[T]()
}

// EventPayload _may_ be implemented by an event to specify an ID.
//
// If not present, a unique TypeID will be generated using [NewID].
type EventPayload interface {
	// EventID returns the unique identifier for the event.
	//
	// This is required for idempotence and deduplication in the face of multiple retries.
	EventID() string
}

// Topic represents a PubSub topic.
type Topic[T any] interface {
	// Publish publishes a message to the topic.
	Publish(ctx context.Context, msg T) error
	// Subscribe subscribes to a topic.
	Subscribe(ctx context.Context, handler func(context.Context, T) error) error
	// Close the topic.
	Close() error
}

// NewID returns a unique identifier for the given type.
//
// The string is a [TypeID](https://github.com/jetify-com/typeid), with the type name as the prefix.
func NewID[T any]() string {
	t := reflect.TypeFor[T]()
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	// CamelCase -> snake_case
	name := strings.ReplaceAll(strings.ToLower(strings.Join(strcase.Split(t.Name()), "_")), "__", "_")
	return typeid.MustGenerate(name).String()
}

// Event represents a typed CloudEvent.
//
// Marshals to/from a JSON CloudEvent (https://cloudevents.io/)
//
// eg.
//
//	{
//	  "specversion": "1.0",
//	  "type": "github.com/alecthomas/zero.User",
//	  "source": "github.com/alecthomas/zero.PublishUserEvent",
//	  "id": "Bob",
//	  "data": {"name": "Bob", "age": 30}
//	}
type Event[T any] struct {
	id      string // If the payload implements EventPayload, the ID is taken from the payload, otherwise one will be automatically generated.
	source  string
	created time.Time
	payload T
}

func NewEvent[T any](payload T) Event[T] {
	var source string
	pc, _, _, ok := runtime.Caller(1)
	if ok && pc != 0 {
		source = runtime.FuncForPC(pc).Name()
	}
	var id string
	if p, ok := any(payload).(EventPayload); ok {
		id = p.EventID()
	} else {
		id = NewID[T]()
	}
	return Event[T]{
		id:      id,
		source:  source,
		created: time.Now().UTC(),
		payload: payload,
	}
}

// ID returns the ID of the underlying payload.
func (e Event[T]) ID() string         { return e.id }
func (e Event[T]) Source() string     { return e.source }
func (e Event[T]) Created() time.Time { return e.created }
func (e Event[T]) Payload() T         { return e.payload }

func (e Event[T]) MarshalJSON() ([]byte, error) {
	cloudEvent := cloudevent.New(e.id, e.source, e.created, e.payload)
	return errors.WithStack2(json.MarshalIndent(cloudEvent, "", "  "))
}

func (e *Event[T]) UnmarshalJSON(data []byte) error {
	var ce cloudevent.Event[T]
	err := json.Unmarshal(data, &ce)
	if err != nil {
		return errors.Errorf("failed to unmarshal CloudEvent: %w", err)
	}
	e.source = ce.Source
	e.created = ce.Time
	e.payload = ce.Data
	return nil
}
