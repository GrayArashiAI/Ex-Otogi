package media

import (
	"context"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/pressly/goose/v3"

	contract "github.com/TundraWork/Ex-Otogi/pkg/media"
)

//go:embed migrations/*.sql
var migrations embed.FS

// key names a piece of media by a digest of its content.
type key struct {
	Algorithm string
	Digest    []byte
}

func (k key) String() string { return k.Algorithm + ":" + hex.EncodeToString(k.Digest) }

// store is the module's private database: descriptions and sources.
type store struct{ db *sql.DB }

func openStore(ctx context.Context, db *sql.DB) (*store, error) {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("media store: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, sub)
	if err != nil {
		return nil, fmt.Errorf("media store: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return nil, fmt.Errorf("media store: migrate: %w", err)
	}
	return &store{db: db}, nil
}

// get returns the description kept under k.
func (s *store) get(ctx context.Context, k key) (contract.Description, bool, error) {
	var d contract.Description
	var kind string
	err := s.db.QueryRowContext(ctx, `SELECT kind, summary, text FROM descriptions WHERE algorithm = ? AND digest = ?`, k.Algorithm, k.Digest).
		Scan(&kind, &d.Summary, &d.Text)
	if errors.Is(err, sql.ErrNoRows) {
		return contract.Description{}, false, nil
	}
	if err != nil {
		return contract.Description{}, false, fmt.Errorf("media store: read description: %w", err)
	}
	d.Kind = contract.Kind(kind)
	return d, true, nil
}

// put keeps d under k, described by model at now.
func (s *store) put(ctx context.Context, k key, d contract.Description, model string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO descriptions (algorithm, digest, kind, summary, text, model, described_at, seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (algorithm, digest) DO UPDATE SET kind = excluded.kind, summary = excluded.summary, text = excluded.text,
			model = excluded.model, described_at = excluded.described_at, seen_at = excluded.seen_at`,
		k.Algorithm, k.Digest, string(d.Kind), d.Summary, d.Text, model, now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return fmt.Errorf("media store: keep description: %w", err)
	}
	return nil
}

// seen marks the description under k as seen at now, so it outlives the
// pruning while the media keeps coming back.
func (s *store) seen(ctx context.Context, k key, now time.Time) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE descriptions SET seen_at = ? WHERE algorithm = ? AND digest = ?`, now.UnixMilli(), k.Algorithm, k.Digest); err != nil {
		return fmt.Errorf("media store: mark seen: %w", err)
	}
	return nil
}

// source returns the key of the media that came from src.
func (s *store) source(ctx context.Context, src string) (key, bool, error) {
	var k key
	err := s.db.QueryRowContext(ctx, `SELECT algorithm, digest FROM sources WHERE source = ?`, src).Scan(&k.Algorithm, &k.Digest)
	if errors.Is(err, sql.ErrNoRows) {
		return key{}, false, nil
	}
	if err != nil {
		return key{}, false, fmt.Errorf("media store: read source: %w", err)
	}
	return k, true, nil
}

// putSource keeps that src is the media under k.
func (s *store) putSource(ctx context.Context, src string, k key, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sources (source, algorithm, digest, seen_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (source) DO UPDATE SET algorithm = excluded.algorithm, digest = excluded.digest, seen_at = excluded.seen_at`,
		src, k.Algorithm, k.Digest, now.UnixMilli())
	if err != nil {
		return fmt.Errorf("media store: keep source: %w", err)
	}
	return nil
}

// prune deletes descriptions and sources last seen before before.
func (s *store) prune(ctx context.Context, before time.Time) (int64, error) {
	var n int64
	for _, q := range []string{`DELETE FROM descriptions WHERE seen_at < ?`, `DELETE FROM sources WHERE seen_at < ?`} {
		res, err := s.db.ExecContext(ctx, q, before.UnixMilli())
		if err != nil {
			return n, fmt.Errorf("media store: prune: %w", err)
		}
		rows, _ := res.RowsAffected()
		n += rows
	}
	return n, nil
}
