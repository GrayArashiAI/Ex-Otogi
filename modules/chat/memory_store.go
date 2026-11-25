package chat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// memoryEntry is one thing an agent remembers of a chat.
type memoryEntry struct {
	ID      int64
	Text    string
	People  map[string]string // the actor IDs it is about, to their names when noted
	Created time.Time
	Updated time.Time
}

// memoryMark is how far an agent has taken a chat into its memory: the last
// message taken, and how many times in a row the batch after it was
// answered with nothing usable.
type memoryMark struct {
	At       time.Time
	SourceID string
	Unusable int
}

// markOf is the mark just past l.
func markOf(l line) memoryMark { return memoryMark{At: l.At, SourceID: l.SourceID} }

// before reports whether l comes before the mark, in transcript order.
func (k memoryMark) before(l line) bool {
	return k.At.Before(l.At) || k.At.Equal(l.At) && k.SourceID < l.SourceID
}

// book returns an agent's memory of a chat, oldest entry first.
func (s *store) book(ctx context.Context, profile string, to otogi.Destination) ([]memoryEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, text, people, created_at, updated_at FROM memories
		WHERE profile = ? AND platform = ? AND account = ? AND conversation = ? ORDER BY id`,
		profile, to.Platform, to.Account, to.Conversation)
	if err != nil {
		return nil, fmt.Errorf("chat store: read memory: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []memoryEntry
	for rows.Next() {
		var e memoryEntry
		var people string
		var created, updated int64
		if err := rows.Scan(&e.ID, &e.Text, &people, &created, &updated); err != nil {
			return nil, fmt.Errorf("chat store: read memory: %w", err)
		}
		if err := json.Unmarshal([]byte(people), &e.People); err != nil {
			return nil, fmt.Errorf("chat store: decode memory people: %w", err)
		}
		e.Created, e.Updated = time.UnixMilli(created), time.UnixMilli(updated)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("chat store: read memory: %w", err)
	}
	return out, nil
}

// mark returns how far an agent has taken a chat into its memory; the zero
// mark when it has taken nothing.
func (s *store) mark(ctx context.Context, profile string, to otogi.Destination) (memoryMark, error) {
	var k memoryMark
	var at int64
	err := s.db.QueryRowContext(ctx, `SELECT at, source_id, unusable FROM memory_marks
		WHERE profile = ? AND platform = ? AND account = ? AND conversation = ?`,
		profile, to.Platform, to.Account, to.Conversation).Scan(&at, &k.SourceID, &k.Unusable)
	switch {
	case err == nil:
		k.At = time.UnixMilli(at)
		return k, nil
	case errors.Is(err, sql.ErrNoRows):
		return memoryMark{}, nil
	}
	return memoryMark{}, fmt.Errorf("chat store: read memory mark: %w", err)
}

// chats returns the chats the transcript keeps.
func (s *store) chats(ctx context.Context) ([]otogi.Destination, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT DISTINCT platform, account, conversation FROM transcript ORDER BY platform, account, conversation")
	if err != nil {
		return nil, fmt.Errorf("chat store: read chats: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []otogi.Destination
	for rows.Next() {
		var d otogi.Destination
		if err := rows.Scan(&d.Platform, &d.Account, &d.Conversation); err != nil {
			return nil, fmt.Errorf("chat store: read chats: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// linesAfter returns up to n messages of the chat after the mark, oldest
// first, deleted ones left out.
func (s *store) linesAfter(ctx context.Context, to otogi.Destination, k memoryMark, n int) ([]line, error) {
	at := k.At.UnixMilli()
	if k.At.IsZero() {
		at = -1
	}
	return s.lines(ctx, lineColumns+`
		WHERE t.platform = ? AND t.account = ? AND t.conversation = ? AND t.deleted = 0 AND (t.at > ? OR t.at = ? AND t.source_id > ?)
		ORDER BY t.at, t.source_id LIMIT ?`,
		to.Platform, to.Account, to.Conversation, at, at, k.SourceID, n)
}

// linesBefore returns up to n messages of the chat before l, oldest first,
// deleted ones left out.
func (s *store) linesBefore(ctx context.Context, to otogi.Destination, l line, n int) ([]line, error) {
	lines, err := s.lines(ctx, lineColumns+`
		WHERE t.platform = ? AND t.account = ? AND t.conversation = ? AND t.deleted = 0 AND (t.at < ? OR t.at = ? AND t.source_id < ?)
		ORDER BY t.at DESC, t.source_id DESC LIMIT ?`,
		to.Platform, to.Account, to.Conversation, l.At.UnixMilli(), l.At.UnixMilli(), l.SourceID, n)
	slices.Reverse(lines)
	return lines, err
}

// newest returns the last n messages of the chat, oldest first, deleted
// ones left out.
func (s *store) newest(ctx context.Context, to otogi.Destination, n int) ([]line, error) {
	lines, err := s.lines(ctx, lineColumns+`
		WHERE t.platform = ? AND t.account = ? AND t.conversation = ? AND t.deleted = 0
		ORDER BY t.at DESC, t.source_id DESC LIMIT ?`,
		to.Platform, to.Account, to.Conversation, n)
	slices.Reverse(lines)
	return lines, err
}

// memoryChange is one change to a book: an entry to insert (ID 0), to
// replace, or, with only ID set, to delete.
type memoryChange struct {
	entry  memoryEntry
	delete bool
}

// keepMemory applies changes to an agent's memory of a chat and, when next
// is set, moves its mark there, in one transaction: a batch is taken
// whole or not at all.
func (s *store) keepMemory(ctx context.Context, profile string, to otogi.Destination, changes []memoryChange, next *memoryMark) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("chat store: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, c := range changes {
		e := c.entry
		people, err := json.Marshal(e.People)
		if err != nil {
			return fmt.Errorf("chat store: encode memory people: %w", err)
		}
		switch {
		case c.delete:
			_, err = tx.ExecContext(ctx, "DELETE FROM memories WHERE id = ?", e.ID)
		case e.ID == 0:
			_, err = tx.ExecContext(ctx, `INSERT INTO memories (profile, platform, account, conversation, text, people, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				profile, to.Platform, to.Account, to.Conversation, e.Text, string(people), e.Created.UnixMilli(), e.Updated.UnixMilli())
		default:
			_, err = tx.ExecContext(ctx, "UPDATE memories SET text = ?, people = ?, created_at = ?, updated_at = ? WHERE id = ?",
				e.Text, string(people), e.Created.UnixMilli(), e.Updated.UnixMilli(), e.ID)
		}
		if err != nil {
			return fmt.Errorf("chat store: keep memory: %w", err)
		}
	}
	if next != nil {
		if err := setMark(ctx, tx, profile, to, *next); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("chat store: %w", err)
	}
	return nil
}

// setUnusable keeps how many times in a row the batch after the mark was
// answered with nothing usable.
func (s *store) setUnusable(ctx context.Context, profile string, to otogi.Destination, k memoryMark) error {
	return setMark(ctx, s.db, profile, to, k)
}

// execer runs a statement, in a transaction or not.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func setMark(ctx context.Context, db execer, profile string, to otogi.Destination, k memoryMark) error {
	_, err := db.ExecContext(ctx, `INSERT INTO memory_marks (profile, platform, account, conversation, at, source_id, unusable)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (profile, platform, account, conversation) DO UPDATE SET at = excluded.at, source_id = excluded.source_id, unusable = excluded.unusable`,
		profile, to.Platform, to.Account, to.Conversation, k.At.UnixMilli(), k.SourceID, k.Unusable)
	if err != nil {
		return fmt.Errorf("chat store: keep memory mark: %w", err)
	}
	return nil
}
