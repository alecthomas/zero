package dashboard

import (
	_ "embed"
	"html/template"
)

//go:embed index.gohtml
var index string
var indexTmpl = template.Must(template.New("providers/dashboard/index.gohtml").Parse(index))

type indexContext struct {
	Selected   Component
	Components Components
	Content    template.HTML
}
