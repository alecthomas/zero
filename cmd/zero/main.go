package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/alecthomas/errors"
	"github.com/alecthomas/kong"
	"github.com/alecthomas/zero/internal/depgraph"
	"github.com/alecthomas/zero/internal/generator"
)

var cli struct {
	Version    kong.VersionFlag   `help:"Print the version and exit."`
	Chdir      kong.ChangeDirFlag `help:"Change to this directory before running." placeholder:"DIR" short:"C"`
	Debug      bool               `help:"Enable debug logging."`
	Tags       []string           `help:"Tags to enable during type analysis." placeholder:"TAG"`
	OutputTags []string           `help:"Tags to add to generated code."`
	Resolve    []string           `help:"Resolve an ambiguous type with this provider." placeholder:"REF"`
	List       bool               `help:"List all dependencies." xor:"action"`
	Root       []string           `help:"Prune dependencies outside these root types."  placeholder:"REF" short:"r"`
	Dest       string             `help:"Destination package directory for generated files." arg:"" type:"existingdir"`
	Patterns   []string           `help:"Additional packages pattern to scan." arg:"" optional:""`
}

func main() {
	version := "dev"
	if info, ok := debug.ReadBuildInfo(); ok {
		version = info.Main.Version
	}
	kctx := kong.Parse(&cli, kong.Vars{"version": version})
	extraOptions := []depgraph.Option{}
	if cli.Debug {
		extraOptions = append(extraOptions, depgraph.WithDebug(true))
	}

	// Verify/add the version of zero being used.
	err := ensureGoModuleVersion(kctx, version)
	kctx.FatalIfErrorf(err)

	graph, err := depgraph.Analyse(cli.Dest,
		depgraph.WithRoots(cli.Root...),
		depgraph.WithPatterns(cli.Patterns...),
		depgraph.WithProviders(cli.Resolve...),
		depgraph.WithOptions(extraOptions...),
		depgraph.WithTags(cli.Tags...),
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
	err = generator.Generate(w, graph, generator.WithTags(cli.OutputTags...))
	kctx.FatalIfErrorf(err)
}

func ensureGoModuleVersion(kctx *kong.Context, version string) error {
	if strings.Contains(version, "+dirty") {
		return nil
	}
	output, err := exec.Command("go", "list", "-m", "-f", "{{.Version}}", "github.com/alecthomas/zero").CombinedOutput() //nolint
	if err != nil {
		return fmt.Errorf("failed to get version of Go module github.com/alecthomas/zero: %w", err)
	}
	moduleVersion := strings.TrimSpace(string(output))
	if moduleVersion == "v0.0.0-00010101000000-000000000000" || moduleVersion == version {
		return nil
	}
	kctx.Printf("updating to github.com/alecthomas/zero@%s", version)
	cmd := exec.Command("go", "get", "github.com/alecthomas/zero@"+version) //nolint
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return errors.Wrap(cmd.Run(), "failed to update to github.com/alecthomas/zero@"+version)
}
