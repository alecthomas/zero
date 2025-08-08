// Package zero contains the runtime for Zero's.
package zero

import (
	"net/http"

	"github.com/alecthomas/zero/providers/cron"
)

// Container is the root type for a Zero system. It can be used directly as a [http.Handler].
type Container struct {
	*http.ServeMux

	// Cron will be nil if no cron jobs are registered.
	Cron *cron.Scheduler
}
