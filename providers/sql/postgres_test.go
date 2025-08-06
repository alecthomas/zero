//go:build postgres

package sql_test

import "testing"

func TestPostgres(t *testing.T) {
	testDB(t, "postgres://postgres:secret@localhost:5432/zero-test?sslmode=disable")
}
