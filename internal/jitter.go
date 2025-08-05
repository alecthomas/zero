// Package internal contains internal packages for Zero.
package internal

import (
	"math/rand/v2"
	"time"
)

// Jitter n ± 10%
func Jitter(n time.Duration) time.Duration {
	ni := int64(n)
	return time.Duration(ni + rand.Int64N(ni/10) - ni/20) //nolint
}
