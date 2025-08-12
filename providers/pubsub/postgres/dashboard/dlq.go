// Package dashboard provides dashboard components for the PostgreSQL PubSub provider.
package dashboard

import (
	"context"
	"database/sql"
	_ "embed"
	"log/slog"

	"github.com/alecthomas/errors"
	"github.com/alecthomas/zero/providers/dashboard"
	"github.com/alecthomas/zero/providers/pubsub/postgres/internal"
)

//zero:provider weak multi
func Provide(dlq *Component) dashboard.Components {
	return dashboard.Components{dlq}
}

//zero:provider weak require=Provide
func New(logger *slog.Logger, db *sql.DB) *Component {
	return &Component{queries: internal.New(db)}
}

type Component struct {
	queries  *internal.Queries
	renderer *dashboard.Renderer
}

var _ dashboard.Component = (*Component)(nil)

func (d *Component) Children() dashboard.Components { return nil }

func (d *Component) Detail() dashboard.Detail {
	return dashboard.Detail{Icon: "filter", Title: "DLQ", Slug: "dlq"}
}

func (d *Component) SetRenderer(renderer *dashboard.Renderer) {
	d.renderer = renderer
}

type DQLListQuery struct {
	Offset int32 `qstring:"offset"`
	Limit  int32 `qstring:"limit"`
}

//zero:api GET /_admin/dlq/
func (d *Component) Index(ctx context.Context, query DQLListQuery) (string, error) {
	count, err := d.queries.DeadLetterCount(ctx)
	if err != nil {
		return "", errors.WithStack(err)
	}
	if query.Limit == 0 {
		query.Limit = 50
	}
	events, err := d.queries.ListDeadLetters(ctx, query.Offset, query.Limit)
	if err != nil {
		return "", errors.WithStack(err)
	}
	return errors.WithStack2(d.renderer.RenderTemplate(ctx, indexTemplate, indexContext{
		Count:  count,
		Events: events,
	}))
}

//zero:api DELETE /_admin/api/dlq/{cloudEventID} dashboard
func (d *Component) DeleteDeadLetter(ctx context.Context, cloudEventID string) error {
	_, err := d.queries.DeleteDeadLetter(ctx, cloudEventID)
	return errors.WithStack(err)
}

//zero:api POST /_admin/api/dlq/{cloudEventID}/retry dashboard
func (d *Component) ReenqueueDeadLetter(ctx context.Context, cloudEventID string) error {
	_, err := d.queries.RetryDeadLetterEvent(ctx, cloudEventID)
	return errors.WithStack(err)
}
