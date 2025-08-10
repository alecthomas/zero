//go:build postgres

package postgres

import (
	"testing"

	"github.com/alecthomas/assert/v2"
	"github.com/alecthomas/zero/providers/logging/loggingtest"
	"github.com/alecthomas/zero/providers/pubsub/pubsubtest"
	"github.com/alecthomas/zero/providers/sql/sqltest"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresPubSubBaseline(t *testing.T) {
	t.Parallel()
	logger := loggingtest.NewForTesting()
	db, _ := sqltest.NewForTesting(t, sqltest.PostgresDSN, Migrations())
	listener, err := NewListener(t.Context(), logger, db)
	assert.NoError(t, err)
	topic, err := New(t.Context(), logger, listener, db, DefaultConfig[pubsubtest.User]())
	assert.NoError(t, err)
	pubsubtest.RunPubSubTest(t, topic)
}
