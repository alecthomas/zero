//go:build mysql

package sql

import "testing"

func TestMySQL(t *testing.T) {
	testDB(t, "mysql://root:secret@tcp(localhost:3306)/zero?multiStatements=true")
}
