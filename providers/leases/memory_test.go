package leases

import "testing"

func TestInMemoryLeaser(t *testing.T) {
	testLeases(t, NewMemoryLeaser())
}
