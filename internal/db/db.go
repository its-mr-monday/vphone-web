// Package db manages the SQLite database connection and schema migrations.
//
// Migrations are plain SQL files embedded at build time and applied in
// lexical order. Applied migrations are tracked in the schema_migrations
// table so re-running is idempotent.
package db

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no CGO)
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Open opens (creating if needed) the SQLite database at path, applies any
// pending migrations, and returns the connection.
func Open(path string) (*sql.DB, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create data dir %s: %w", dir, err)
		}
	}

	// _pragma options enable WAL and foreign keys for the modernc driver.
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}

	// modernc.org/sqlite does not tolerate concurrent writers well; serialize.
	sqlDB.SetMaxOpenConns(1)

	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	if err := migrate(sqlDB); err != nil {
		sqlDB.Close()
		return nil, err
	}

	slog.Info("database ready", "path", path)
	return sqlDB, nil
}

// migrate applies all embedded migrations that have not yet run.
func migrate(sqlDB *sql.DB) error {
	if _, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".sql" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		var exists bool
		if err := sqlDB.QueryRow(
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name = ?)`, name,
		).Scan(&exists); err != nil {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		if exists {
			continue
		}

		content, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		tx, err := sqlDB.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", name, err)
		}
		if _, err := tx.Exec(string(content)); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (name) VALUES (?)`, name); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
		slog.Info("applied migration", "name", name)
	}

	return nil
}
