package pubsub

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/alecthomas/assert/v2"
)

type User struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func (u User) EventID() string { return u.Name }

func TestEventSerialisation(t *testing.T) {
	synctest.Run(func() {
		e := NewEvent(User{Name: "Bob", Age: 30})
		data, err := e.MarshalJSON()
		assert.NoError(t, err)
		assert.Equal(t, `{
  "specversion": "1.0",
  "type": "github.com/alecthomas/zero/providers/pubsub.User",
  "source": "github.com/alecthomas/zero/providers/pubsub.TestEventSerialisation.func1",
  "time": "2000-01-01T00:00:00Z",
  "id": "Bob",
  "datacontenttype": "application/json; charset=utf-8",
  "data": {
    "name": "Bob",
    "age": 30
  }
}`, string(data))
	})
}

func testPubSub(t *testing.T, topic Topic[User]) { //nolint
	t.Cleanup(func() {
		assert.NoError(t, topic.Close())
	})

	var received0 atomic.Int32
	err := topic.Subscribe(t.Context(), "first", func(ctx context.Context, u User) error {
		received0.Add(1)
		return nil
	})
	assert.NoError(t, err)

	var received1 atomic.Int32
	err = topic.Subscribe(t.Context(), "first", func(ctx context.Context, u User) error {
		received1.Add(1)
		return nil
	})
	assert.NoError(t, err)

	var received2 atomic.Int32
	err = topic.Subscribe(t.Context(), "second", func(ctx context.Context, u User) error {
		received2.Add(1)
		return nil
	})
	assert.NoError(t, err)
	for range 16 {
		err = topic.Publish(t.Context(), User{
			Name: "Alice",
			Age:  30,
		})
		assert.NoError(t, err)
		time.Sleep(time.Millisecond * 50)
	}

	assert.True(t, received0.Load() > 4, "received0 = %d", received0.Load())
	assert.True(t, received1.Load() > 4, "received1 = %d", received1.Load())
	assert.Equal(t, received2.Load(), 16, "received2 = %d", received2.Load())
	assert.True(t, received0.Load()+received1.Load() == 16, "received0 = %d + received1 = %d", received0.Load(), received1.Load())
}
