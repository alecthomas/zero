package leases

import (
	"testing"

	"github.com/alecthomas/assert/v2"
	"github.com/alecthomas/zero/providers/leases/migrations"
	"github.com/alecthomas/zero/providers/logging/loggingtest"
	"github.com/alecthomas/zero/providers/sql/sqltest"
)

func testSQLLeaser(t *testing.T, dsn string) { //nolint
	logger := loggingtest.NewForTesting()
	db, driver := sqltest.NewForTesting(t, dsn, migrations.Migrations())
	leaser, err := NewSQLLeaser(t.Context(), logger, driver, db)
	assert.NoError(t, err)
	testLeases(t, leaser)
}
