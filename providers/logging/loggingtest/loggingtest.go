package loggingtest

import (
	"log/slog"
	"os"
)

func NewForTesting() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}
