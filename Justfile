_help:
    @just -l

# Generate Zero code for examples
generate-examples:
    zero -C ./_examples/service . && go test -C ./_examples/service .

# Run tests
test:
    go test ./...

# Run linter
lint:
    golangci-lint run

# Tag and push a new release out
release:
    git diff --exit-code
    git tag $(svu next)
    git push --tags

# Run "go mod tidy" in all modules
tidy:
    git ls-files | grep go.mod | xargs dirname | xargs -I {} go mod -C {} tidy

# Run the exemplar with hot reload
dev:
    #!/bin/bash
    reflex -d fancy -c <(cat <<EOF
    -r '\.sql$' -- sqlc generate
    -s -r '\.(go|gohtml)$' -R 'zero.go$' -- sh -c 'zero && go run -C ./_examples/service . --log-level=debug'
    EOF)
