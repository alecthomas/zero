package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/alecthomas/errors"
	"github.com/alecthomas/kong"
	kongtoml "github.com/alecthomas/kong-toml"
	"github.com/alecthomas/zero/internal/depgraph"
	"github.com/alecthomas/zero/internal/generator"
	"github.com/kballard/go-shellquote"
)

var cli struct {
	Config     kong.ConfigFlag    `help:"Path to the configuration file." placeholder:"FILE" short:"c"`
	Version    kong.VersionFlag   `help:"Print the version and exit."`
	Chdir      kong.ChangeDirFlag `help:"Change to this directory before running." placeholder:"DIR" short:"C"`
	Debug      bool               `help:"Enable debug logging." short:"d"`
	Tags       []string           `help:"Tags to enable during type analysis (will also be read from $GOFLAGS)." placeholder:"TAG" short:"t"`
	OutputTags []string           `help:"Tags to add to generated code." placeholder:"TAG" short:"T"`
	Resolve    []string           `help:"Resolve an ambiguous type with this provider." placeholder:"REF" short:"r"`
	List       bool               `group:"Actions:" help:"List all dependencies." xor:"action"`
	OpenAPI    string             `group:"Actions:" name:"openapi" help:"Generate OpenAPI specification." xor:"action" placeholder:"TITLE:VERSION"`
	Root       []string           `help:"Prune dependencies outside these root types."  placeholder:"REF" short:"R"`
	Dest       string             `help:"Destination package directory for generated files." default:"."`
	Patterns   []string           `help:"Additional packages pattern to scan." arg:"" optional:""`
}

func main() {
	version := "dev"
	if info, ok := debug.ReadBuildInfo(); ok {
		version = info.Main.Version
	}
	kctx := kong.Parse(&cli, kong.Vars{"version": version}, kong.Configuration(kongtoml.Loader, ".zero.toml"))
	extraOptions := []depgraph.Option{}
	if cli.Debug {
		extraOptions = append(extraOptions, depgraph.WithDebug(true))
	}
	ctx := context.Background()

	// Verify/add the version of zero being used.
	err := ensureGoModuleVersion(kctx, version)
	kctx.FatalIfErrorf(err)

	cli.Dest, err = filepath.Abs(filepath.Join(string(cli.Chdir), cli.Dest))
	kctx.FatalIfErrorf(err)

	// Combine explicit tags and tags from GOFLAGS
	tags := append(cli.Tags, parseGoTags()...)

	graph, err := depgraph.Analyse(ctx, cli.Dest,
		depgraph.WithRoots(cli.Root...),
		depgraph.WithPatterns(cli.Patterns...),
		depgraph.WithProviders(cli.Resolve...),
		depgraph.WithOptions(extraOptions...),
		depgraph.WithTags(tags...),
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

	// Run actions if any
	switch {
	case cli.List:
		g := graph.Graph()
		for root, deps := range g {
			fmt.Printf("%s\n", root)
			for _, dep := range deps {
				fmt.Printf("  %s\n", dep)
			}
		}
		kctx.Exit(0)

	case cli.OpenAPI != "":
		title, version, ok := strings.Cut(cli.OpenAPI, ":")
		if !ok {
			kctx.Fatalf("expected --openapi=TITLE:VERSION")
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(graph.GenerateOpenAPISpec(title, version)); err != nil {
			kctx.Fatalf("failed to encode OpenAPI spec: %v", err)
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
	if err := cmd.Run(); err != nil {
		return errors.Wrap(err, "failed to update to github.com/alecthomas/zero@"+version)
	}
	cmd = exec.Command("go", "mod", "tidy") //nolint
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return errors.Wrap(err, "failed to update to github.com/alecthomas/zero@"+version)
	}
	return nil
}

func parseGoTags() []string {
	goFlags := os.Getenv("GOFLAGS")
	words, err := shellquote.Split(goFlags)
	if err != nil {
		return nil
	}
	tags := []string{}
	for _, word := range words {
		if strings.HasPrefix(word, "-tags=") {
			tags = append(tags, strings.Split(word[6:], ",")...)
		} else if strings.HasPrefix(word, "--tags=") {
			tags = append(tags, strings.Split(word[7:], ",")...)
		}
	}
	return tags
}
