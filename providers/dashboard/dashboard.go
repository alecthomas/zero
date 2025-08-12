// Package dashboard provides an extensible dashboard for Zero.
//
// The dashboard is served under `/_admin/`, with each component directly underneath that.
//
// API calls for each [Component] should be handled by //zero:api endpoints, and mounted under `/_admin/api/<slug>/...`.
package dashboard

import (
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"log/slog"
	"strings"

	"github.com/alecthomas/errors"
	zerohttp "github.com/alecthomas/zero/providers/http"
)

type Detail struct {
	// Icon returns the Font Awesome 5 icon for the sidebar.
	Icon string
	// Title of the component in the sidebar.
	Title string
	// Slug for the component.
	Slug string
}

// Component defines the structure of a dashboard component.
type Component interface {
	Detail() Detail
	// Children returns sub-components for expandable sections.
	Children() Components
	// SetRenderer sets the renderer for the component.
	SetRenderer(renderer *Renderer)
}

type Components []Component

type Dashboard struct {
	renderer   *Renderer
	components Components
}

// New creates a new [Dashboard] instance.
//
//zero:provider weak
func New(logger *slog.Logger, config zerohttp.Config, components Components) *Dashboard {
	logger.Debug("Registered admin dashboard", "url", fmt.Sprintf("http://%s/_admin/", config.Bind))
	for _, component := range components {
		component.SetRenderer(NewRenderer(component, components))
	}
	return &Dashboard{components: components, renderer: NewRenderer(nil, components)}
}

//zero:api /_admin/ dashboard
func (d *Dashboard) Admin(ctx context.Context) (string, error) {
	tctx := indexContext{
		Components: d.components,
	}
	w := &strings.Builder{}
	if err := indexTmpl.Execute(w, tctx); err != nil {
		return "", errors.Wrap(err, "failed to execute base template")
	}
	return w.String(), nil
}

// Renderer is used by components to render their content in the admin panel.
type Renderer struct {
	selected   Component
	components Components
}

// NewRenderer creates a new [Renderer] instance.
func NewRenderer(selected Component, components Components) *Renderer {
	return &Renderer{selected: selected, components: components}
}

// RenderString renders a string in the main content container of the admin panel.
func (l *Renderer) RenderString(ctx context.Context, content string) (string, error) {
	tctx := indexContext{
		Selected:   l.selected,
		Components: l.components,
		Content:    template.HTML(content), //nolint
	}

	w := &strings.Builder{}
	if err := indexTmpl.Execute(w, tctx); err != nil {
		return "", errors.Wrap(err, "failed to execute base template")
	}
	return w.String(), nil
}

// RenderTemplate renders a html/template precompiled template in the main content container of the admin panel.
func (l *Renderer) RenderTemplate(ctx context.Context, html *template.Template, data any) (string, error) {
	w := &strings.Builder{}
	if err := html.Execute(w, data); err != nil {
		return "", errors.Wrap(err, "failed to execute component template")
	}
	return l.RenderString(ctx, w.String())
}
