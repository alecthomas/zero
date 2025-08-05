// Package pubsubtest contains helper functions for testing pubsub.
package pubsubtest

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alecthomas/assert/v2"
	"github.com/alecthomas/zero/providers/pubsub"
)

type User struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func (u User) EventID() string { return u.Name }

// RunPubSubTest runs the test suite for a [PubSub] implementation.
func RunPubSubTest(t *testing.T, topic pubsub.Topic[User]) { //nolint
	t.Cleanup(func() {
		assert.NoError(t, topic.Close())
	})
	var received0 atomic.Int32
	var received1 atomic.Int32
	err := topic.Subscribe(t.Context(), func(ctx context.Context, u User) error {
		received0.Add(1)
		return nil
	})
	assert.NoError(t, err)
	err = topic.Subscribe(t.Context(), func(ctx context.Context, u User) error {
		received1.Add(1)
		return nil
	})
	assert.NoError(t, err)
	for i := range 16 {
		err = topic.Publish(t.Context(), User{
			Name: fmt.Sprintf("Alice %d", i),
			Age:  30,
		})
		assert.NoError(t, err)
		// For the in-memory pubsub, if we busy loop publish, sometimes all events go to a single receiver.
		time.Sleep(time.Millisecond * 100)
	}

	time.Sleep(time.Second)

	assert.True(t, received0.Load() > 4, "received0 = %d", received0.Load())
	assert.True(t, received1.Load() > 4, "received1 = %d", received1.Load())
	assert.True(t, received0.Load()+received1.Load() == 16, "received0 = %d + received1 = %d", received0.Load(), received1.Load())
}
