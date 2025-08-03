// Package zero contains the runtime for Zero's.
package zero

import (
	"net/http"

	"github.com/alecthomas/zero/providers/cron"
)

// ErrorHandler represents a function for handling errors from Zero's generated code.
type ErrorHandler func(w http.ResponseWriter, msg string, code int)

// Middleware is a convenience type for Zero middleware.
type Middleware func(http.Handler) http.Handler

// Container is the root type for a Zero system. It can be used directly as a [http.Handler].
type Container struct {
	*http.ServeMux

	// Cron will be nil if no cron jobs are registered.
	Cron *cron.Scheduler
}
