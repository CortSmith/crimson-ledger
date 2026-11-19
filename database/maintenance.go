package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// pruneChunk bounds one DELETE so the write lock is never held long.
	pruneChunk = 10000
	// keepBackups is the default number of newest backups kept.
	keepBackups = 7
	// backupLayout names backups ledger-YYYYMMDD-HHMMSS.db.
	backupLayout = "20060102-150405"
)

// DefaultKeepBackups is the number of backups kept when the caller has no preference.
const DefaultKeepBackups = keepBackups

const (
	deleteOldFixes = `DELETE FROM fixes WHERE (ruleset, guid, t, map_id) IN
		(SELECT ruleset, guid, t, map_id FROM fixes WHERE t < ? LIMIT ?)`
	deleteOldBatches = `DELETE FROM batches WHERE batch_id IN (SELECT batch_id FROM batches WHERE received_at < ? LIMIT ?)`
)

// PruneResult counts the rows Prune removed.
type PruneResult struct{ Fixes, Batches int }

// Prune deletes fixes older than cutoff (epoch ms) and batch ids received before it, in chunks.
// Contacts, kills, charts and keys are never touched.
func Prune(ctx context.Context, db *sql.DB, cutoff int64) (PruneResult, error) {
	fixes, err := deleteChunked(ctx, db, deleteOldFixes, cutoff)
	if err != nil {
		return PruneResult{}, fmt.Errorf("prune fixes: %w", err)
	}
	batches, err := deleteChunked(ctx, db, deleteOldBatches, cutoff)
	if err != nil {
		return PruneResult{Fixes: fixes}, fmt.Errorf("prune batches: %w", err)
	}
	return PruneResult{Fixes: fixes, Batches: batches}, nil
}

func deleteChunked(ctx context.Context, db *sql.DB, query string, cutoff int64) (int, error) {
	total := 0
	for {
		res, err := db.ExecContext(ctx, query, cutoff, pruneChunk)
		if err != nil {
			return total, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, err
		}
		total += int(n)
		if n < pruneChunk {
			return total, nil
		}
	}
}

// Backup writes a consistent copy of db into destDir as ledger-YYYYMMDD-HHMMSS.db (mode 0600)
// and deletes all but the newest keep backups there. It returns the new file's path.
func Backup(ctx context.Context, db *sql.DB, destDir string, keep int) (string, error) {
	if err := os.MkdirAll(destDir, dirMode); err != nil {
		return "", fmt.Errorf("create backup directory: %w", err)
	}
	dest := filepath.Join(destDir, "ledger-"+time.Now().UTC().Format(backupLayout)+".db")
	// VACUUM INTO does not accept a bound parameter on every sqlite version: quote the literal.
	literal := "'" + strings.ReplaceAll(dest, "'", "''") + "'"
	if _, err := db.ExecContext(ctx, "VACUUM INTO "+literal); err != nil { //nolint:gosec // G202: the path is quoted above
		return "", fmt.Errorf("backup: %w", err)
	}
	if err := os.Chmod(dest, fileMode); err != nil {
		return "", fmt.Errorf("restrict backup: %w", err)
	}
	return dest, removeOldBackups(destDir, keep)
}

func removeOldBackups(dir string, keep int) error {
	matches, err := filepath.Glob(filepath.Join(dir, "ledger-*.db"))
	if err != nil {
		return err
	}
	sort.Strings(matches) // the timestamp in the name sorts oldest first
	for len(matches) > keep && keep > 0 {
		if err := os.Remove(matches[0]); err != nil {
			return fmt.Errorf("remove old backup: %w", err)
		}
		matches = matches[1:]
	}
	return nil
}

// Integrity runs PRAGMA integrity_check and PRAGMA foreign_key_check and reports the first problem.
func Integrity(ctx context.Context, db *sql.DB) error {
	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return fmt.Errorf("integrity_check: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("integrity_check: %s", result)
	}
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("foreign_key_check: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		return fmt.Errorf("foreign_key_check: violations found")
	}
	return rows.Err()
}

// Vacuum rebuilds the database file to reclaim space.
func Vacuum(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, "VACUUM"); err != nil {
		return fmt.Errorf("vacuum: %w", err)
	}
	return nil
}
