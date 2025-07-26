// Package generator generates the Zero's bootstrap code.
package generator

import (
	"fmt"
	"go/types"
	"hash/fnv"
	"io"

	"github.com/alecthomas/errors"
	"github.com/alecthomas/zero/internal/codewriter"
	"github.com/alecthomas/zero/internal/depgraph"
)

// Generate Zero's bootstrap code.
func Generate(out io.Writer, graph *depgraph.Graph) error {
	w := codewriter.New(graph.Dest.Name())
	w.Import("context")
	w.L("// Config contains combined Kong configuration for all types in [Construct].")
	w.L("type ZeroConfig struct {")
	w.In(func(w *codewriter.Writer) {
		for key, config := range graph.Configs {
			alias := "Config" + hash(key)
			ref := graph.TypeRef(config)
			if ref.Import != "" {
				w.Import(ref.Import)
			}
			w.L("%s %s `embed:\"\"`", alias, ref.Ref)
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
		w.L("switch any(out).(type) {")
		w.L("case context.Context:")
		w.In(func(w *codewriter.Writer) {
			w.L("return any(ctx).(T), nil")
		})
		w.W("\n")

		for key, config := range graph.Configs {
			alias := "Config" + hash(key)
			ref := graph.TypeRef(config)
			if ref.Import != "" {
				w.Import(ref.Import)
			}
			w.L("case *%s: // Handle pointer to config.", ref.Ref)
			w.In(func(w *codewriter.Writer) {
				w.L("return any(&config.%s).(T), nil", alias)
			})
			w.W("\n")
			w.L("case %s:", ref.Ref)
			w.In(func(w *codewriter.Writer) {
				w.L("return any(config.%s).(T), nil", alias)
			})
			w.W("\n")
		}

		for _, provider := range graph.Providers {
			ref := graph.TypeRef(provider.Provides)
			if ref.Import != "" {
				w.Import(ref.Import)
			}
			w.L("case %s:", ref.Ref)
			w.In(func(w *codewriter.Writer) {
				for i, require := range provider.Requires {
					reqRef := graph.TypeRef(require)
					if reqRef.Import != "" {
						w.Import(reqRef.Import)
					}
					w.L("if p%d, err := ZeroConstructSingletons[%s](ctx, config, singletons); err != nil {", i, reqRef.Ref)
					w.In(func(w *codewriter.Writer) {
						w.Import("fmt")
						w.L(`return out, err`)
					})
					w.Indent()
					w.W("} else ")
				}

				functionRef := provider.Function.Name()
				if alias := graph.ImportAlias(provider.Function.Pkg().Path()); alias != "" {
					functionRef = alias + "." + functionRef
				}
				w.W("if o, err := %s(", functionRef)
				for i := range len(provider.Requires) {
					w.W("p%d", i)
					if i < len(provider.Requires)-1 {
						w.W(", ")
					}
				}
				w.W("); err != nil {\n")
				w.In(func(w *codewriter.Writer) {
					w.L(`return out, fmt.Errorf("%s: %%w", err)`, provider.Function.Name())
				})
				w.L("} else {")
				w.In(func(w *codewriter.Writer) {
					w.L("return any(o).(T), nil")
				})
				w.L("}")
			})
			w.W("\n")
		}

		if len(graph.APIs) > 0 {
			w.Import("net/http")
			w.L("case *http.ServeMux:")
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
					if ref.Import != "" {
						w.Import(ref.Import)
					}
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
					w.L("mux.HandleFunc(%q, func(w http.ResponseWriter, r *http.Request) {", api.Pattern.Pattern())
					w.In(func(w *codewriter.Writer) {
						signature := api.Function.Signature()

						ref := graph.TypeRef(signature.Recv().Type())
						receiverIndex := receivers[Receiver{ref.Import, ref.Ref}]
						params := signature.Params()

						// First pass, decode any parameters from the Request
						for i := range params.Len() {
							paramType := params.At(i).Type()
							ref := graph.TypeRef(paramType)
							if ref.Import != "" {
								w.Import(ref.Import)
							}
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
							case "*http.Request", "http.ResponseWriter", "context.Context":
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
							case "*http.Request":
								w.W("r")
							case "http.ResponseWriter":
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
							if ref.Import != "" {
								w.Import(ref.Import)
							}
							w.L(`_ = zero.EncodeResponse[%s](r, w, out, %s)`, ref.Ref, errorValue)
						} else if hasError {
							w.L(`_ = zero.EncodeResponse[zero.EmptyResponse](r, w, nil, %s)`, errorValue)
						}
					})
					w.L("})")
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
