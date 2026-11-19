// Package sqlitestore implements store.Store on SQLite, using the schema owned by the database package.
package sqlitestore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"crimsonledger/database"
	"crimsonledger/internal/domain"
	"crimsonledger/internal/store"
)

// Store is a SQLite-backed store.Store.
type Store struct {
	db   *sql.DB
	keys *keyStore
}

var _ store.Store = (*Store)(nil)

// Open opens (creating it if needed) the ledger at path and brings its schema up to date.
func Open(path string) (*Store, error) {
	db, err := database.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite store: %w", err)
	}
	if err := database.Migrate(context.Background(), db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate sqlite store: %w", err)
	}
	return New(db), nil
}

// New wraps an already opened and migrated database. The Store owns it and closes it.
func New(db *sql.DB) *Store { return &Store{db: db, keys: &keyStore{db: db}} }

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Keys returns the API key store.
func (s *Store) Keys() store.KeyStore { return s.keys }

// Version returns the ping counter of a ruleset (0 before the first accepted ping).
func (s *Store) Version(ctx context.Context, rs domain.Ruleset) (int64, error) {
	var v int64
	err := s.db.QueryRowContext(ctx, `SELECT version FROM ping_versions WHERE ruleset = ?`, string(rs)).Scan(&v)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read version of %s: %w", rs, err)
	}
	return v, nil
}

// toMillis and fromMillis convert between time.Time and the epoch milliseconds stored in the file.
func toMillis(t time.Time) int64 { return t.UnixMilli() }

func fromMillis(ms int64) time.Time { return time.UnixMilli(ms).UTC() }
