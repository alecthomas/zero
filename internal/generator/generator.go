// Package generator generates the Zero's bootstrap code.
package generator

import (
	"fmt"
	"go/types"
	"hash/fnv"
	"io"
	"strings"

	"github.com/alecthomas/errors"
	"github.com/alecthomas/zero/internal/codewriter"
	"github.com/alecthomas/zero/internal/depgraph"
)

type generateOptions struct {
	tags []string
}

type Option func(*generateOptions)

// WithTags sets the list of //go:build tags to include in the generated code.
func WithTags(tags ...string) Option {
	return func(o *generateOptions) {
		o.tags = tags
	}
}

// Generate Zero's bootstrap code.
func Generate(out io.Writer, graph *depgraph.Graph, options ...Option) error {
	opts := &generateOptions{}
	for _, option := range options {
		option(opts)
	}

	w := codewriter.New(graph.Dest.Name())
	if len(opts.tags) > 0 {
		pw := w.Prelude()
		pw.L("//go:build %s", strings.Join(opts.tags, " "))
		pw.L("")
	}
	w.Import("context")
	w.L("// Config contains combined Kong configuration for all types in [Construct].")
	w.L("type ZeroConfig struct {")
	w.In(func(w *codewriter.Writer) {
		for key, config := range graph.Configs {
			alias := "Config" + hash(key)
			ref := graph.TypeRef(config.Type)
			w.Import(ref.Import)
			prefix := ""
			if config.Directive.Prefix != "" {
				prefix = fmt.Sprintf(" prefix:%q", config.Directive.Prefix)
			}
			w.L("%s %s `embed:\"\"%s`", alias, ref.Ref, prefix)
		}
	})
	w.L("}")
	w.L("")
	w.L("// Construct an instance of T.")
	w.L("func ZeroConstruct[T any](ctx context.Context, config ZeroConfig) (out T, err error) {")
	w.In(func(w *codewriter.Writer) {
		w.Import("reflect")
		w.L("return ZeroConstructSingletons[T](ctx, config, map[reflect.Type]any{})")
	})
	w.L("}")
	w.L("")
	w.L("// ZeroConstructSingletons constructs a new instance of T, or returns an instance of T from \"singletons\" if already constructed.")
	w.L("func ZeroConstructSingletons[T any](ctx context.Context, config ZeroConfig, singletons map[reflect.Type]any) (out T, err error) {")
	w.In(func(w *codewriter.Writer) {
		w.L("if singleton, ok := singletons[reflect.TypeFor[T]()]; ok {")
		w.In(func(w *codewriter.Writer) {
			w.L("return singleton.(T), nil")
		})
		w.L("}")
		w.L("defer func() { singletons[reflect.TypeFor[T]()] = out }()")
		w.Import("reflect")
		w.L("switch reflect.TypeOf((*T)(nil)).Elem() {")
		w.L("case reflect.TypeOf((*context.Context)(nil)).Elem():")
		w.In(func(w *codewriter.Writer) {
			w.L("return any(ctx).(T), nil")
		})
		w.W("\n")

		for key, config := range graph.Configs {
			alias := "Config" + hash(key)
			ref := graph.TypeRef(config.Type)
			w.Import(ref.Import)
			w.L("case reflect.TypeOf((**%s)(nil)).Elem(): // Handle pointer to config.", ref.Ref)
			w.In(func(w *codewriter.Writer) {
				w.L("return any(&config.%s).(T), nil", alias)
			})
			w.W("\n")
			w.L("case reflect.TypeOf((*%s)(nil)).Elem():", ref.Ref)
			w.In(func(w *codewriter.Writer) {
				w.L("return any(config.%s).(T), nil", alias)
			})
			w.W("\n")
		}

		for _, provider := range graph.Providers {
			ref := graph.TypeRef(provider.Provides)
			w.Import(ref.Import)
			w.L("case reflect.TypeOf((*%s)(nil)).Elem():", ref.Ref)
			w.In(func(w *codewriter.Writer) {
				for i, require := range provider.Requires {
					reqRef := graph.TypeRef(require)
					if reqRef.Import != "" {
						w.Import(reqRef.Import)
					}
					w.L("p%d, err := ZeroConstructSingletons[%s](ctx, config, singletons)", i, reqRef.Ref)
					w.L("if err != nil {")
					w.In(func(w *codewriter.Writer) {
						w.L(`return out, err`)
					})
					w.L("}")
				}

				functionRef := graph.FunctionRef(provider.Function)
				if functionRef.Import != "" {
					w.Import(functionRef.Import)
				}
				returnsErr := provider.Function.Signature().Results().Len() == 2
				w.Indent()
				if returnsErr {
					w.W("o, err := %s(", functionRef.Ref)
				} else {
					w.W("o := %s(", functionRef.Ref)
				}
				for i := range len(provider.Requires) {
					w.W("p%d", i)
					if i < len(provider.Requires)-1 {
						w.W(", ")
					}
				}
				w.W(")\n")
				if returnsErr {
					w.L("if err != nil {\n")
					w.In(func(w *codewriter.Writer) {
						w.L(`return out, fmt.Errorf("%s: %%w", err)`, ref.Ref)
					})
					w.L("}")
				}
				w.L("return any(o).(T), nil")
			})
			w.W("\n")
		}

		if len(graph.APIs) > 0 {
			w.Import("net/http")
			w.L("case reflect.TypeOf((**http.ServeMux)(nil)).Elem():")
			w.In(func(w *codewriter.Writer) {
				// First, collect the receiver types so we can construct them.
				type Receiver struct {
					Imp string
					Typ string
				}
				receivers := map[Receiver]int{}
				receiverIndex := 0
				for _, api := range graph.APIs {
					receiver := api.Function.Signature().Recv().Type()
					ref := graph.TypeRef(receiver)
					w.Import(ref.Import)
					key := Receiver{ref.Import, ref.Ref}
					if _, ok := receivers[key]; !ok {
						receivers[key] = receiverIndex
						receiverIndex++
					}
				}
				for receiver, index := range receivers {
					w.L("r%d, err := ZeroConstructSingletons[%s](ctx, config, singletons)", index, receiver.Typ)
					w.L("if err != nil {")
					w.In(func(w *codewriter.Writer) {
						w.Import("fmt")
						w.L(`return out, fmt.Errorf("*http.ServeMux: %%w", err)`)
					})
					w.L("}")
				}

				// Next, create the ServeMux and register the handlers across receiver types.
				w.L("mux := http.NewServeMux()")
				for _, api := range graph.APIs {
					handler := "http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {"
					closing := ""
					for mi, middleware := range graph.Middleware {
						if !middleware.Match(api) {
							continue
						}
						ref := graph.FunctionRef(middleware.Function)
						w.Import(ref.Import)
						if middleware.Factory {
							args := []string{}
							params := middleware.Function.Signature().Params()
							w.L("// Parameters for the %s middleware", ref.Ref)
							for i := range params.Len() {
								args = append(args, fmt.Sprintf("m%dp%d", mi, i))
								paramType := params.At(i).Type()
								ref := graph.TypeRef(paramType)
								w.Import(ref.Import)
								paramName := params.At(i).Name()
								typeName := types.TypeString(paramType, nil)
								switch typeName {
								// Labels
								case "int":
									w.L(`m%dp%d, err := strconv.Itoa(%q)`, mi, i, api.Label(paramName))
									w.L("if err != nil {")
									w.In(func(w *codewriter.Writer) {
										w.L(`http.Error(w, fmt.Sprintf("Path parameter %s must be a valid integer: %s", paramName, err), http.StatusBadRequest)`)
										w.L("return")
									})
									w.L("}")
								case "string":
									w.L(`m%dp%d := %q`, mi, i, api.Label(paramName))
								default:
									w.L("m%dp%d, err := ZeroConstructSingletons[%s](ctx, config, singletons)", mi, i, paramType)
									w.L("if err != nil {")
									w.In(func(w *codewriter.Writer) {
										w.Import("fmt")
										w.L(`return out, err`)
									})
									w.L("}")
								}
							}
							handler = fmt.Sprintf("%s(%s)(%s", ref.Ref, strings.Join(args, ", "), handler)
						} else {
							handler = fmt.Sprintf("%s(%s", ref.Ref, handler)
						}
						closing += ")"
					}
					w.L("mux.Handle(%q, %s", api.Pattern.Pattern(), handler)
					w.In(func(w *codewriter.Writer) {
						signature := api.Function.Signature()

						ref := graph.TypeRef(signature.Recv().Type())
						receiverIndex := receivers[Receiver{ref.Import, ref.Ref}]
						params := signature.Params()

						// First pass, decode any parameters from the Request
						for i := range params.Len() {
							paramType := params.At(i).Type()
							ref := graph.TypeRef(paramType)
							w.Import(ref.Import)
							paramName := params.At(i).Name()
							typeName := types.TypeString(paramType, nil)
							switch typeName {
							// Path parameters.
							case "int":
								w.L(`p%d, err := strconv.Itoa(r.PathValue("%s"))`, i, paramName)
								w.L("if err != nil {")
								w.In(func(w *codewriter.Writer) {
									w.L(`http.Error(w, fmt.Sprintf("Path parameter %s must be a valid integer: %s", paramName, err), http.StatusBadRequest)`)
									w.L("return")
								})
								w.L("}")
							case "string":
								w.L(`p%d := r.PathValue("%s")`, i, paramName)
							// Builtin types, just pass them through.
							case "*net/http.Request", "net/http.ResponseWriter", "context.Context":
							default: // Anything else is a request body/query parameters.
								w.Import("github.com/alecthomas/zero")
								w.L(`p%d, err := zero.DecodeRequest[%s]("%s", r)`, i, ref.Ref, api.Pattern.Method)
								w.L("if err != nil {")
								w.In(func(w *codewriter.Writer) {
									w.L(`http.Error(w, fmt.Sprintf("Invalid request: %%s", err), http.StatusBadRequest)`)
									w.L("return")
								})
								w.L("}")
							}
						}

						// Second pass, construct the request.
						w.Indent()
						results := signature.Results()
						var responseType types.Type
						hasError := true
						switch results.Len() {
						case 0:
							hasError = false
						case 1: // Either (error) or response body (T)
							if results.At(0).Type().String() == "error" {
								w.W("herr := ")
							} else {
								hasError = false
								w.W("out := ")
								responseType = results.At(1).Type()
							}
						case 2: // Always (T, error)
							w.W("out, herr := ")
							responseType = results.At(0).Type()
						}
						w.W("r%d.%s(", receiverIndex, api.Function.Name())
						for i := range params.Len() {
							if i > 0 {
								w.W(", ")
							}
							paramType := params.At(i).Type()
							typeName := types.TypeString(paramType, nil)
							switch typeName {
							case "context.Context":
								w.W("r.Context()")
							case "*net/http.Request":
								w.W("r")
							case "net/http.ResponseWriter":
								w.W("w")
							default:
								w.W("p%d", i)
							}
						}
						w.W(")\n")
						errorValue := "nil"
						if hasError {
							errorValue = "herr"
						}
						if responseType != nil {
							ref := graph.TypeRef(responseType)
							w.Import(ref.Import)
							w.Import("github.com/alecthomas/zero")
							w.L(`_ = zero.EncodeResponse[%s](r, w, out, %s)`, ref.Ref, errorValue)
						} else if hasError {
							w.Import("github.com/alecthomas/zero")
							w.L(`_ = zero.EncodeResponse[zero.EmptyResponse](r, w, nil, %s)`, errorValue)
						}
					})
					w.L("}))%s", closing)
				}
				w.L("return any(mux).(T), nil")
			})
			w.W("\n")
		}
		w.L("}")
		w.L(`return out, fmt.Errorf("don't know how to construct %%T", out)`)
	})
	w.L("}")
	_, err := out.Write(w.Bytes())
	if err != nil {
		return errors.Errorf("failed to write file: %w", err)
	}
	return nil
}

func hash(s string) string {
	h := fnv.New64a()
	h.Write([]byte(s))
	return fmt.Sprintf("%x", h.Sum64())
}
