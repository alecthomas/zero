package depgraph

import (
	_ "embed"
	"runtime"
	"strings"
)

//go:generate /bin/sh -c "go version | cut -d' ' -f3 | cut -d. -f1-2 > stdlib.txt && go list std >> stdlib.txt"
//go:embed stdlib.txt
var rawStdlib string

// stdlib contains the list of stdlib packages for the version of Go that Zero is built with.
var stdlib = func() map[string]struct{} {
	lines := strings.SplitSeq(rawStdlib, "\n")
	for line := range lines {
		if !strings.HasPrefix(runtime.Version(), line) {
			panic("run go generate ./internal/depgraph")
		}
		break
	}
	out := make(map[string]struct{}, 512)
	for line := range lines {
		if strings.TrimSpace(line) != "" {
			out[line] = struct{}{}
		}
	}
	return out
}()
