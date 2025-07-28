// Package logging contains providers for common loggers.
package logging

import (
	"log/slog"
	"os"

	"github.com/lmittmann/tint"
)

//zero:config prefix="log-"
type SlogConfig struct {
	Level slog.Level `help:"The default logging level." default:"info"`
	JSON  bool       `help:"Enable JSON logging."`
}

//zero:provider weak
func ProvideSlogger(config SlogConfig) *slog.Logger {
	var handler slog.Handler
	if config.JSON {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: config.Level,
		})
	} else {
		handler = tint.NewHandler(os.Stdout, &tint.Options{
			Level:      config.Level,
			TimeFormat: "15:04:05",
		})
	}
	return slog.New(handler)
}
