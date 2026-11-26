package media

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	contract "github.com/TundraWork/Ex-Otogi/pkg/media"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// packID is what a sticker pack's ID matches.
var packID = regexp.MustCompile(contract.PackIDPattern)

// makePack makes pack p, with no stickers, by by.
func (m *Module) makePack(ctx context.Context, p contract.StickerPack, by string) (contract.StickerPack, error) {
	if !packID.MatchString(p.ID) || strings.TrimSpace(p.Title) == "" {
		return contract.StickerPack{}, contract.ErrInvalidPack
	}
	if err := m.store.makePack(ctx, p, by, m.now()); err != nil {
		return contract.StickerPack{}, err
	}
	m.recordPack(ctx, map[string]any{"action": "create", "pack": p.ID, "title": p.Title, "by": by})
	return contract.StickerPack{ID: p.ID, Title: p.Title, Description: p.Description}, nil
}

// editPack sets pack id's title and description, by by.
func (m *Module) editPack(ctx context.Context, id, title, description, by string) (contract.StickerPack, error) {
	if strings.TrimSpace(title) == "" {
		return contract.StickerPack{}, contract.ErrInvalidPack
	}
	p, err := m.store.editPack(ctx, id, title, description)
	if err != nil {
		return contract.StickerPack{}, err
	}
	m.recordPack(ctx, map[string]any{"action": "edit", "pack": id, "title": title, "by": by})
	return p, nil
}

// removePack removes pack id, by by, and with contract.RemoveStickers the
// stickers in no other pack, each recorded as removed too.
func (m *Module) removePack(ctx context.Context, id string, how contract.RemovePack, by string) ([]int, error) {
	if how != contract.KeepStickers && how != contract.RemoveStickers {
		return nil, fmt.Errorf("media: remove sticker pack %q: stickers %q: %w", id, how, contract.ErrInvalidPack)
	}
	p, gone, err := m.store.removePack(ctx, id, how == contract.RemoveStickers)
	if err != nil {
		return nil, err
	}
	removed := make([]int, len(gone))
	for i, st := range gone {
		removed[i] = st.ID
		payload := stickerPayload("remove", st, by)
		payload["pack"] = id
		m.recordSticker(ctx, nil, "ok", payload)
	}
	m.recordPack(ctx, map[string]any{"action": "remove", "pack": id, "title": p.Title, "stickers": string(how), "removed": removed, "by": by})
	return removed, nil
}

// packSticker puts sticker id into pack, or takes it out, by by, on e when
// a command did it, recording a change.
func (m *Module) packSticker(ctx context.Context, pack string, id int, in bool, by string, e *otogi.Event) error {
	st, changed, err := m.store.packSticker(ctx, pack, id, in)
	if err != nil || !changed {
		return err
	}
	action := "unpack"
	if in {
		action = "pack"
	}
	payload := stickerPayload(action, st, by)
	payload["pack"] = pack
	m.recordSticker(ctx, e, "ok", payload)
	return nil
}

// stickerPayload is a media.sticker record's payload for action on st.
func stickerPayload(action string, st sticker, by string) map[string]any {
	return map[string]any{"action": action, "id": st.ID, "sha256": hex.EncodeToString(st.SHA256), "kind": st.Kind, "text": st.text(), "by": by, "bytes": st.Size}
}

// recordPack records a pack's change as media.sticker_pack, on no event:
// only the management plane changes packs.
func (m *Module) recordPack(ctx context.Context, payload map[string]any) {
	m.host.Record(ctx, otogi.Record{Kind: "media.sticker_pack", Outcome: "ok", Payload: payload})
}

