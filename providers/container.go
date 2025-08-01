package providers

import (
	"net/http"

	"github.com/alecthomas/zero"
	"github.com/alecthomas/zero/providers/cron"
)

// NewContainer creates a new [Container] instance.
//
//zero:provider
func NewContainer(mux *http.ServeMux, cron *cron.Scheduler) *zero.Container {
	return &zero.Container{ServeMux: mux, Cron: cron}
}
