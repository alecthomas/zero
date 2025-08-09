package logging

import (
	"context"
	"log"
	"log/slog"
	"strings"
	"sync"
)

// Legacy creates a [log.Logger] that logs to the given [log/slog.Logger].
func Legacy(logger *slog.Logger, level slog.Level) *log.Logger {
	return log.New(&slogWriter{logger: logger}, "", 0)
}

type slogWriter struct {
	mu     sync.Mutex
	logger *slog.Logger
	level  slog.Level
	// Buffer by line
	buffer string
}

func (w *slogWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buffer += string(p)
	// Log all lines one at a time in the string up until the last \n, leaving the suffix in w.buffer
	if i := strings.LastIndexByte(w.buffer, '\n'); i != -1 {
		for line := range strings.SplitSeq(w.buffer[:i], "\n") {
			w.logger.Log(context.Background(), w.level, line)
		}
		w.buffer = w.buffer[i+1:]
	}
	return len(p), nil
}
