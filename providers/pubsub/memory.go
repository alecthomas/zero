package pubsub

import (
	"context"
	"log/slog"

	"github.com/alecthomas/errors"
)

type InMemoryTopic[T any] struct {
	logger   *slog.Logger
	messages chan Event[T]
}

// NewMemoryTopic creates a new in-memory [Topic].
//
//zero:provider weak
func NewMemoryTopic[T any](logger *slog.Logger) Topic[T] {
	return &InMemoryTopic[T]{
		logger:   logger,
		messages: make(chan Event[T], 128),
	}
}

var _ Topic[string] = (*InMemoryTopic[string])(nil)

func (i *InMemoryTopic[T]) Publish(ctx context.Context, msg Event[T]) error {
	select {
	case i.messages <- msg:
		return nil
	default:
		return errors.Errorf("failed to publish message, channel full")
	}
}

func (i *InMemoryTopic[T]) Subscribe(ctx context.Context, handler func(context.Context, Event[T]) error) error {
	go func() {
		for {
			select {
			case msg, ok := <-i.messages:
				if !ok {
					return
				}
				if err := handler(ctx, msg); err != nil {
					i.logger.Error("Failed to handle message", "error", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return nil
}

func (i *InMemoryTopic[T]) Close() error {
	close(i.messages)
	return nil
}
