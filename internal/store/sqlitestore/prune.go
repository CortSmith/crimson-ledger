package sqlitestore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"crimsonledger/internal/store"
)

// Prune deletes fixes, kills and batch markers older than cutoff. Contacts, charts and keys stay.
func (s *Store) Prune(ctx context.Context, cutoff time.Time) (store.PruneResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return store.PruneResult{}, fmt.Errorf("prune: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	at := toMillis(cutoff)
	var res store.PruneResult
	for _, step := range []struct {
		query string
		count *int
	}{
		{`DELETE FROM fixes WHERE t < ?`, &res.Fixes},
		{`DELETE FROM kills WHERE t < ?`, &res.Kills},
		{`DELETE FROM batches WHERE received_at < ?`, &res.Batches},
	} {
		if *step.count, err = deleteBefore(ctx, tx, step.query, at); err != nil {
			return store.PruneResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return store.PruneResult{}, fmt.Errorf("prune: commit: %w", err)
	}
	return res, nil
}

func deleteBefore(ctx context.Context, tx *sql.Tx, query string, at int64) (int, error) {
	r, err := tx.ExecContext(ctx, query, at)
	if err != nil {
		return 0, fmt.Errorf("prune: %w", err)
	}
	n, err := r.RowsAffected()
	return int(n), err
}
