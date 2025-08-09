// Package generator generates the Zero's bootstrap code.
package generator

import (
	"cmp"
	"fmt"
	"go/types"
	"hash/fnv"
	"io"
	"iter"
	"maps"
	"slices"
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
	w.L("// Config contains combined Kong configuration for all types constructable by the [Injector].")
	w.L("type ZeroConfig struct {")
	w.In(func(w *codewriter.Writer) {
		for key, config := range stableMapIter(graph.Configs) {
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

	w.L("// Injector contains the constructed dependency graph.")
	w.L("type Injector struct {")
	w.In(func(w *codewriter.Writer) {
		w.L("config     ZeroConfig")
		w.L("singletons map[reflect.Type]any")
	})
	w.L("}")

	w.L("")
	w.L("// NewInjector creates a new Injector with the given context and configuration.")
	w.L("func NewInjector(ctx context.Context, config ZeroConfig) *Injector {")
	w.In(func(w *codewriter.Writer) {
		w.L("return &Injector{config: config, singletons: map[reflect.Type]any{}}")
	})
	w.L("}")
	w.L("")

	w.L("// RegisterHandlers registers all Zero handlers with the injector's [http.ServeMux].")
	w.L("func RegisterHandlers(ctx context.Context, injector *Injector) error {")
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
		for _, receiver := range slices.SortedStableFunc(maps.Keys(receivers), func(a, b Receiver) int {
			return strings.Compare(a.Imp+"."+a.Typ, b.Imp+"."+b.Typ)
		}) {
			index := receivers[receiver]
			writeZeroConstructSingletonByName(w, fmt.Sprintf("r%d", index), receiver.Typ, receiver.Typ)
		}

		// Register the handlers across receiver types.
		writeZeroConstructSingletonByName(w, "mux", "*http.ServeMux", "")
		w.L("_ = mux")
		writeZeroConstructSingletonByName(w, "logger", "*slog.Logger", "")
		w.L("_ = logger")
		w.Import("github.com/alecthomas/zero")
		writeZeroConstructSingletonByName(w, "encodeError", "zero.ErrorEncoder", "")
		writeZeroConstructSingletonByName(w, "encodeResponse", "zero.ResponseEncoder", "")
		w.L("_ = encodeError")
		w.L("_ = encodeResponse")
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
						paramName := params.At(i).Name()
						writeParameterConstruction(w, graph, paramType, api.Label(paramName), fmt.Sprintf("m%dp", mi), i, true, "")
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
					paramName := params.At(i).Name()
					typeName := types.TypeString(paramType, nil)
					// Skip builtin types that are handled in the call site
					if typeName != "*net/http.Request" && typeName != "net/http.ResponseWriter" && typeName != "context.Context" {
						writeParameterConstruction(w, graph, paramType, paramName, "p", i, false, api.Pattern.Method)
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
						responseType = results.At(0).Type()
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
					writeParameterCall(w, paramType, "p", i)
				}
				w.W(")\n")
				errorValue := "nil"
				if hasError {
					errorValue = "herr"
				}
				w.Import("github.com/alecthomas/zero")
				if responseType != nil {
					ref := graph.TypeRef(responseType)
					w.Import(ref.Import)
					w.L(`encodeResponse(logger, r, w, encodeError, out, %s)`, errorValue)
				} else if hasError {
					w.L(`encodeResponse(logger, r, w, encodeError, nil, %s)`, errorValue)
				}
			})
			w.L("}))%s", closing)
		}
		w.L("return nil")
	})
	w.L("}")

	w.Import("net/http")
	w.L("// Run the Zero server container.")
	w.L("//")
	w.L("// This registers all request handlers, cron jobs, PubSub subscribers, etc.")
	w.L("func Run(ctx context.Context, config ZeroConfig) error {")
	w.In(func(w *codewriter.Writer) {
		w.L("injector := NewInjector(ctx, config)")
		w.Import("net/http")
		w.L("if err := RegisterHandlers(ctx, injector); err != nil {")
		w.In(func(w *codewriter.Writer) {
			w.L(`return fmt.Errorf("failed to register handlers: %%w", err)`)
		})
		w.L("}")
		writeZeroConstructSingletonByName(w, "server", "*http.Server", "")

		if len(graph.CronJobs) > 0 {
			w.Import("github.com/alecthomas/zero/providers/cron")
			writeZeroConstructSingletonByName(w, "cron", "*cron.Scheduler", "")
			writeCronJobRegistration(w, graph)
		}

		w.Import("golang.org/x/sync/errgroup")
		w.L("wg, ctx := errgroup.WithContext(ctx)")
		w.Import("log/slog")
		writeZeroConstructSingletonByName(w, "logger", "*slog.Logger", "")
		w.L(`logger.Info("Server starting", "bind", server.Addr)`)
		w.L("wg.Go(func() error { return server.ListenAndServe() })")
		w.L("return wg.Wait()")
	})
	w.L("}")
	w.L("")

	w.L("// Construct an instance of T.")
	w.L("func ZeroConstruct[T any](ctx context.Context, config ZeroConfig) (out T, err error) {")
	w.In(func(w *codewriter.Writer) {
		w.Import("reflect")
		w.L("injector := NewInjector(ctx, config)")
		w.L("return ZeroConstructSingletons[T](ctx, injector)")
	})
	w.L("}")
	w.L("")
	w.L("// ZeroConstructSingletons constructs a new instance of T, or returns an instance of T from the injector if already constructed.")
	w.L("func ZeroConstructSingletons[T any](ctx context.Context, injector *Injector) (out T, err error) {")
	w.In(func(w *codewriter.Writer) {
		w.L("if singleton, ok := injector.singletons[reflect.TypeFor[T]()]; ok {")
		w.In(func(w *codewriter.Writer) {
			w.L("return singleton.(T), nil")
		})
		w.L("}")
		w.L("defer func() { injector.singletons[reflect.TypeFor[T]()] = out }()")
		w.Import("reflect")
		w.L("switch reflect.TypeOf((*T)(nil)).Elem() {")
		w.L("case reflect.TypeOf((*context.Context)(nil)).Elem():")
		w.In(func(w *codewriter.Writer) {
			w.L("return any(ctx).(T), nil")
		})
		w.W("\n")

		for key, config := range stableMapIter(graph.Configs) {
			alias := "Config" + hash(key)
			ref := graph.TypeRef(config.Type)
			w.Import(ref.Import)
			w.L("case reflect.TypeOf((**%s)(nil)).Elem(): // Handle pointer to config.", ref.Ref)
			w.In(func(w *codewriter.Writer) {
				w.L("return any(&injector.config.%s).(T), nil", alias)
			})
			w.W("\n")
			w.L("case reflect.TypeOf((*%s)(nil)).Elem():", ref.Ref)
			w.In(func(w *codewriter.Writer) {
				w.L("return any(injector.config.%s).(T), nil", alias)
			})
			w.W("\n")
		}

		for _, provider := range stableMapIter(graph.Providers) {
			ref := graph.TypeRef(provider.Provides)
			w.Import(ref.Import)
			w.L("case reflect.TypeOf((*%s)(nil)).Elem():", ref.Ref)
			w.In(func(w *codewriter.Writer) {
				writeProviderCall(w, graph, provider, "p", "o")
				w.L("return any(o).(T), nil")
			})
			w.W("\n")
		}

		for _, providers := range stableMapIter(graph.MultiProviders) {
			if len(providers) == 0 {
				continue
			}
			ref := graph.TypeRef(providers[0].Provides)
			w.Import(ref.Import)
			w.L("case reflect.TypeOf((*%s)(nil)).Elem():", ref.Ref)
			w.In(func(w *codewriter.Writer) {
				// Construct all provider results
				for pi, provider := range providers {
					writeProviderCall(w, graph, provider, fmt.Sprintf("p%d_", pi), fmt.Sprintf("r%d", pi))
				}

				// Determine if it's a map or slice and merge accordingly
				providedType := providers[0].Provides.Underlying()
				switch t := providedType.(type) {
				case *types.Map:
					w.Import("maps")
					// Map merging
					w.L("result := make(%s)", ref.Ref)
					for pi := range providers {
						w.L("maps.Copy(result, r%d)", pi)
					}
				case *types.Slice:
					// Slice appending
					w.L("var result %s", ref.Ref)
					for pi := range providers {
						w.L("result = append(result, r%d...)", pi)
					}
				default:
					_ = t
					w.L(`return out, fmt.Errorf("multi-provider type %s must be a map or slice", "%s")`, ref.Ref)
				}
				w.L("return any(result).(T), nil")
			})
			w.W("\n")
		}

		w.W("\n")

		w.L("}")
		w.Import("fmt")
		w.L(`return out, fmt.Errorf("don't know how to construct %%s", reflect.TypeFor[T]())`)
	})
	w.L("}")
	_, err := out.Write(w.Bytes())
	if err != nil {
		return errors.Errorf("failed to write file: %w", err)
	}
	return nil
}

