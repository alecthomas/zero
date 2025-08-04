package pubsub

import (
	"log/slog"
	"os"
	"testing"
)

func TestMemoryPubSub(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	topic := NewMemoryTopic[User](t.Context(), logger)
	testPubSub(t, topic)
}
