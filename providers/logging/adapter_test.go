package logging_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/alecthomas/assert/v2"
	"github.com/alecthomas/zero/providers/logging"
)

func TestLegacy(t *testing.T) {
	tests := []struct {
		name            string
		level           slog.Level
		input           string
		expectedContent string
	}{
		{
			name:            "SingleLine",
			level:           slog.LevelInfo,
			input:           "Hello World",
			expectedContent: "Hello World",
		},
		{
			name:            "EmptyString",
			level:           slog.LevelError,
			input:           "",
			expectedContent: "",
		},
		{
			name:            "WithNewlines",
			level:           slog.LevelWarn,
			input:           "Line with\nnewlines",
			expectedContent: "Line with",
		},
		{
			name:            "SpecialChars",
			level:           slog.LevelDebug,
			input:           "Special !@#$%",
			expectedContent: "Special !@#$%",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{
				Level: slog.LevelDebug,
			})
			slogLogger := slog.New(handler)

			legacyLogger := logging.Legacy(slogLogger, tt.level)
			legacyLogger.Print(tt.input)

			output := buf.String()
			assert.True(t, strings.Contains(output, tt.expectedContent))
			assert.True(t, strings.Contains(output, "level="+tt.level.String()))

			// Special case for newlines test - verify both parts are logged separately
			if tt.name == "WithNewlines" {
				assert.True(t, strings.Contains(output, "newlines"))
				lines := strings.Split(strings.TrimSpace(output), "\n")
				assert.Equal(t, 2, len(lines))
			}
		})
	}
}

func TestLegacyMultipleWrites(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	slogLogger := slog.New(handler)

	legacyLogger := logging.Legacy(slogLogger, slog.LevelInfo)

	// Each Print call creates a separate log entry
	legacyLogger.Print("First")
	legacyLogger.Print("Second")

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	assert.Equal(t, 2, len(lines))
	assert.True(t, strings.Contains(output, "First"))
	assert.True(t, strings.Contains(output, "Second"))
}

func TestLegacyPrintMethods(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	slogLogger := slog.New(handler)

	legacyLogger := logging.Legacy(slogLogger, slog.LevelInfo)

	// Test Printf
	legacyLogger.Printf("Hello %s", "World")
	output := buf.String()
	assert.True(t, strings.Contains(output, "Hello World"))

	// Test Println
	buf.Reset()
	legacyLogger.Println("Test message")
	output = buf.String()
	assert.True(t, strings.Contains(output, "Test message"))
}

func TestLegacyLogLevels(t *testing.T) {
	levels := []slog.Level{
		slog.LevelDebug,
		slog.LevelInfo,
		slog.LevelWarn,
		slog.LevelError,
	}

	for _, level := range levels {
		t.Run(level.String(), func(t *testing.T) {
			var buf bytes.Buffer
			handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{
				Level: slog.LevelDebug,
			})
			slogLogger := slog.New(handler)

			legacyLogger := logging.Legacy(slogLogger, level)
			legacyLogger.Print("Test message\n")

			output := buf.String()
			assert.True(t, strings.Contains(output, "Test message"))
			assert.True(t, strings.Contains(output, "level="+level.String()))
		})
	}
}

func TestLegacyEmptyInput(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	slogLogger := slog.New(handler)

	legacyLogger := logging.Legacy(slogLogger, slog.LevelInfo)

	// Test empty string
	legacyLogger.Print("")
	output := buf.String()
	assert.True(t, strings.Contains(output, "level=INFO"))
	assert.True(t, strings.Contains(output, `msg=""`))
}

func TestLegacyWriterBuffering(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	slogLogger := slog.New(handler)

	legacyLogger := logging.Legacy(slogLogger, slog.LevelInfo)

	// Test that the underlying writer properly buffers across multiple writes
	// when newlines are split across write calls
	writer := legacyLogger.Writer()

	// Write partial content without newline
	writer.Write([]byte("Part 1 "))
	assert.Equal(t, "", strings.TrimSpace(buf.String()), "Should buffer without newline")

	// Write more content without newline
	writer.Write([]byte("Part 2 "))
	assert.Equal(t, "", strings.TrimSpace(buf.String()), "Should continue buffering")

	// Complete the line with newline
	writer.Write([]byte("Part 3\n"))
	output := buf.String()
	assert.True(t, strings.Contains(output, "Part 1 Part 2 Part 3"))

	// Test multiple complete lines in one write
	buf.Reset()
	writer.Write([]byte("Line 1\nLine 2\nLine 3\n"))
	output = buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	assert.Equal(t, 3, len(lines))
	assert.True(t, strings.Contains(output, "Line 1"))
	assert.True(t, strings.Contains(output, "Line 2"))
	assert.True(t, strings.Contains(output, "Line 3"))
}

func TestLegacyConcurrentWrites(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	slogLogger := slog.New(handler)

	legacyLogger := logging.Legacy(slogLogger, slog.LevelInfo)

	// Test that concurrent writes are properly synchronized
	done := make(chan bool, 2)

	go func() {
		legacyLogger.Print("Goroutine 1 line 1")
		legacyLogger.Print("Goroutine 1 line 2")
		done <- true
	}()

	go func() {
		legacyLogger.Print("Goroutine 2 line 1")
		legacyLogger.Print("Goroutine 2 line 2")
		done <- true
	}()

	// Wait for both goroutines
	<-done
	<-done

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	assert.Equal(t, 4, len(lines))

	// Verify all expected content is present
	assert.True(t, strings.Contains(output, "Goroutine 1 line 1"))
	assert.True(t, strings.Contains(output, "Goroutine 1 line 2"))
	assert.True(t, strings.Contains(output, "Goroutine 2 line 1"))
	assert.True(t, strings.Contains(output, "Goroutine 2 line 2"))
}
