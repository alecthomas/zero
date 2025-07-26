// Package directiveparser implements a parser for the Zero's compiler directives.
package directiveparser

import (
	"fmt"
	"strings"

	"github.com/alecthomas/errors"
	"github.com/alecthomas/participle/v2"
	"github.com/alecthomas/participle/v2/lexer"
)

var (
	annotationParser = participle.MustBuild[annotation](
		participle.Lexer(patternLexer),
		participle.Union[Directive](&DirectiveAPI{}, &DirectiveProvider{}, &DirectiveConfig{}, &DirectiveMiddleware{}),
		participle.Union[Segment](WildcardSegment{}, LiteralSegment{}, TrailingSegment{}),
		participle.Elide("Whitespace"),
		participle.CaseInsensitive("Method"),
	)
	patternLexer = lexer.MustSimple([]lexer.SimpleRule{
		{"Method", `GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS|TRACE|CONNECT|ANY`},
		{"Ident", `[a-zA-Z_][a-zA-Z0-9_]*`},
		{"Escape", `%[0-9a-fA-F][0-9a-fA-F]`},
		{"Ellipsis", `\.\.\.`},
		{"Other", `[-{}._~!$&'()*+,;=@/0-9:]`},
		{"String", `"(\\.|[^"])*"`},
		{"Whitespace", `\s+`},
	})
)

type annotation struct {
	Directive Directive `parser:"'zero' ':' @@"`
}

type Directive interface {
	directive()
	// Validate the directive.
	Validate() error
	String() string
}

type DirectiveProvider struct {
	Weak bool `parser:"'provider' @'weak'?"`
}

func (p *DirectiveProvider) directive() {}
func (p *DirectiveProvider) String() string {
	if p.Weak {
		return "zero:provider weak"
	}
	return "zero:provider"
}
func (p *DirectiveProvider) Validate() error { return nil }

type DirectiveConfig struct {
	Config bool `parser:"@'config'"` // Unused but necessary for Participle.
}

func (d *DirectiveConfig) directive()      {}
func (d *DirectiveConfig) String() string  { return "zero:config" }
func (d *DirectiveConfig) Validate() error { return nil }

type DirectiveMiddleware struct {
	Middleware bool     `parser:"@'middleware'"` // Unused but necessary for Participle.
	Labels     []string `parser:"@Ident*"`
}

func (d *DirectiveMiddleware) directive() {}
func (d *DirectiveMiddleware) String() string {
	result := "zero:middleware"
	if len(d.Labels) > 0 {
		result += " " + strings.Join(d.Labels, " ")
	}
	return result
}
func (d *DirectiveMiddleware) Validate() error { return nil }

// DirectiveAPI represents a //zero:api directive
type DirectiveAPI struct {
	Method   string    `parser:"'api' @Method?"` // HTTP method, empty for any method
	Host     string    `parser:"(@~'/')*"`       // Host pattern, empty for any host
	Segments []Segment `parser:"@@+"`            // Parsed path segments
	Labels   []*Label  `parser:"@@*"`
}

func (p *DirectiveAPI) directive() {}
func (p *DirectiveAPI) Wildcard(name string) bool {
	for _, segment := range p.Segments {
		if wildcard, ok := segment.(WildcardSegment); ok {
			if wildcard.Name == name {
				return true
			}
		}
	}
	return false
}
func (p *DirectiveAPI) Validate() error {
	p.Method = strings.ToUpper(p.Method)
	for i, segment := range p.Segments {
		switch segment := segment.(type) {
		case TrailingSegment:
			if i != len(p.Segments)-1 {
				return errors.Errorf("invalid path, cannot contain empty path segments")
			}
		case WildcardSegment:
			if segment.Remainder && i != len(p.Segments)-1 {
				return errors.Errorf("invalid path, catch-all can only be at end")
			}
		}

	}
	return nil
}

type Label struct {
	Name  string `parser:"@(Ident | Method)"`
	Value string `parser:"('=' @~(Whitespace | EOF)+)?"`
}

type Segment interface {
	String() string
	pathSegment()
}

type WildcardSegment struct {
	Name      string `parser:"'/' '{' @(Ident | Method)"`
	Remainder bool   `parser:"@'...'? '}'"`
}

func (w WildcardSegment) pathSegment() {}
func (w WildcardSegment) String() string {
	if w.Remainder {
		return fmt.Sprintf("/{%s...}", w.Name)
	}
	return fmt.Sprintf("/{%s}", w.Name)
}

type LiteralSegment struct {
	Literal string `parser:"'/' @~(' ' | '/')+"`
}

func (l LiteralSegment) pathSegment()   {}
func (l LiteralSegment) String() string { return fmt.Sprintf("/%s", l.Literal) }

// TrailingSegment represents a trailing terminating /
type TrailingSegment struct {
	Anonymous string `parser:"'/'"`
}

func (a TrailingSegment) pathSegment()   {}
func (a TrailingSegment) String() string { return "/" }

// Parse a Zero's compiler directive.
func Parse(pattern string) (Directive, error) {
	if pattern == "" {
		return nil, errors.Errorf("empty pattern")
	}

	result, err := annotationParser.ParseString("", pattern)
	if err != nil {
		return nil, errors.Errorf("failed to parse pattern: %w", err)
	}
	directive := result.Directive
	if err := directive.Validate(); err != nil {
		return nil, err
	}

	return result.Directive, nil
}

// Pattern returns the http.ServeMux-compatible pattern.
func (p *DirectiveAPI) Pattern() string {
	var parts []string

	if p.Method != "" {
		parts = append(parts, p.Method)
	}

	if p.Host != "" {
		parts = append(parts, p.Host+p.Path())
	} else {
		parts = append(parts, p.Path())
	}
	return strings.Join(parts, " ")
}

func (p *DirectiveAPI) String() string {
	return "zero:api " + p.Pattern()
}

func (p *DirectiveAPI) Path() string {
	out := make([]string, 0, len(p.Segments))
	for _, segment := range p.Segments {
		out = append(out, segment.String())
	}
	return strings.Join(out, "")
}
