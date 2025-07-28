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
