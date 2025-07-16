// Package zero contains the runtime for Zero's.
package zero

import (
	"context"
	"encoding/json"
	"runtime"
	"time"

	"github.com/alecthomas/errors"
	"github.com/alecthomas/zero/internal/cloudevent"
)

type EventPayload interface {
	// ID returns the unique identifier for the event.
	//
	// This is required for idempotence and deduplication in the face of multiple retries.
	ID() string
}

type Topic[T EventPayload] interface {
	// Publish publishes a message to the topic.
	Publish(ctx context.Context, msg T) error
	// Subscribe subscribes to a topic.
	Subscribe(ctx context.Context, handler func(context.Context, T) error) error
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
type Event[T EventPayload] struct {
	source  string
	created time.Time
	payload T
}

func NewEvent[T EventPayload](payload T) Event[T] {
	var source string
	pc, _, _, ok := runtime.Caller(1)
	if ok && pc != 0 {
		source = runtime.FuncForPC(pc).Name()
	}
	return Event[T]{
		source:  source,
		created: time.Now().UTC(),
		payload: payload,
	}
}

// ID returns the ID of the underlying payload.
func (e Event[T]) ID() string         { return e.payload.ID() }
func (e Event[T]) Source() string     { return e.source }
func (e Event[T]) Created() time.Time { return e.created }
func (e Event[T]) Payload() T         { return e.payload }

func (e Event[T]) MarshalJSON() ([]byte, error) {
	cloudEvent := cloudevent.New(e.source, e.created, e.payload)
	return json.MarshalIndent(cloudEvent, "", "  ")
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
