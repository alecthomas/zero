package zero

import (
	"testing"
	"testing/synctest"

	"github.com/alecthomas/assert/v2"
)

type User struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func (u User) ID() string { return u.Name }

func TestEvent(t *testing.T) {
	synctest.Run(func() {
		e := NewEvent(User{Name: "Bob", Age: 30})
		data, err := e.MarshalJSON()
		assert.NoError(t, err)
		assert.Equal(t, `{
  "specversion": "1.0",
  "type": "github.com/alecthomas/zero.User",
  "source": "github.com/alecthomas/zero.TestEvent.func1",
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
