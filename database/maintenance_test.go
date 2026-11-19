package database

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func migrated(t *testing.T) (string, *sql.DB) {
	t.Helper()
	path, db := openTemp(t)
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	return path, db
}

func TestBackup_ProducesOpenableFile(t *testing.T) {
	_, db := migrated(t)
	ctx := context.Background()
	dest := t.TempDir()
	file, err := Backup(ctx, db, dest, DefaultKeepBackups)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(file), "ledger-") || !strings.HasSuffix(file, ".db") {
		t.Errorf("unexpected backup name %s", file)
	}
	b, err := Open(file)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer b.Close()
	if err := Integrity(ctx, b); err != nil {
		t.Errorf("backup integrity: %v", err)
	}
	if v, _ := Version(ctx, b); v != 1 {
		t.Errorf("backup version = %d, want 1", v)
	}
}

func TestBackup_FileMode0600(t *testing.T) {
	skipModeChecksOnWindows(t)
	_, db := migrated(t)
	file, err := Backup(context.Background(), db, t.TempDir(), 7)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(file)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestBackup_KeepsNewestN(t *testing.T) {
	_, db := migrated(t)
	dir := t.TempDir()
	for _, name := range []string{"ledger-20200101-000000.db", "ledger-20200102-000000.db", "ledger-20200103-000000.db"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Backup(context.Background(), db, dir, 2); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("%d backups kept, want 2", len(entries))
	}
}

func TestBackup_QuoteInDirectoryIsEscaped(t *testing.T) {
	_, db := migrated(t)
	dir := filepath.Join(t.TempDir(), "it's")
	if _, err := Backup(context.Background(), db, dir, 7); err != nil {
		t.Fatalf("Backup into dir with quote: %v", err)
	}
}

func TestPrune_DeletesOldFixesAndBatchesOnly(t *testing.T) {
	_, db := migrated(t)
	ctx := context.Background()
	stmts := []string{
		`INSERT INTO contacts (ruleset,guid,name,raw,reaction,first_seen,last_seen) VALUES ('pvp','g','n','r','hostile',1,1)`,
		`INSERT INTO fixes VALUES ('pvp','g',10,1,1,1,'exact'), ('pvp','g',1000,1,1,1,'exact')`,
		`INSERT INTO kills VALUES ('pvp',10,'a','b',1)`,
		`INSERT INTO batches VALUES ('old',10), ('new',1000)`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	res, err := Prune(ctx, db, 500)
	if err != nil {
		t.Fatal(err)
	}
	if res.Fixes != 1 || res.Batches != 1 {
		t.Errorf("pruned %+v, want 1 fix and 1 batch", res)
	}
	count := func(table string) (n int) {
		_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n)
		return n
	}
	if count("contacts") != 1 || count("kills") != 1 || count("fixes") != 1 || count("batches") != 1 {
		t.Error("prune touched rows it must keep")
	}
}

func TestPrune_ChunksLargeDeletes(t *testing.T) {
	_, db := migrated(t)
	ctx := context.Background()
	tx, _ := db.BeginTx(ctx, nil)
	for i := 0; i < pruneChunk+50; i++ {
		if _, err := tx.ExecContext(ctx, `INSERT INTO fixes VALUES ('pvp','g',?,1,1,1,'exact')`, i); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	res, err := Prune(ctx, db, 1<<40)
	if err != nil || res.Fixes != pruneChunk+50 {
		t.Fatalf("Prune = %+v, %v", res, err)
	}
}

func TestIntegrity_OnHealthyDB(t *testing.T) {
	_, db := migrated(t)
	if err := Integrity(context.Background(), db); err != nil {
		t.Fatal(err)
	}
}

func TestVacuum_OnHealthyDB(t *testing.T) {
	_, db := migrated(t)
	if err := Vacuum(context.Background(), db); err != nil {
		t.Fatal(err)
	}
}

func TestMaintenance_OnClosedDB_ReturnError(t *testing.T) {
	_, db := migrated(t)
	ctx := context.Background()
	_ = db.Close()
	if _, err := Prune(ctx, db, 1); err == nil {
		t.Error("Prune on closed db: want error")
	}
	if _, err := Backup(ctx, db, t.TempDir(), 1); err == nil {
		t.Error("Backup on closed db: want error")
	}
	if Integrity(ctx, db) == nil || Vacuum(ctx, db) == nil || Migrate(ctx, db) == nil {
		t.Error("Integrity, Vacuum and Migrate on closed db: want errors")
	}
}

func TestOpen_WhenPathIsDirectory_ReturnsError(t *testing.T) {
	if _, err := Open(t.TempDir()); err == nil {
		t.Error("want error opening a directory")
	}
}
