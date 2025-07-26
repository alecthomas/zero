_help:
  @just -l

# Generate Zero code for examples
generate-examples:
  zero -C ./_examples/service --resolve github.com/alecthomas/zero/providers/sql.New ./_examples/service && go test -C ./_examples/service .
