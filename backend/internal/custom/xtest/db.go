// Package xtest provides shared helpers for tests of the fork's custom code.
//
// Tests run against SQLite by default. Set TEST_X_DB=postgres (and optionally TEST_X_PG_DSN)
// to run the same tests against Postgres; each test then gets its own throwaway database
// (the DSN's user needs CREATEDB).
package xtest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib" // registers "pgx"
	"github.com/pressly/goose/v3"

	upstreammigrations "github.com/sysadminsmedia/homebox/backend/internal/data/migrations"
	_ "github.com/sysadminsmedia/homebox/backend/internal/data/migrations/postgres" // upstream Go migrations
	_ "github.com/sysadminsmedia/homebox/backend/internal/data/migrations/sqlite3"  // upstream Go migrations
	_ "github.com/sysadminsmedia/homebox/backend/pkgs/cgofreesqlite"                // registers "sqlite3"
)

const defaultPgDSN = "postgres://postgres:pg@localhost:5432/postgres?sslmode=disable"

// DB is a test database handle.
type DB struct {
	SQL *sql.DB
	// Driver is the HBOX_DATABASE_DRIVER-style name: "postgres" or "sqlite3".
	Driver string
	// Dialect is the entgo dialect name: "postgres" or "sqlite3".
	Dialect string
}

// IsPostgres reports whether the tests were asked to run against Postgres.
func IsPostgres() bool { return os.Getenv("TEST_X_DB") == "postgres" }

func randName() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "x_test_" + hex.EncodeToString(b)
}

// Open returns an empty, isolated database for one test and registers cleanup.
func Open(t testing.TB) DB {
	t.Helper()
	if IsPostgres() {
		return openPostgres(t)
	}
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1&_time_format=sqlite", randName()))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1) // one connection keeps the shared in-memory DB alive and serialised
	t.Cleanup(func() { _ = db.Close() })
	return DB{SQL: db, Driver: "sqlite3", Dialect: "sqlite3"}
}

func openPostgres(t testing.TB) DB {
	t.Helper()
	dsn := os.Getenv("TEST_X_PG_DSN")
	if dsn == "" {
		dsn = defaultPgDSN
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	// A whole database per test (not a schema): upstream migrations hard-code "public.".
	name := randName()
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		_ = admin.Close()
		t.Fatalf("create database (is Postgres running? TEST_X_PG_DSN=%q): %v", dsn, err)
	}

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	u.Path = "/" + name

	db, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatalf("open postgres test database: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		_, _ = admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)")
		_ = admin.Close()
	})
	return DB{SQL: db, Driver: "postgres", Dialect: "postgres"}
}

// UpstreamMigrate applies upstream's migrations the same way the app does at startup
// (global goose, default goose_db_version table), so custom code is tested against the
// real upstream schema.
func UpstreamMigrate(t testing.TB, d DB) {
	t.Helper()
	fsys, err := upstreammigrations.Migrations(d.Driver)
	if err != nil {
		t.Fatalf("upstream migrations fs: %v", err)
	}
	goose.SetBaseFS(fsys)
	if err := goose.SetDialect(d.Driver); err != nil {
		t.Fatalf("goose dialect: %v", err)
	}
	if err := goose.UpContext(context.Background(), d.SQL, d.Driver); err != nil {
		t.Fatalf("upstream migrations: %v", err)
	}
}
