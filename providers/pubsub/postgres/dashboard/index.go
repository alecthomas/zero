package dashboard

import (
	_ "embed"
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/alecthomas/zero/providers/pubsub/postgres/internal"
)

//go:embed index.gohtml
var index string
var indexTemplate = template.Must(template.New("providers/pubsub/postgres/dashboard/index.gohtml").Funcs(template.FuncMap{
	"timeAgo":         timeAgoFunc,
	"truncateEventID": truncateEventIDFunc,
	"truncateError":   truncateErrorFunc,
}).Parse(index))

type indexContext struct {
	Count  int64
	Events []internal.ListDeadLettersRow
}

// Helper functions for template rendering
func timeAgoFunc(t time.Time) string {
	duration := time.Since(t)

	if duration < time.Minute {
		return "just now"
	} else if duration < time.Hour {
		minutes := int(duration.Minutes())
		if minutes == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", minutes)
	} else if duration < 24*time.Hour {
		hours := int(duration.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	} else {
		days := int(duration.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}

func truncateEventIDFunc(eventID, topicName string) string {
	// Strip topic prefix if it exists
	eventID = strings.TrimPrefix(eventID, topicName+"_")

	const maxLength = 32
	if len(eventID) <= maxLength {
		return eventID
	}
	// Show first 27 characters + "…" + last 4 characters
	return eventID[:27] + "…" + eventID[len(eventID)-4:]
}

func truncateErrorFunc(s string) string {
	const maxLength = 100
	if len(s) <= maxLength {
		return s
	}
	return s[:maxLength] + "..."
}
