package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/qq/store/queries"
)

// Peer is what is known of a user or a group.
type Peer struct {
	Name        string
	Description string
	Members     int
	LookedUp    time.Time // the last group info lookup; zero for never
}

// Peer returns what is known of peer (user:<openid> or group:<openid>).
func (s *Store) Peer(ctx context.Context, peer string) (Peer, error) {
	r, err := queries.New(s.db).PeerInfo(ctx, peer)
	if errors.Is(err, sql.ErrNoRows) {
		return Peer{}, nil
	}
	if err != nil {
		return Peer{}, fmt.Errorf("qq store: peer %s: %w", peer, err)
	}
	p := Peer{Name: r.Name, Description: r.Description, Members: int(r.Members)}
	if r.LookedUp != 0 {
		p.LookedUp = time.UnixMilli(r.LookedUp)
	}
	return p, nil
}

// SetPeerName keeps the latest name a user or group was seen with.
func (s *Store) SetPeerName(ctx context.Context, peer, name string) error {
	if err := queries.New(s.db).SetPeerName(ctx, queries.SetPeerNameParams{Peer: peer, Name: name}); err != nil {
		return fmt.Errorf("qq store: set name of %s: %w", peer, err)
	}
	return nil
}

// SetGroupInfo keeps a group's details from the group info API.
func (s *Store) SetGroupInfo(ctx context.Context, peer string, p Peer) error {
	err := queries.New(s.db).SetGroupInfo(ctx, queries.SetGroupInfoParams{
		Peer: peer, Name: p.Name, Description: p.Description, Members: int64(p.Members), LookedUp: p.LookedUp.UnixMilli(),
	})
	if err != nil {
		return fmt.Errorf("qq store: set group info of %s: %w", peer, err)
	}
	return nil
}
