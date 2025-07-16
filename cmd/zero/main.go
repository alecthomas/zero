package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/alecthomas/zero/internal/depgraph"
	"github.com/alecthomas/zero/internal/generator"
)

var cli struct {
	Tags     string   `help:"Tags to enable during type analysis."          placeholder:"TAG"`
	Resolve  []string `help:"Resolve an ambiguous type with this provider." placeholder:"REF"`
	List     bool     `help:"List all dependencies."                        xor:"action"`
	Root     []string `help:"Prune dependencies outside these root types."  placeholder:"REF"                                         short:"r"`
	Dest     string   `arg:""                                               help:"Destination package directory for generated files." type:"existingdir"`
	Patterns []string `arg:""                                               help:"Additional packages pattern to scan."               optional:""`
}

func main() {
	kctx := kong.Parse(&cli)
	graph, err := depgraph.Analyse(cli.Dest,
		depgraph.WithRoots(cli.Root...),
		depgraph.WithPatterns(cli.Patterns...),
		depgraph.WithProviders(cli.Resolve...),
	)
	kctx.FatalIfErrorf(err)
	if len(graph.Missing) > 0 {
		for fn, missing := range graph.Missing {
			missingStr := []string{}
			for _, typ := range missing {
				missingStr = append(missingStr, typ.String())
			}
			kctx.Errorf("%s() is missing a provider for %s", fn.FullName(), strings.Join(missingStr, ", "))
		}
		kctx.Exit(1)
	}

	if cli.List {
		g := graph.Graph()
		for root, deps := range g {
			fmt.Printf("%s\n", root)
			for _, dep := range deps {
				fmt.Printf("  %s\n", dep)
			}
		}
		kctx.Exit(0)
	}

	w, err := os.Create(filepath.Join(cli.Dest, "zero.go"))
	kctx.FatalIfErrorf(err)
	err = generator.Generate(w, graph)
	kctx.FatalIfErrorf(err)
}
