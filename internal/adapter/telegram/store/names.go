package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store/queries"
)

// SaveNames stores display names by peer (user:<id>, chat:<id>, or
// channel:<id>), replacing older ones.
func (s *Store) SaveNames(ctx context.Context, names map[string]string) error {
	if len(names) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("save names: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	q := queries.New(tx)
	for peer, name := range names {
		if err := q.SaveName(ctx, queries.SaveNameParams{Peer: peer, Name: name}); err != nil {
			return fmt.Errorf("save name of %s: %w", peer, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("save names: %w", err)
	}
	return nil
}

// Name returns a peer's display name, or "" when none is known.
func (s *Store) Name(ctx context.Context, peer string) (string, error) {
	name, err := queries.New(s.db).Name(ctx, peer)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("name of %s: %w", peer, err)
	}
	return name, nil
}
