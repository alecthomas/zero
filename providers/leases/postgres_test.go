//go:build postgres

package leases

import (
	"testing"

	"github.com/alecthomas/zero/providers/sql/sqltest"
)

func TestPostgresLeaser(t *testing.T) {
	testSQLLeaser(t, sqltest.PostgresDSN)
}