// writeParameterConstruction generates code to construct a parameter of the given type.
// Returns the variable name that holds the constructed parameter.
func writeParameterConstruction(w *codewriter.Writer, graph *depgraph.Graph, paramType types.Type, paramName string, varPrefix string, index int, isMiddleware bool, httpMethod string) {
	ref := graph.TypeRef(paramType)
	w.Import(ref.Import)
	typeName := types.TypeString(paramType, nil)
	varName := fmt.Sprintf("%s%d", varPrefix, index)

	switch typeName {
	case "int":
		if isMiddleware {
			w.L(`%s, err := strconv.Itoa(%q)`, varName, paramName) // For middleware, paramName is the label value
		} else {
			w.L(`%s, err := strconv.Atoi(r.PathValue("%s"))`, varName, paramName)
		}
		w.L("if err != nil {")
		w.In(func(w *codewriter.Writer) {
			if isMiddleware {
				w.Import("fmt")
				w.L(`return out, err`)
			} else {
				w.L(`encodeError(logger, w, fmt.Sprintf("path parameter %s must be a valid integer: %%s", err), http.StatusBadRequest)`, paramName)
				w.L("return")
			}
		})
		w.L("}")
	case "string":
		if isMiddleware {
			w.L(`%s := %q`, varName, paramName) // For middleware, paramName is the label value
		} else {
			w.L(`%s := r.PathValue("%s")`, varName, paramName)
		}
	case "*net/http.Request", "net/http.ResponseWriter", "context.Context":
		// These are handled specially in the call site, no construction needed
	default:
		if isMiddleware {
			w.L("%s, err := ZeroConstructSingletons[%s](ctx, injector)", varName, ref.Ref)
			w.L("if err != nil {")
			w.In(func(w *codewriter.Writer) {
				w.Import("fmt")
				w.L(`return out, err`)
			})
			w.L("}")
		} else {
			w.Import("github.com/alecthomas/zero")
			w.L(`%s, err := zero.DecodeRequest[%s]("%s", r)`, varName, ref.Ref, httpMethod)
			w.L("if err != nil {")
			w.In(func(w *codewriter.Writer) {
				w.L(`encodeError(logger, w, fmt.Sprintf("invalid request: %%s", err), http.StatusBadRequest)`)
				w.L("return")
			})
			w.L("}")
		}
	}
}

