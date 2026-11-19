package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	_ "modernc.org/sqlite" // pure-Go driver, works with CGO_ENABLED=0
)

const (
	// defaultBusyTimeoutMS is how long a statement waits for the write lock.
	defaultBusyTimeoutMS = 5000
	// dirMode and fileMode keep the ledger private to its owner (PLAN.md section 4).
	dirMode  = 0o700
	fileMode = 0o600
)

// dsnPragmas are applied to every connection by the driver.
var dsnPragmas = "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(" + strconv.Itoa(defaultBusyTimeoutMS) + ")" +
	"&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)"

// Open opens (creating with mode 0600) the SQLite file with safe pragmas and one connection.
// The parent directory is created with mode 0700. A symlink at path is refused.
func Open(path string) (*sql.DB, error) {
	if err := prepareFile(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+dsnPragmas)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	// SQLite is a single writer; a pool of one removes SQLITE_BUSY at this load.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return db, nil
}

// prepareFile creates the parent directory and the file with private modes, before sqlite sees the path.
func prepareFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return fmt.Errorf("create database directory: %w", err)
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink; refusing to open it", path)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, fileMode) // #nosec G304: path comes from the owner's config
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}
