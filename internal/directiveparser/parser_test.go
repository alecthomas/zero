package directiveparser

import (
	"testing"

	"github.com/alecthomas/assert/v2"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    Directive
		wantErr bool
	}{
		{
			name:    "CronValid",
			pattern: "zero:cron 1h",
			want: &DirectiveCron{
				Schedule: "1h",
			},
		},
		{
			name:    "CronDay",
			pattern: "zero:cron 1d",
			want: &DirectiveCron{
				Schedule: "1d",
			},
		},
		{
			name:    "CronWeek",
			pattern: "zero:cron 1w",
			want: &DirectiveCron{
				Schedule: "1w",
			},
		},
		{
			name:    "CronInvalid",
			pattern: "zero:cron 1y",
			wantErr: true,
		},
		{
			name:    "EmptyPattern",
			pattern: "zero:api",
			wantErr: true,
		},
		{
			name:    "RootPath",
			pattern: "zero:api /",
			want: &DirectiveAPI{
				Segments: []Segment{
					TrailingSegment{},
				},
			},
		},
		{
			name:    "SimplePath",
			pattern: "zero:api /hello",
			want: &DirectiveAPI{
				Segments: []Segment{
					LiteralSegment{Literal: "hello"},
				},
			},
		},
		{
			name:    "PathWithMethod",
			pattern: "zero:api GET /users",
			want: &DirectiveAPI{
				Method: "GET",
				Segments: []Segment{
					LiteralSegment{Literal: "users"},
				},
			},
		},
		{
			name:    "PathWithHost",
			pattern: "zero:api example.com/api",
			want: &DirectiveAPI{
				Host: "example.com",
				Segments: []Segment{
					LiteralSegment{Literal: "api"},
				},
			},
		},
		{
			name:    "MethodWithHostAndPath",
			pattern: "zero:api POST api.example.com/users",
			want: &DirectiveAPI{
				Method: "POST",
				Host:   "api.example.com",
				Segments: []Segment{
					LiteralSegment{Literal: "users"},
				},
			},
		},
		{
			name:    "SingleWildcard",
			pattern: "zero:api /users/{id}",
			want: &DirectiveAPI{
				Segments: []Segment{
					LiteralSegment{Literal: "users"},
					WildcardSegment{Name: "id"},
				},
			},
		},
		{
			name:    "MultipleWildcards",
			pattern: "zero:api /users/{id}/posts/{postId}",
			want: &DirectiveAPI{
				Segments: []Segment{
					LiteralSegment{Literal: "users"},
					WildcardSegment{Name: "id"},
					LiteralSegment{Literal: "posts"},
					WildcardSegment{Name: "postId"},
				},
			},
		},
		{
			name:    "CatchAllWildcard",
			pattern: "zero:api /static/{path...}",
			want: &DirectiveAPI{
				Segments: []Segment{
					LiteralSegment{Literal: "static"},
					WildcardSegment{Name: "path", Remainder: true},
				},
			},
		},
		{
			name:    "CatchAllAtRoot",
			pattern: "zero:api /{path...}",
			want: &DirectiveAPI{
				Segments: []Segment{
					WildcardSegment{Name: "path", Remainder: true},
				},
			},
		},
		{
			name:    "HostMustHaveTrailingSlash",
			pattern: "zero:api example.com",
			wantErr: true,
		},
		{
			name:    "InvalidWildcardSyntax",
			pattern: "zero:api /users/{id",
			wantErr: true,
		},
		{
			name:    "EmptyWildcardName",
			pattern: "zero:api /users/{}",
			wantErr: true,
		},
		{
			name:    "EmptyCatchAllName",
			pattern: "zero:api /static/{...}",
			wantErr: true,
		},
		{
			name:    "SchemeNotAllowed",
			pattern: "zero:api https://example.com/path",
			wantErr: true,
		},
		{
			name:    "HostPatternWithoutLeadingSlash",
			pattern: "zero:api users/123",
			want: &DirectiveAPI{
				Host: "users",
				Segments: []Segment{
					LiteralSegment{Literal: "123"},
				},
			},
		},
		{
			name:    "EmptySegment",
			pattern: "zero:api /users//posts",
			wantErr: true,
		},
		{
			name:    "EscapedChar",
			pattern: "zero:api /users/%2F",
			want: &DirectiveAPI{
				Segments: []Segment{
					LiteralSegment{Literal: "users"},
					LiteralSegment{Literal: "%2F"},
				},
			},
		},
		{
			name:    "LabelsWithParams",
			pattern: "zero:api /hello ttl=300",
			want: &DirectiveAPI{
				Segments: []Segment{
					LiteralSegment{Literal: "hello"},
				},
				Labels: []*Label{
					{Name: "ttl", Value: "300"},
				},
			},
		},
		{
			name:    "CatchAllNotAtEnd",
			pattern: "zero:api /users/{path...}/posts",
			wantErr: true,
		},
		{
			name:    "Provider",
			pattern: "zero:provider",
			want: &DirectiveProvider{
				Weak: false,
			},
		},
		{
			name:    "ProviderWeak",
			pattern: "zero:provider weak",
			want: &DirectiveProvider{
				Weak: true,
			},
		},
		{
			name:    "ProviderAllOptions",
			pattern: "zero:provider multi weak require=first require=second,third",
			want: &DirectiveProvider{
				Weak:    true,
				Multi:   true,
				Require: []string{"first", "second", "third"},
			},
		},
		{
			name:    "ProviderWithStringRequire",
			pattern: `zero:provider require="github.com/example/pkg/ExternalProvider"`,
			want: &DirectiveProvider{
				Require: []string{"github.com/example/pkg/ExternalProvider"},
			},
		},
		{
			name:    "ProviderMixedRequire",
			pattern: `zero:provider require=LocalProvider,"github.com/example/pkg/ExternalProvider"`,
			want: &DirectiveProvider{
				Require: []string{"LocalProvider", "github.com/example/pkg/ExternalProvider"},
			},
		},
		{
			name:    "Config",
			pattern: "zero:config",
			want:    &DirectiveConfig{},
		},
		{
			name:    "Config",
			pattern: `zero:config prefix="prefix-"`,
			want: &DirectiveConfig{
				Prefix: "prefix-",
			},
		},
		{
			name:    "Config",
			pattern: "zero:config invalid",
			wantErr: true,
		},
		{
			name:    "Middleware",
			pattern: "zero:middleware",
			want:    &DirectiveMiddleware{},
		},
		{
			name:    "MiddlewareWithLabel",
			pattern: "zero:middleware auth",
			want: &DirectiveMiddleware{
				Labels: []string{"auth"},
			},
		},
		{
			name:    "MiddlewareWithMultipleLabels",
			pattern: "zero:middleware auth cors",
			want: &DirectiveMiddleware{
				Labels: []string{"auth", "cors"},
			},
		},
		{
			name:    "Subscribe",
			pattern: "zero:subscribe",
			want:    &DirectiveSubscribe{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.pattern)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPatternString(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
	}{
		{
			name:    "SimplePath",
			pattern: "zero:api /users",
		},
		{
			name:    "MethodAndPath",
			pattern: "zero:api GET /users",
		},
		{
			name:    "HostAndPath",
			pattern: "zero:api example.com/api",
		},
		{
			name:    "MethodHostAndPath",
			pattern: "zero:api POST api.example.com/users",
		},
		{
			name:    "WildcardPattern",
			pattern: "zero:api /users/{id}",
		},
		{
			name:    "CatchAllPattern",
			pattern: "zero:api /static/{path...}",
		},
		{
			name:    "TrailingSegment",
			pattern: "zero:api /users/{id}/",
		},
		{
			name:    "Subscribe",
			pattern: "zero:subscribe",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			directive, err := Parse(tt.pattern)
			assert.NoError(t, err)
			assert.Equal(t, tt.pattern, directive.String())
		})
	}
}