// querier is a database or a transaction.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// packThere fails with contract.ErrNoPack when there is no pack id.
func packThere(ctx context.Context, q querier, id string) error {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM sticker_packs WHERE id = ?`, id).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return contract.ErrNoPack
	case err != nil:
		return fmt.Errorf("media store: read sticker pack: %w", err)
	}
	return nil
}

// packs returns every pack by ID, with its stickers by number.
func (s *store) packs(ctx context.Context) ([]contract.StickerPack, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, title, description FROM sticker_packs ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("media store: list sticker packs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []contract.StickerPack
	for rows.Next() {
		var p contract.StickerPack
		if err := rows.Scan(&p.ID, &p.Title, &p.Description); err != nil {
			return nil, fmt.Errorf("media store: list sticker packs: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("media store: list sticker packs: %w", err)
	}
	members, err := s.db.QueryContext(ctx, `SELECT pack, sticker FROM sticker_pack_members ORDER BY sticker`)
	if err != nil {
		return nil, fmt.Errorf("media store: list sticker packs: %w", err)
	}
	defer func() { _ = members.Close() }()
	in := map[string][]int{}
	for members.Next() {
		var pack string
		var id int
		if err := members.Scan(&pack, &id); err != nil {
			return nil, fmt.Errorf("media store: list sticker packs: %w", err)
		}
		in[pack] = append(in[pack], id)
	}
	if err := members.Err(); err != nil {
		return nil, fmt.Errorf("media store: list sticker packs: %w", err)
	}
	for i := range out {
		out[i].Stickers = in[out[i].ID]
	}
	return out, nil
}

// makePack makes pack p, by by at now; one with its ID is
// contract.ErrPackExists.
func (s *store) makePack(ctx context.Context, p contract.StickerPack, by string, now time.Time) error {
	res, err := s.db.ExecContext(ctx, `INSERT INTO sticker_packs (id, title, description, made_by, made_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT (id) DO NOTHING`,
		p.ID, p.Title, p.Description, by, now.UnixMilli())
	if err != nil {
		return fmt.Errorf("media store: make sticker pack: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return contract.ErrPackExists
	}
	return nil
}

// editPack sets pack id's title and description and returns it.
func (s *store) editPack(ctx context.Context, id, title, description string) (contract.StickerPack, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE sticker_packs SET title = ?, description = ? WHERE id = ?`, title, description, id)
	if err != nil {
		return contract.StickerPack{}, fmt.Errorf("media store: edit sticker pack: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return contract.StickerPack{}, contract.ErrNoPack
	}
	all, err := s.packs(ctx)
	if err != nil {
		return contract.StickerPack{}, err
	}
	for _, p := range all {
		if p.ID == id {
			return p, nil
		}
	}
	return contract.StickerPack{}, contract.ErrNoPack // removed meanwhile
}

// removePack removes pack id, and with stickers too the stickers in no other
// pack, in one transaction. It returns the pack and the stickers removed.
func (s *store) removePack(ctx context.Context, id string, stickers bool) (contract.StickerPack, []sticker, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return contract.StickerPack{}, nil, fmt.Errorf("media store: remove sticker pack: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	p := contract.StickerPack{ID: id}
	err = tx.QueryRowContext(ctx, `SELECT title, description FROM sticker_packs WHERE id = ?`, id).Scan(&p.Title, &p.Description)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return contract.StickerPack{}, nil, contract.ErrNoPack
	case err != nil:
		return contract.StickerPack{}, nil, fmt.Errorf("media store: remove sticker pack: %w", err)
	}
	var removed []sticker
	if stickers {
		rows, err := tx.QueryContext(ctx, `DELETE FROM stickers WHERE id IN (
				SELECT sticker FROM sticker_pack_members WHERE pack = ?1
				AND sticker NOT IN (SELECT sticker FROM sticker_pack_members WHERE pack != ?1))
			RETURNING id, sha256, length(bytes), kind, summary, note`, id)
		if err != nil {
			return contract.StickerPack{}, nil, fmt.Errorf("media store: remove sticker pack: %w", err)
		}
		for rows.Next() {
			var st sticker
			var kind string
			if err := rows.Scan(&st.ID, &st.SHA256, &st.Size, &kind, &st.Summary, &st.Note); err != nil {
				_ = rows.Close()
				return contract.StickerPack{}, nil, fmt.Errorf("media store: remove sticker pack: %w", err)
			}
			st.Kind = contract.Kind(kind)
			removed = append(removed, st)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return contract.StickerPack{}, nil, fmt.Errorf("media store: remove sticker pack: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sticker_packs WHERE id = ?`, id); err != nil {
		return contract.StickerPack{}, nil, fmt.Errorf("media store: remove sticker pack: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return contract.StickerPack{}, nil, fmt.Errorf("media store: remove sticker pack: %w", err)
	}
	return p, removed, nil
}

// packSticker puts sticker id into pack, or takes it out, in one
// transaction, returning the sticker and whether that changed anything.
func (s *store) packSticker(ctx context.Context, pack string, id int, in bool) (sticker, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return sticker{}, false, fmt.Errorf("media store: pack sticker: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := packThere(ctx, tx, pack); err != nil {
		return sticker{}, false, err
	}
	st := sticker{ID: id}
	var kind string
	err = tx.QueryRowContext(ctx, `SELECT sha256, length(bytes), kind, summary, note FROM stickers WHERE id = ?`, id).
		Scan(&st.SHA256, &st.Size, &kind, &st.Summary, &st.Note)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return sticker{}, false, contract.ErrNoSticker
	case err != nil:
		return sticker{}, false, fmt.Errorf("media store: pack sticker: %w", err)
	}
	st.Kind = contract.Kind(kind)
	query := `DELETE FROM sticker_pack_members WHERE pack = ? AND sticker = ?`
	if in {
		query = `INSERT INTO sticker_pack_members (pack, sticker) VALUES (?, ?) ON CONFLICT DO NOTHING`
	}
	res, err := tx.ExecContext(ctx, query, pack, id)
	if err != nil {
		return sticker{}, false, fmt.Errorf("media store: pack sticker: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return sticker{}, false, fmt.Errorf("media store: pack sticker: %w", err)
	}
	n, _ := res.RowsAffected()
	return st, n == 1, nil
}
