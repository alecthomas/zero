// Package http provides HTTP-related providers for Zero.
package http

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/alecthomas/zero"
	"github.com/alecthomas/zero/providers/logging"
)

// DefaultErrorEncoder for otherwise unhandled errors. It can be overridden.
//
// The response will be JSON in the form:
//
//	{
//	  "error": "error message",
//	  "code": code
//	}
//
//zero:provider weak
func DefaultErrorEncoder() zero.ErrorEncoder { return zero.EncodeError }

// DefaultResponseEncoder encodes responses using the default Zero format. It can be overridden.
//
//zero:provider weak
func DefaultResponseEncoder() zero.ResponseEncoder { return zero.EncodeResponse }

// DefaultServeMux returns the default [http.ServeMux]. It can be overridden.
//
//zero:provider weak
func DefaultServeMux() *http.ServeMux {
	return http.NewServeMux()
}

//zero:config prefix="server-"
type Config struct {
	Bind string `help:"The address to bind the server to." default:"127.0.0.1:8080"`
}

//zero:provider weak
func DefaultServer(ctx context.Context, logger *slog.Logger, config Config, mux *http.ServeMux) *http.Server {
	return &http.Server{
		Addr:              config.Bind,
		Handler:           mux,
		BaseContext:       func(l net.Listener) context.Context { return ctx },
		ReadTimeout:       time.Second * 10,
		WriteTimeout:      time.Second * 10,
		ReadHeaderTimeout: time.Second * 5,
		ErrorLog:          logging.Legacy(logger, slog.LevelError),
	}
}
