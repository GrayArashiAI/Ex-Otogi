package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// keepOriginal keeps data, the picture under k, as seen in chat at now:
// its bytes once, however many chats it is seen in.
func (s *store) keepOriginal(ctx context.Context, k key, chat, mediaType string, data []byte, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("media store: keep original: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO originals (algorithm, digest, media_type, bytes, size, seen_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (algorithm, digest) DO UPDATE SET seen_at = excluded.seen_at`,
		k.Algorithm, k.Digest, mediaType, data, len(data), now.UnixMilli()); err != nil {
		return fmt.Errorf("media store: keep original: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO original_chats (algorithm, digest, chat) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`,
		k.Algorithm, k.Digest, chat); err != nil {
		return fmt.Errorf("media store: keep original: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("media store: keep original: %w", err)
	}
	return nil
}

// seenOriginal marks the picture under k as seen in chat at now, when its
// original is kept; kept is false when it is not.
func (s *store) seenOriginal(ctx context.Context, k key, chat string, now time.Time) (kept bool, err error) {
	res, err := s.db.ExecContext(ctx, `UPDATE originals SET seen_at = ? WHERE algorithm = ? AND digest = ?`, now.UnixMilli(), k.Algorithm, k.Digest)
	if err != nil {
		return false, fmt.Errorf("media store: mark original seen: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO original_chats (algorithm, digest, chat) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`,
		k.Algorithm, k.Digest, chat); err != nil {
		return false, fmt.Errorf("media store: mark original seen: %w", err)
	}
	return true, nil
}

// original returns the kept picture under k, as seen in chat: none for a
// chat it was not seen in.
func (s *store) original(ctx context.Context, k key, chat string) (mediaType string, data []byte, ok bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT o.media_type, o.bytes FROM originals o
		JOIN original_chats c ON c.algorithm = o.algorithm AND c.digest = o.digest
		WHERE o.algorithm = ? AND o.digest = ? AND c.chat = ?`, k.Algorithm, k.Digest, chat).Scan(&mediaType, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, false, nil
	}
	if err != nil {
		return "", nil, false, fmt.Errorf("media store: read original: %w", err)
	}
	return mediaType, data, true, nil
}

// pruneOriginals deletes the pictures whose descriptions are gone, then,
// past limit bytes in all, the longest unseen until they fit; with limit 0,
// all of them, as none are to be kept.
func (s *store) pruneOriginals(ctx context.Context, limit int64) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("media store: prune originals: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	gone := `DELETE FROM originals WHERE NOT EXISTS (SELECT 1 FROM descriptions d WHERE d.algorithm = originals.algorithm AND d.digest = originals.digest)`
	if limit == 0 {
		gone = `DELETE FROM originals`
	}
	res, err := tx.ExecContext(ctx, gone)
	if err != nil {
		return 0, fmt.Errorf("media store: prune originals: %w", err)
	}
	n, _ := res.RowsAffected()
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(sum(size), 0) FROM originals`).Scan(&total); err != nil {
		return n, fmt.Errorf("media store: prune originals: %w", err)
	}
	if total > limit {
		// The cutoff: the seen_at of the newest picture that has to go, the
		// longest unseen going first.
		var cutoff int64
		err := tx.QueryRowContext(ctx, `SELECT seen_at FROM (
				SELECT seen_at, sum(size) OVER (ORDER BY seen_at, algorithm, digest) AS upto FROM originals
			) WHERE upto >= ? ORDER BY seen_at LIMIT 1`, total-limit).Scan(&cutoff)
		if err != nil {
			return n, fmt.Errorf("media store: prune originals: %w", err)
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM originals WHERE seen_at <= ?`, cutoff)
		if err != nil {
			return n, fmt.Errorf("media store: prune originals: %w", err)
		}
		more, _ := res.RowsAffected()
		n += more
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM original_chats WHERE NOT EXISTS
		(SELECT 1 FROM originals o WHERE o.algorithm = original_chats.algorithm AND o.digest = original_chats.digest)`); err != nil {
		return n, fmt.Errorf("media store: prune originals: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return n, fmt.Errorf("media store: prune originals: %w", err)
	}
	return n, nil
}
