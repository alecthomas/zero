package sql

import (
	"database/sql"

	"github.com/alecthomas/errors"
	_ "github.com/jackc/pgx/v5/stdlib"
)

//zero:provider weak
func NewPostgreSQL(config Config) (*sql.DB, error) {
	db, err := sql.Open("pgx", config.DSN)
	if err != nil {
		return nil, errors.Errorf("failed to open PGX connection: %w", err)
	}
	return db, nil
}
