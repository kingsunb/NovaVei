// Package testutil provides shared helpers for test suites, including a
// database initializer that respects the NOVAVEIL_TEST_DB_TYPE /
// NOVAVEIL_TEST_DB_DSN environment variables so the same tests can run
// against SQLite (default), PostgreSQL, or MySQL in a CI service matrix.
package testutil

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kingsunb/NovaVeil/internal/db"
)

// InitTestDB initialises a test database selected by environment variables.
//
//   - NOVAVEIL_TEST_DB_TYPE: database type ("sqlite" | "postgres" | "mysql").
//     Defaults to "sqlite" when unset.
//   - NOVAVEIL_TEST_DB_DSN:  connection DSN for non-SQLite backends.
//     Required for postgres/mysql; ignored (temp file is used) for sqlite.
//
// The returned cleanup function removes the temp directory for SQLite and is
// a no-op for external databases (the CI service container is torn down by the
// runner). Callers should defer cleanup immediately after a successful return.
func InitTestDB() (func(), error) {
	dbType := os.Getenv("NOVAVEIL_TEST_DB_TYPE")
	if dbType == "" {
		dbType = "sqlite"
	}
	dsn := os.Getenv("NOVAVEIL_TEST_DB_DSN")

	if dbType == "sqlite" {
		dir, err := os.MkdirTemp("", "test-db-*")
		if err != nil {
			return nil, err
		}
		if dsn == "" {
			dsn = filepath.Join(dir, "test.db")
		}
		cleanup := func() { _ = os.RemoveAll(dir) }
		return cleanup, db.InitDB(dbType, dsn, false)
	}

	if dsn == "" {
		return nil, fmt.Errorf("NOVAVEIL_TEST_DB_DSN is required when NOVAVEIL_TEST_DB_TYPE=%s", dbType)
	}
	return func() {}, db.InitDB(dbType, dsn, false)
}
