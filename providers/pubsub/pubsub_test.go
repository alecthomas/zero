package pubsub_test

import (
	"testing"
	"testing/synctest"

	"github.com/alecthomas/assert/v2"
	"github.com/alecthomas/zero/providers/pubsub"
	"github.com/alecthomas/zero/providers/pubsub/pubsubtest"
)

func TestEventSerialisation(t *testing.T) {
	synctest.Run(func() {
		e := pubsub.NewEvent(pubsubtest.User{Name: "Bob", Age: 30})
		data, err := e.MarshalJSON()
		assert.NoError(t, err)
		assert.Equal(t, `{
  "specversion": "1.0",
  "type": "github.com/alecthomas/zero/providers/pubsub/pubsubtest.User",
  "source": "github.com/alecthomas/zero/providers/pubsub_test.TestEventSerialisation.func1",
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
