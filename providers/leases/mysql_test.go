//go:build mysql

package leases

import "testing"

func TestMySQLLeaser(t *testing.T) {
	testSQLLeaser(t, "mysql://root:secret@tcp(localhost:3306)/zero?multiStatements=true")
}
