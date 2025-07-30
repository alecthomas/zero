//go:build sqlite

package leases

import "testing"

func TestSQLiteLeaser(t *testing.T) {
	testSQLLeaser(t, "sqlite://file:discard?mode=memory&cache=shared")
}
