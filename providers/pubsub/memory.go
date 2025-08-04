package pubsub

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/alecthomas/errors"
)

type InMemoryTopic[T any] struct {
	logger   *slog.Logger
	messages chan T

	lock          sync.Mutex
	subscriptions map[string]*inMemorySubscription[T]
}

type inMemorySubscription[T any] struct {
	channel     chan T
	subscribers atomic.Int32
}

// NewMemoryTopic creates a new in-memory [Topic].
//
//zero:provider weak
func NewMemoryTopic[T any](ctx context.Context, logger *slog.Logger) Topic[T] {
	i := &InMemoryTopic[T]{
		logger:        logger,
		messages:      make(chan T, 128),
		subscriptions: map[string]*inMemorySubscription[T]{},
	}
	go i.fanout(ctx)
	return i
}

var _ Topic[string] = (*InMemoryTopic[string])(nil)

func (i *InMemoryTopic[T]) fanout(ctx context.Context) {
	for {
		select {
		case msg, ok := <-i.messages:
			if !ok {
				return
			}
			i.lock.Lock()
			for name, subscription := range i.subscriptions {
				select {
				case subscription.channel <- msg:
				default:
					i.logger.Error("Failed to publish message to subscription", "subscription", name)
				}
			}
			i.lock.Unlock()

		case <-ctx.Done():
			return
		}
	}
}

func (i *InMemoryTopic[T]) Publish(ctx context.Context, msg T) error {
	select {
	case i.messages <- msg:
		return nil
	default:
		return errors.Errorf("failed to publish message, channel full")
	}
}

func (i *InMemoryTopic[T]) Subscribe(ctx context.Context, name string, handler func(context.Context, T) error) error {
	i.lock.Lock()
	subscription, ok := i.subscriptions[name]
	if !ok {
		subscription = &inMemorySubscription[T]{
			channel: make(chan T, 128),
		}
		i.subscriptions[name] = subscription
	}
	subscription.subscribers.Add(1)
	i.lock.Unlock()
	go func() {
		for {
			select {
			case msg, ok := <-subscription.channel:
				if !ok { // Topic is closed
					return
				}
				if err := handler(ctx, msg); err != nil {
					i.logger.Error("Failed to handle message", "error", err)
				}

			case <-ctx.Done():
				if subscription.subscribers.Add(-1) <= 0 {
					i.lock.Lock()
					if _, ok := i.subscriptions[name]; ok {
						close(subscription.channel)
						delete(i.subscriptions, name)
					}
					i.lock.Unlock()
				}
				return
			}
		}
	}()
	return nil
}

// Close the topic. Publish after Close will panic.
func (i *InMemoryTopic[T]) Close() error {
	i.lock.Lock()
	close(i.messages)
	for name, subscription := range i.subscriptions {
		close(subscription.channel)
		delete(i.subscriptions, name)
	}
	i.lock.Unlock()
	return nil
}
