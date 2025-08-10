package pubsub_test

import (
	"log/slog"
	"os"
	"testing"

	"github.com/alecthomas/zero/providers/pubsub"
	"github.com/alecthomas/zero/providers/pubsub/pubsubtest"
)

func TestMemoryPubSub(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	topic := pubsub.NewMemoryTopic[pubsubtest.User](logger)
	pubsubtest.RunPubSubTest(t, topic)
}
