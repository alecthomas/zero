//go:build postgres

package leases

import "testing"

func TestPostgresLeaser(t *testing.T) {
	testSQLLeaser(t, "postgres://postgres:secret@localhost:5432/zero-test?sslmode=disable")
}
