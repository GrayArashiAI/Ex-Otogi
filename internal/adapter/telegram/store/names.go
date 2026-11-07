package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store/queries"
)

// Peer is what the entities of updates say of a user, chat, or channel.
type Peer struct {
	// Name is a user's display name, or a chat's or channel's title.
	Name string
	// Bot marks a bot user.
	Bot bool
}

// SavePeers stores peers by ID (user:<id>, chat:<id>, or channel:<id>),
// replacing what was known.
func (s *Store) SavePeers(ctx context.Context, peers map[string]Peer) error {
	if len(peers) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("save peers: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	q := queries.New(tx)
	for peer, p := range peers {
		if err := q.SavePeer(ctx, queries.SavePeerParams{Peer: peer, Name: p.Name, Bot: p.Bot}); err != nil {
			return fmt.Errorf("save peer %s: %w", peer, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("save peers: %w", err)
	}
	return nil
}

// Peer returns what is known of a peer; nothing known is a zero Peer.
func (s *Store) Peer(ctx context.Context, peer string) (Peer, error) {
	row, err := queries.New(s.db).PeerInfo(ctx, peer)
	if errors.Is(err, sql.ErrNoRows) {
		return Peer{}, nil
	}
	if err != nil {
		return Peer{}, fmt.Errorf("peer %s: %w", peer, err)
	}
	return Peer{Name: row.Name, Bot: row.Bot}, nil
}
