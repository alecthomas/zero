package depgraph

import (
	_ "embed"
	"runtime"
	"strings"
)

//go:generate /bin/sh -c "go list std > stdlib.txt"
//go:embed stdlib.txt
var rawStdlib string

// stdlib contains the list of stdlib packages for the version of Go that Zero is built with.
var stdlib = func() map[string]struct{} {
	if !strings.HasPrefix(runtime.Version(), "go1.24") {
		panic("run go generate ./internal/depgraph")
	}
	out := make(map[string]struct{}, 512)
	for line := range strings.SplitSeq(rawStdlib, "\n") {
		if strings.TrimSpace(line) != "" {
			out[line] = struct{}{}
		}
	}
	return out
}()
