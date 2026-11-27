// Package migrations applies the fork's own schema migrations.
//
// They are tracked in their own goose version table (x_goose_db_version), separate from
// upstream's goose_db_version, so upstream migrations can be added in any order without goose
// reporting "missing" versions (.fork/PLAN.md R3). Files use sequential versions (00001_...).
// SQL only: upstream registers Go migrations in goose's global registry, which is disabled here.
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
)

// TableName is the goose version table for custom migrations.
const TableName = "x_goose_db_version"

//go:embed postgres/*.sql
var postgresFS embed.FS

//go:embed sqlite3/*.sql
var sqliteFS embed.FS

// Provider builds a goose provider for the custom migrations of the given driver
// ("postgres", "sqlite3" or "sqlite", matching upstream's HBOX_DATABASE_DRIVER values).
func Provider(db *sql.DB, driver string) (*goose.Provider, error) {
	var (
		dialect database.Dialect
		files   fs.FS
		dir     string
	)
	switch driver {
	case "postgres":
		dialect, files, dir = database.DialectPostgres, postgresFS, "postgres"
	case "sqlite3", "sqlite":
		dialect, files, dir = database.DialectSQLite3, sqliteFS, "sqlite3"
	default:
		return nil, fmt.Errorf("custom migrations: unsupported driver %q", driver)
	}

	sub, err := fs.Sub(files, dir)
	if err != nil {
		return nil, err
	}
	store, err := database.NewStore(dialect, TableName)
	if err != nil {
		return nil, err
	}
	return goose.NewProvider(goose.DialectCustom, db, sub,
		goose.WithStore(store),
		goose.WithDisableGlobalRegistry(true),
	)
}

// Run applies all pending custom migrations and returns the versions applied.
func Run(ctx context.Context, db *sql.DB, driver string) ([]int64, error) {
	p, err := Provider(db, driver)
	if err != nil {
		return nil, err
	}
	results, err := p.Up(ctx)
	if err != nil {
		return nil, fmt.Errorf("custom migrations: %w", err)
	}
	applied := make([]int64, 0, len(results))
	for _, r := range results {
		applied = append(applied, r.Source.Version)
	}
	return applied, nil
}
