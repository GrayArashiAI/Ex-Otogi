package media

import (
	"context"
	"crypto/md5"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/pressly/goose/v3"

	contract "github.com/TundraWork/Ex-Otogi/pkg/media"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

//go:embed migrations/*.sql
var migrations embed.FS

// key names a piece of media by a digest of its content.
type key struct {
	Algorithm string
	Digest    []byte
}

func (k key) String() string { return k.Algorithm + ":" + hex.EncodeToString(k.Digest) }

// store is the module's private database: descriptions, sources, recent
// messages' media, and stickers.
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

// prune deletes descriptions and sources last seen before before, and
// recent media from before recentBefore.
func (s *store) prune(ctx context.Context, before, recentBefore time.Time) (int64, error) {
	var n int64
	for _, q := range []struct {
		sql string
		at  time.Time
	}{
		{`DELETE FROM descriptions WHERE seen_at < ?`, before},
		{`DELETE FROM sources WHERE seen_at < ?`, before},
		{`DELETE FROM recent_media WHERE at < ?`, recentBefore},
	} {
		res, err := s.db.ExecContext(ctx, q.sql, q.at.UnixMilli())
		if err != nil {
			return n, fmt.Errorf("media store: prune: %w", err)
		}
		rows, _ := res.RowsAffected()
		n += rows
	}
	return n, nil
}

// putRecent keeps the media of message source in d, seen at now.
func (s *store) putRecent(ctx context.Context, d otogi.Destination, source string, media []otogi.Media, now time.Time) error {
	encoded, err := json.Marshal(media)
	if err != nil {
		return fmt.Errorf("media store: keep recent media: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO recent_media (platform, account, conversation, source_id, media, at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (platform, account, conversation, source_id) DO UPDATE SET media = excluded.media, at = excluded.at`,
		d.Platform, d.Account, d.Conversation, source, string(encoded), now.UnixMilli())
	if err != nil {
		return fmt.Errorf("media store: keep recent media: %w", err)
	}
	return nil
}

// recent returns the media of message source in d, kept since since.
func (s *store) recent(ctx context.Context, d otogi.Destination, source string, since time.Time) ([]otogi.Media, bool, error) {
	var encoded string
	err := s.db.QueryRowContext(ctx, `SELECT media FROM recent_media WHERE platform = ? AND account = ? AND conversation = ? AND source_id = ? AND at >= ?`,
		d.Platform, d.Account, d.Conversation, source, since.UnixMilli()).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("media store: read recent media: %w", err)
	}
	var media []otogi.Media
	if err := json.Unmarshal([]byte(encoded), &media); err != nil {
		return nil, false, fmt.Errorf("media store: read recent media: %w", err)
	}
	return media, true, nil
}

// sticker is a kept sticker. Bytes is only read for its picture; AddedBy,
// AddedAt, and Packs, only for the list and the management plane.
type sticker struct {
	ID        int
	SHA256    []byte
	MediaType string
	Bytes     []byte
	Size      int64
	Kind      contract.Kind
	Summary   string
	Note      string
	AddedBy   string
	AddedAt   time.Time
	Packs     []string
}

// kept is st as an operator manages it.
func (st sticker) kept() contract.KeptSticker {
	return contract.KeptSticker{Sticker: st.listed(), Summary: st.Summary, Note: st.Note,
		MediaType: st.MediaType, Size: st.Size, SHA256: st.SHA256, AddedBy: st.AddedBy, AddedAt: st.AddedAt}
}

// listed is st as the stickers service gives it.
func (st sticker) listed() contract.Sticker {
	return contract.Sticker{ID: st.ID, Kind: st.Kind, Text: st.text(), Packs: st.Packs}
}

// text is what the sticker shows in a list: its note, else its summary.
func (st sticker) text() string {
	if st.Note != "" {
		return st.Note
	}
	return st.Summary
}

// addSticker keeps st, added by by at now, with the MD5 of its bytes, and
// returns its number, putting it in pack unless pack is empty, in one
// transaction; packed is whether that put it in pack. When a sticker with
// st's digest is kept already, as when a message is delivered again, it
// returns that one's number and existed, put in pack all the same. A pack
// that is not there is contract.ErrNoPack, and nothing is kept.
func (s *store) addSticker(ctx context.Context, st sticker, pack, by string, now time.Time) (id int, existed, packed bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, false, fmt.Errorf("media store: keep sticker: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if pack != "" {
		if err := packThere(ctx, tx, pack); err != nil {
			return 0, false, false, err
		}
	}
	sum := md5.Sum(st.Bytes)
	res, err := tx.ExecContext(ctx, `INSERT INTO stickers (sha256, md5, media_type, bytes, kind, summary, note, added_by, added_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (sha256) DO NOTHING`,
		st.SHA256, sum[:], st.MediaType, st.Bytes, string(st.Kind), st.Summary, st.Note, by, now.UnixMilli())
	if err != nil {
		return 0, false, false, fmt.Errorf("media store: keep sticker: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 1 {
		last, err := res.LastInsertId()
		if err != nil {
			return 0, false, false, fmt.Errorf("media store: keep sticker: %w", err)
		}
		id = int(last)
	} else {
		existed = true
		if err := tx.QueryRowContext(ctx, `SELECT id FROM stickers WHERE sha256 = ?`, st.SHA256).Scan(&id); err != nil {
			return 0, false, false, fmt.Errorf("media store: keep sticker: neither inserted nor kept: %w", err)
		}
	}
	if pack != "" {
		res, err := tx.ExecContext(ctx, `INSERT INTO sticker_pack_members (pack, sticker) VALUES (?, ?) ON CONFLICT DO NOTHING`, pack, id)
		if err != nil {
			return 0, false, false, fmt.Errorf("media store: keep sticker: %w", err)
		}
		n, _ := res.RowsAffected()
		packed = n == 1
	}
	if err := tx.Commit(); err != nil {
		return 0, false, false, fmt.Errorf("media store: keep sticker: %w", err)
	}
	return id, existed, packed, nil
}

// stickerQueries find a kept sticker by a key's digest, by algorithm.
var stickerQueries = map[string]string{
	"sha256": `SELECT id, kind, summary, note FROM stickers WHERE sha256 = ?`,
	"md5":    `SELECT id, kind, summary, note FROM stickers WHERE md5 = ?`,
}

// stickerByKey returns the kept sticker whose SHA-256 or MD5 is k, without
// its bytes; a key of another algorithm finds none.
func (s *store) stickerByKey(ctx context.Context, k key) (sticker, bool, error) {
	query, ok := stickerQueries[k.Algorithm]
	if !ok {
		return sticker{}, false, nil
	}
	var st sticker
	var kind string
	err := s.db.QueryRowContext(ctx, query, k.Digest).Scan(&st.ID, &kind, &st.Summary, &st.Note)
	if errors.Is(err, sql.ErrNoRows) {
		return sticker{}, false, nil
	}
	if err != nil {
		return sticker{}, false, fmt.Errorf("media store: read sticker: %w", err)
	}
	st.Kind = contract.Kind(kind)
	return st, true, nil
}

// stickerByDigest returns the number of the sticker with digest sum.
func (s *store) stickerByDigest(ctx context.Context, sum []byte) (int, bool, error) {
	var id int
	err := s.db.QueryRowContext(ctx, `SELECT id FROM stickers WHERE sha256 = ?`, sum).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("media store: read sticker: %w", err)
	}
	return id, true, nil
}

// stickers returns the kept stickers by number, without their bytes.
func (s *store) stickers(ctx context.Context) ([]sticker, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, sha256, media_type, length(bytes), kind, summary, note, added_by, added_at FROM stickers ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("media store: list stickers: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []sticker
	for rows.Next() {
		var st sticker
		var kind string
		var at int64
		if err := rows.Scan(&st.ID, &st.SHA256, &st.MediaType, &st.Size, &kind, &st.Summary, &st.Note, &st.AddedBy, &at); err != nil {
			return nil, fmt.Errorf("media store: list stickers: %w", err)
		}
		st.Kind, st.AddedAt = contract.Kind(kind), time.UnixMilli(at)
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("media store: list stickers: %w", err)
	}
	in, err := s.memberships(ctx)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Packs = in[out[i].ID]
	}
	return out, nil
}

// sticker returns sticker id without its bytes, with its packs.
func (s *store) sticker(ctx context.Context, id int) (sticker, bool, error) {
	all, err := s.stickers(ctx)
	if err != nil {
		return sticker{}, false, err
	}
	for _, st := range all {
		if st.ID == id {
			return st, true, nil
		}
	}
	return sticker{}, false, nil
}

// memberships returns the packs of each sticker in any, by number, each
// sorted.
func (s *store) memberships(ctx context.Context) (map[int][]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT sticker, pack FROM sticker_pack_members ORDER BY pack`)
	if err != nil {
		return nil, fmt.Errorf("media store: list sticker packs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	in := map[int][]string{}
	for rows.Next() {
		var id int
		var pack string
		if err := rows.Scan(&id, &pack); err != nil {
			return nil, fmt.Errorf("media store: list sticker packs: %w", err)
		}
		in[id] = append(in[id], pack)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("media store: list sticker packs: %w", err)
	}
	return in, nil
}

// stickerPicture returns sticker id with its bytes.
func (s *store) stickerPicture(ctx context.Context, id int) (sticker, bool, error) {
	st := sticker{ID: id}
	var kind string
	err := s.db.QueryRowContext(ctx, `SELECT sha256, media_type, bytes, kind, summary, note FROM stickers WHERE id = ?`, id).
		Scan(&st.SHA256, &st.MediaType, &st.Bytes, &kind, &st.Summary, &st.Note)
	if errors.Is(err, sql.ErrNoRows) {
		return sticker{}, false, nil
	}
	if err != nil {
		return sticker{}, false, fmt.Errorf("media store: read sticker: %w", err)
	}
	st.Kind, st.Size = contract.Kind(kind), int64(len(st.Bytes))
	return st, true, nil
}

// setStickerNote sets sticker id's note and returns the sticker.
func (s *store) setStickerNote(ctx context.Context, id int, note string) (sticker, bool, error) {
	st := sticker{ID: id}
	var kind string
	var at int64
	err := s.db.QueryRowContext(ctx, `UPDATE stickers SET note = ? WHERE id = ? RETURNING sha256, media_type, length(bytes), kind, summary, note, added_by, added_at`, note, id).
		Scan(&st.SHA256, &st.MediaType, &st.Size, &kind, &st.Summary, &st.Note, &st.AddedBy, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return sticker{}, false, nil
	}
	if err != nil {
		return sticker{}, false, fmt.Errorf("media store: note sticker: %w", err)
	}
	st.Kind, st.AddedAt = contract.Kind(kind), time.UnixMilli(at)
	return st, true, nil
}

// removeSticker deletes sticker id and returns what it was.
func (s *store) removeSticker(ctx context.Context, id int) (sticker, bool, error) {
	st := sticker{ID: id}
	var kind string
	err := s.db.QueryRowContext(ctx, `DELETE FROM stickers WHERE id = ? RETURNING sha256, media_type, length(bytes), kind, summary, note`, id).
		Scan(&st.SHA256, &st.MediaType, &st.Size, &kind, &st.Summary, &st.Note)
	if errors.Is(err, sql.ErrNoRows) {
		return sticker{}, false, nil
	}
	if err != nil {
		return sticker{}, false, fmt.Errorf("media store: remove sticker: %w", err)
	}
	st.Kind = contract.Kind(kind)
	return st, true, nil
}