// writeParameterCall writes the parameter name for a function call based on the parameter type.
func writeParameterCall(w *codewriter.Writer, paramType types.Type, varPrefix string, index int) {
	typeName := types.TypeString(paramType, nil)
	switch typeName {
	case "context.Context":
		w.W("r.Context()")
	case "*net/http.Request":
		w.W("r")
	case "net/http.ResponseWriter":
		w.W("w")
	default:
		w.W("%s%d", varPrefix, index)
	}
}

// writeZeroConstructSingleton writes code to construct a dependency using ZeroConstructSingletons.
func writeZeroConstructSingleton(w *codewriter.Writer, graph *depgraph.Graph, varName string, depType types.Type, errorWrapper string) {
	ref := graph.TypeRef(depType)
	if ref.Import != "" {
		w.Import(ref.Import)
	}
	w.L("%s, err := ZeroConstructSingletons[%s](ctx, injector)", varName, ref.Ref)
	w.L("if err != nil {")
	w.In(func(w *codewriter.Writer) {
		if errorWrapper != "" {
			w.Import("fmt")
			w.L(`return out, fmt.Errorf("%s: %%w", err)`, errorWrapper)
		} else {
			w.L(`return out, err`)
		}
	})
	w.L("}")
}

// writeZeroConstructSingletonByName writes code to construct a dependency using ZeroConstructSingletons by type name.
func writeZeroConstructSingletonByName(w *codewriter.Writer, varName string, typeName string, errorWrapper string) {
	w.L("%s, err := ZeroConstructSingletons[%s](ctx, injector)", varName, typeName)
	w.L("if err != nil {")
	w.In(func(w *codewriter.Writer) {
		if errorWrapper != "" {
			w.Import("fmt")
			w.L(`return fmt.Errorf("%s: %%w", err)`, errorWrapper)
		} else {
			w.L(`return err`)
		}
	})
	w.L("}")
}

