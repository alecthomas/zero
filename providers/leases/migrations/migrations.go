// Package migrations contains SQL migrations for the leases provider.
package migrations

import (
	"embed"

	"github.com/alecthomas/zero/providers/sql"
)

//go:embed *.sql
var migrations embed.FS

//zero:provider weak multi
func Migrations() sql.Migrations {
	return sql.Migrations{migrations}
}
