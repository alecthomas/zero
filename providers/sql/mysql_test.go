//go:build mysql

package sql_test

import "testing"

func TestMySQL(t *testing.T) {
	testDB(t, "mysql://root:secret@tcp(localhost:3306)/zero?multiStatements=true")
}
