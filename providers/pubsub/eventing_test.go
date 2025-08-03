package pubsub

import (
	"context"
	"testing"
	"testing/synctest"

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
  "type": "github.com/alecthomas/zero/providers/eventing.User",
  "source": "github.com/alecthomas/zero/providers/eventing.TestEvent.func1",
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

func testPubSub(t *testing.T, topic Topic[User]) {
	topic.Subscribe(t.Context(), func(ctx context.Context, u User) error {})
}
