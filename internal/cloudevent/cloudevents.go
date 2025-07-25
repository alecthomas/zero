// Package cloudevent models CloudEvents.
package cloudevent

import (
	"reflect"
	"runtime/debug"
	"time"
)

var buildInfo *debug.BuildInfo

type Event[T interface{ ID() string }] struct {
	SpecVersion     string    `json:"specversion"`
	Type            string    `json:"type"`
	Source          string    `json:"source"`
	Time            time.Time `json:"time"`
	ID              string    `json:"id"`
	DataContentType string    `json:"datacontenttype"`
	Data            T         `json:"data"`
}

func New[T interface{ ID() string }](source string, created time.Time, data T) Event[T] {
	t := reflect.TypeFor[T]()
	typeName := t.PkgPath() + "." + t.Name()
	return Event[T]{
		SpecVersion:     "1.0",
		Type:            typeName,
		Source:          source,
		Time:            created,
		ID:              data.ID(),
		DataContentType: "application/json; charset=utf-8",
		Data:            data,
	}
}
