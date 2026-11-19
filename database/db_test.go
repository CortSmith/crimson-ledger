package database

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func skipModeChecksOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes are not meaningful on Windows")
	}
}

func openTemp(t *testing.T) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data", "ledger.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return path, db
}

func TestOpen_Pragmas(t *testing.T) {
	_, db := openTemp(t)
	want := map[string]string{
		"journal_mode": "wal", "foreign_keys": "1", "busy_timeout": "5000", "synchronous": "1",
	}
	for pragma, expected := range want {
		var got string
		if err := db.QueryRowContext(context.Background(), "PRAGMA "+pragma).Scan(&got); err != nil {
			t.Fatalf("PRAGMA %s: %v", pragma, err)
		}
		if got != expected {
			t.Errorf("PRAGMA %s = %q, want %q", pragma, got, expected)
		}
	}
}

func TestOpen_FileMode0600(t *testing.T) {
	skipModeChecksOnWindows(t)
	path, db := openTemp(t)
	if _, err := db.ExecContext(context.Background(), "CREATE TABLE t (a)"); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Stat(path + suffix)
		if err != nil {
			t.Fatalf("stat %s: %v", path+suffix, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %v, want 0600", path+suffix, info.Mode().Perm())
		}
	}
}

func TestOpen_CreatesParentDir0700(t *testing.T) {
	skipModeChecksOnWindows(t)
	path, _ := openTemp(t)
	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("parent mode = %v, want 0700", info.Mode().Perm())
	}
}

func TestOpen_WhenPathIsSymlink_Refuses(t *testing.T) {
	skipModeChecksOnWindows(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "real.db")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.db")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(link); err == nil {
		t.Fatal("Open followed a symlink; want an error")
	}
}
