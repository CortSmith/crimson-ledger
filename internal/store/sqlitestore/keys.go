package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"crimsonledger/internal/store"
)

type keyStore struct{ db *sql.DB }

const keyCols = `id, scope, label, hash, created_at, expires_at, disabled, last_used_at`

// Put adds a key; ErrConflict if the id exists.
func (k *keyStore) Put(ctx context.Context, r store.KeyRecord) error {
	const q = `INSERT INTO api_keys (` + keyCols + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (id) DO NOTHING`
	res, err := k.db.ExecContext(ctx, q, r.ID, string(r.Scope), r.Label, r.Hash, toMillis(r.CreatedAt),
		nullMillis(r.ExpiresAt), boolInt(r.Disabled), nullMillis(r.LastUsedAt))
	if err != nil {
		return fmt.Errorf("put key %s: %w", r.ID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("put key %s: %w", r.ID, store.ErrConflict)
	}
	return nil
}

// Get returns a key; ErrNotFound if the id is unknown.
func (k *keyStore) Get(ctx context.Context, id string) (store.KeyRecord, error) {
	r, err := scanKey(k.db.QueryRowContext(ctx, `SELECT `+keyCols+` FROM api_keys WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return store.KeyRecord{}, fmt.Errorf("get key %s: %w", id, store.ErrNotFound)
	}
	if err != nil {
		return store.KeyRecord{}, fmt.Errorf("get key %s: %w", id, err)
	}
	return r, nil
}

// List returns every key, oldest first.
func (k *keyStore) List(ctx context.Context) ([]store.KeyRecord, error) {
	rows, err := k.db.QueryContext(ctx, `SELECT `+keyCols+` FROM api_keys ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list keys: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []store.KeyRecord
	for rows.Next() {
		r, err := scanKey(rows)
		if err != nil {
			return nil, fmt.Errorf("list keys: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetDisabled revokes or restores a key.
func (k *keyStore) SetDisabled(ctx context.Context, id string, disabled bool) error {
	return k.update(ctx, id, `UPDATE api_keys SET disabled = ? WHERE id = ?`, boolInt(disabled))
}

// Touch records last_used_at (not throttled here: SQLite writes are cheap).
func (k *keyStore) Touch(ctx context.Context, id string, now time.Time) error {
	return k.update(ctx, id, `UPDATE api_keys SET last_used_at = ? WHERE id = ?`, toMillis(now))
}

func (k *keyStore) update(ctx context.Context, id, query string, value any) error {
	res, err := k.db.ExecContext(ctx, query, value, id)
	if err != nil {
		return fmt.Errorf("update key %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("update key %s: %w", id, store.ErrNotFound)
	}
	return nil
}

type scanner interface{ Scan(dest ...any) error }

func scanKey(s scanner) (store.KeyRecord, error) {
	var r store.KeyRecord
	var scope string
	var created int64
	var expires, used sql.NullInt64
	var disabled int
	if err := s.Scan(&r.ID, &scope, &r.Label, &r.Hash, &created, &expires, &disabled, &used); err != nil {
		return store.KeyRecord{}, err
	}
	r.Scope, r.CreatedAt, r.Disabled = store.Scope(scope), fromMillis(created), disabled != 0
	r.ExpiresAt, r.LastUsedAt = optTime(expires), optTime(used)
	return r, nil
}

func optTime(n sql.NullInt64) *time.Time {
	if !n.Valid {
		return nil
	}
	t := fromMillis(n.Int64)
	return &t
}

func nullMillis(t *time.Time) any {
	if t == nil {
		return nil
	}
	return toMillis(*t)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