// writeProviderCall generates code to call a provider function with its dependencies.
func writeProviderCall(w *codewriter.Writer, graph *depgraph.Graph, provider *depgraph.Provider, depVarPrefix string, resultVar string) {
	// Construct all dependencies
	for i, require := range provider.Requires {
		writeZeroConstructSingleton(w, graph, fmt.Sprintf("%s%d", depVarPrefix, i), require, "")
	}

	// Get function reference and call it
	functionRef := graph.FunctionRef(provider.Function)
	if functionRef.Import != "" {
		w.Import(functionRef.Import)
	}
	returnsErr := provider.Function.Signature().Results().Len() == 2
	w.Indent()
	if returnsErr {
		w.W("%s, err := %s", resultVar, functionRef.Ref)
	} else {
		w.W("%s := %s", resultVar, functionRef.Ref)
	}

	// Add type instantiation for generic providers
	if provider.IsGeneric {
		// Extract type arguments from the concrete type that this provider provides
		typeArgs := extractTypeArguments(provider.Provides)
		if len(typeArgs) > 0 {
			w.W("[")
			for i, typeArg := range typeArgs {
				argRef := graph.TypeRef(typeArg)
				if argRef.Import != "" {
					w.Import(argRef.Import)
				}
				w.W("%s", argRef.Ref)
				if i < len(typeArgs)-1 {
					w.W(", ")
				}
			}
			w.W("]")
		}
	}

	w.W("(")
	for i := range len(provider.Requires) {
		w.W("%s%d", depVarPrefix, i)
		if i < len(provider.Requires)-1 {
			w.W(", ")
		}
	}
	w.W(")\n")
	if returnsErr {
		ref := graph.TypeRef(provider.Provides)
		w.L("if err != nil {")
		w.In(func(w *codewriter.Writer) {
			w.L(`return out, fmt.Errorf("%s: %%w", err)`, ref.Ref)
		})
		w.L("}")
	}
}

// extractTypeArguments extracts type arguments from a concrete generic type
func extractTypeArguments(t types.Type) []types.Type {
	if named, ok := t.(*types.Named); ok {
		if typeArgs := named.TypeArgs(); typeArgs != nil {
			result := make([]types.Type, typeArgs.Len())
			for i := range typeArgs.Len() {
				result[i] = typeArgs.At(i)
			}
			return result
		}
	}
	return nil
}

func hash(s string) string {
	h := fnv.New64a()
	h.Write([]byte(s))
	return fmt.Sprintf("%x", h.Sum64())
}

func writeCronJobRegistration(w *codewriter.Writer, graph *depgraph.Graph) {
	// First, collect the receiver types so we can construct them.
	type Receiver struct {
		Imp string
		Typ string
	}
	receivers := map[Receiver]int{}
	receiverIndex := 0
	for _, cronJob := range graph.CronJobs {
		receiver := cronJob.Function.Signature().Recv().Type()
		ref := graph.TypeRef(receiver)
		w.Import(ref.Import)
		key := Receiver{ref.Import, ref.Ref}
		if _, ok := receivers[key]; !ok {
			receivers[key] = receiverIndex
			receiverIndex++
		}
	}
	for _, receiver := range slices.SortedStableFunc(maps.Keys(receivers), func(a, b Receiver) int {
		return strings.Compare(a.Imp+"."+a.Typ, b.Imp+"."+b.Typ)
	}) {
		index := receivers[receiver]
		writeZeroConstructSingletonByName(w, fmt.Sprintf("r%d", index), receiver.Typ, "")
	}

	// Register each cron job
	for _, cronJob := range graph.CronJobs {
		receiver := cronJob.Function.Signature().Recv().Type()
		ref := graph.TypeRef(receiver)
		receiverIndex := receivers[Receiver{ref.Import, ref.Ref}]

		// Create the job name from the full type signature
		jobName := fmt.Sprintf("%s.%s", ref, cronJob.Function.Name())

		// Get the schedule duration at generation time
		schedule, scheduleErr := cronJob.Schedule.Duration()
		if scheduleErr != nil {
			w.L(`return out, fmt.Errorf("invalid cron schedule for %s: %%s", %q)`, jobName, scheduleErr.Error())
			continue
		}

		// Register the job
		w.Import("time")
		w.L("err = cron.Register(%q, time.Duration(%d), r%d.%s)", jobName, schedule.Nanoseconds(), receiverIndex, cronJob.Function.Name())
		w.L("if err != nil {")
		w.In(func(w *codewriter.Writer) {
			w.Import("fmt")
			w.L(`return fmt.Errorf("failed to register cron job %s: %%w", err)`, jobName)
		})
		w.L("}")
	}
}

func stableMapIter[K cmp.Ordered, V any](m map[K]V) iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		for _, key := range slices.Sorted(maps.Keys(m)) {
			if !yield(key, m[key]) {
				break
			}
		}
	}
}
