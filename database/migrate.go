package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"time"

	"crimsonledger/database/migrations"
)

// ErrNewerDatabase is returned when the database was migrated by a newer binary.
var ErrNewerDatabase = errors.New("database was created by a newer crimson-ledger")

var migrationName = regexp.MustCompile(`^(\d{4})_[a-z0-9_]+\.sql$`)

type migration struct {
	version int
	name    string
}

// Migrate applies every embedded migration newer than the database, each in its own transaction.
// It refuses (ErrNewerDatabase) when the database is newer than this binary.
func Migrate(ctx context.Context, db *sql.DB) error {
	return migrateFS(ctx, db, migrations.FS)
}

func migrateFS(ctx context.Context, db *sql.DB, files fs.FS) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations
		(version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	current, err := Version(ctx, db)
	if err != nil {
		return err
	}
	list, err := listMigrations(files)
	if err != nil {
		return err
	}
	if len(list) > 0 && current > list[len(list)-1].version {
		return ErrNewerDatabase
	}
	for _, m := range list {
		if m.version <= current {
			continue
		}
		if err := applyOne(ctx, db, files, m); err != nil {
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
	}
	return nil
}

// Version returns the newest applied migration, or 0 for a database with none.
func Version(ctx context.Context, db *sql.DB) (int, error) {
	var v int
	err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v)
	if err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return v, nil
}

// listMigrations returns the NNNN_name.sql files sorted, requiring versions 1..N with no gap or duplicate.
func listMigrations(files fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	var list []migration
	for _, e := range entries {
		m := migrationName.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("migration file %q does not match NNNN_name.sql", e.Name())
		}
		v, _ := strconv.Atoi(m[1])
		list = append(list, migration{version: v, name: e.Name()})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].version < list[j].version })
	for i, m := range list {
		if m.version != i+1 {
			return nil, fmt.Errorf("migration %s: expected version %04d (gap or duplicate)", m.name, i+1)
		}
	}
	return list, nil
}

func applyOne(ctx context.Context, db *sql.DB, files fs.FS, m migration) (err error) {
	body, err := fs.ReadFile(files, m.name)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.ExecContext(ctx, string(body)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		m.version, time.Now().UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}
