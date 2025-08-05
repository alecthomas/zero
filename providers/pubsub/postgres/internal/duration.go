//go:build postgres

package internal

import (
	"database/sql/driver"
	"fmt"
	"strings"
	"time"
)

// Duration is a wrapper around time.Duration that implements SQL driver interfaces
// for PostgreSQL INTERVAL type compatibility.
type Duration time.Duration

// Scan implements the sql.Scanner interface for reading from the database.
func (d *Duration) Scan(value any) error {
	if value == nil {
		*d = Duration(0)
		return nil
	}

	switch v := value.(type) {
	case string:
		// PostgreSQL returns intervals as strings like "00:01:00"
		dur, err := time.ParseDuration(convertPostgresInterval(v))
		if err != nil {
			return fmt.Errorf("failed to parse interval %q: %w", v, err)
		}
		*d = Duration(dur)
		return nil
	case time.Duration:
		*d = Duration(v)
		return nil
	case int64:
		// Fallback for nanosecond representation
		*d = Duration(time.Duration(v))
		return nil
	default:
		return fmt.Errorf("cannot scan %T into Duration", value)
	}
}

// Value implements the driver.Valuer interface for writing to the database.
func (d Duration) Value() (driver.Value, error) {
	return time.Duration(d), nil
}

// convertPostgresInterval converts PostgreSQL interval format to Go duration format.
// PostgreSQL intervals can be in various formats:
//   - "HH:MM:SS" (e.g., "01:30:45", "168:00:00")
//   - "N days" (e.g., "7 days", "1 day")
//   - "N hours" (e.g., "5 hours")
//   - "N minutes" (e.g., "30 minutes")
//   - "N seconds" (e.g., "45 seconds")
//   - Mixed formats (e.g., "1 day 02:30:00")
func convertPostgresInterval(pgInterval string) string {
	if pgInterval == "" {
		return "0s"
	}

	var totalDuration time.Duration

	// Split by spaces to handle mixed formats like "1 day 02:30:00"
	parts := strings.Fields(pgInterval)

	for i := 0; i < len(parts); i++ {
		part := parts[i]

		// Handle "HH:MM:SS" or "HH:MM:SS.fff" format
		if strings.Contains(part, ":") && len(strings.Split(part, ":")) == 3 {
			// Try parsing with fractional seconds first
			var hours, minutes int
			var seconds float64
			if n, _ := fmt.Sscanf(part, "%d:%d:%f", &hours, &minutes, &seconds); n == 3 {
				totalDuration += time.Duration(hours)*time.Hour +
					time.Duration(minutes)*time.Minute +
					time.Duration(seconds*float64(time.Second))
			} else {
				// Fallback to integer seconds
				var secondsInt int
				if n, _ := fmt.Sscanf(part, "%d:%d:%d", &hours, &minutes, &secondsInt); n == 3 {
					totalDuration += time.Duration(hours)*time.Hour +
						time.Duration(minutes)*time.Minute +
						time.Duration(secondsInt)*time.Second
				}
			}
			continue
		}

		// Handle numeric values with units
		if i+1 < len(parts) {
			var value int
			if n, _ := fmt.Sscanf(part, "%d", &value); n == 1 {
				unit := parts[i+1]
				switch {
				case strings.HasPrefix(unit, "day"):
					totalDuration += time.Duration(value) * 24 * time.Hour
					i++ // Skip the unit part
				case strings.HasPrefix(unit, "hour"):
					totalDuration += time.Duration(value) * time.Hour
					i++ // Skip the unit part
				case strings.HasPrefix(unit, "minute"):
					totalDuration += time.Duration(value) * time.Minute
					i++ // Skip the unit part
				case strings.HasPrefix(unit, "second"):
					totalDuration += time.Duration(value) * time.Second
					i++ // Skip the unit part
				}
			}
		}
	}

	if totalDuration > 0 {
		return totalDuration.String()
	}

	// If we can't parse it, return as-is and let time.ParseDuration handle it
	return pgInterval
}

// AsDuration returns the underlying time.Duration value.
func (d Duration) AsDuration() time.Duration {
	return time.Duration(d)
}

// String returns the string representation of the duration.
func (d Duration) String() string {
	return time.Duration(d).String()
}
