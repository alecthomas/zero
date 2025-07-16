package sql

import (
	"database/sql"
	"strings"

	"github.com/alecthomas/errors"
	_ "github.com/go-sql-driver/mysql"
)

//zero:provider weak
func NewMySQL(config Config) (*sql.DB, error) {
	if after, ok := strings.CutPrefix(config.DSN, "mysql://"); ok {
		config.DSN = after
	}
	db, err := sql.Open("mysql", config.DSN)
	if err != nil {
		return nil, errors.Errorf("failed to open MySQL connection: %w", err)
	}
	return db, nil
}
