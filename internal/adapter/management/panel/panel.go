// Package panel keeps the control panel's sign-in in
// <data_dir>/panel.sqlite: the operator's passkeys, the one-time links that
// register a first passkey, and the key that seals session cookies.
package panel

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // database/sql driver "sqlite"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/management/panel/queries"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store is the panel's database.
type Store struct {
	db        *sql.DB
	handle    []byte
	cookieKey []byte
}

// Passkey is a registered passkey of the operator.
type Passkey struct {
	Name       string
	Credential webauthn.Credential
	CreatedAt  time.Time
	UsedAt     time.Time // zero until first used to sign in
}

// Open opens or creates the database at path. On first open it generates
// the operator's WebAuthn user handle and the cookie key.
func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("panel store: create directory: %w", err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("panel store: open: %w", err)
	}
	s, err := open(ctx, db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func open(ctx context.Context, db *sql.DB) (*Store, error) {
	sub, err := fs.Sub(migrations, "migrations")
	if err == nil {
		var provider *goose.Provider
		if provider, err = goose.NewProvider(goose.DialectSQLite3, db, sub); err == nil {
			_, err = provider.Up(ctx)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("panel store: migrate: %w", err)
	}
	q := queries.New(db)
	if err := q.CreateOperator(ctx, queries.CreateOperatorParams{UserHandle: random(32), CookieKey: random(64)}); err != nil {
		return nil, fmt.Errorf("panel store: create operator: %w", err)
	}
	op, err := q.Operator(ctx)
	if err != nil {
		return nil, fmt.Errorf("panel store: load operator: %w", err)
	}
	return &Store{db: db, handle: op.UserHandle, cookieKey: op.CookieKey}, nil
}

func random(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b) // never fails
	return b
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// UserHandle is the operator's WebAuthn user handle.
func (s *Store) UserHandle() []byte { return s.handle }

// CookieKeys are the keys that sign and encrypt session cookies.
func (s *Store) CookieKeys() (hash, block []byte) { return s.cookieKey[:32], s.cookieKey[32:] }

// NewEnrollment creates a one-time token that registers a passkey within ttl.
func (s *Store) NewEnrollment(ctx context.Context, ttl time.Duration) (string, error) {
	token := base64.RawURLEncoding.EncodeToString(random(32))
	hash := sha256.Sum256([]byte(token))
	if err := queries.New(s.db).AddEnrollment(ctx, queries.AddEnrollmentParams{TokenHash: hash[:], ExpiresAt: time.Now().Add(ttl).UnixMilli()}); err != nil {
		return "", fmt.Errorf("panel store: add enrollment: %w", err)
	}
	return token, nil
}

// Enrollment reports whether token is a valid enrollment; consume uses it
// up. Expired enrollments are removed on the way.
func (s *Store) Enrollment(ctx context.Context, token string, consume bool) (bool, error) {
	hash := sha256.Sum256([]byte(token))
	q := queries.New(s.db)
	expires, err := q.Enrollment(ctx, hash[:])
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("panel store: enrollment: %w", err)
	}
	valid := time.Now().UnixMilli() < expires
	if consume || !valid {
		if _, err := q.RemoveEnrollment(ctx, queries.RemoveEnrollmentParams{TokenHash: hash[:], Now: time.Now().UnixMilli()}); err != nil {
			return false, fmt.Errorf("panel store: remove enrollment: %w", err)
		}
	}
	return valid, nil
}

// Passkeys returns the operator's passkeys, oldest first.
func (s *Store) Passkeys(ctx context.Context) ([]Passkey, error) {
	rows, err := queries.New(s.db).Passkeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("panel store: passkeys: %w", err)
	}
	out := make([]Passkey, len(rows))
	for i, r := range rows {
		out[i] = Passkey{Name: r.Name, CreatedAt: time.UnixMilli(r.CreatedAt).UTC()}
		if r.UsedAt != 0 {
			out[i].UsedAt = time.UnixMilli(r.UsedAt).UTC()
		}
		if err := json.Unmarshal([]byte(r.Credential), &out[i].Credential); err != nil {
			return nil, fmt.Errorf("panel store: passkey %s: %w", r.Name, err)
		}
	}
	return out, nil
}

// AddPasskey stores a newly registered passkey.
func (s *Store) AddPasskey(ctx context.Context, name string, c webauthn.Credential) error {
	data, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("panel store: encode passkey: %w", err)
	}
	if err := queries.New(s.db).AddPasskey(ctx, queries.AddPasskeyParams{ID: c.ID, Name: name, Credential: string(data), CreatedAt: time.Now().UnixMilli()}); err != nil {
		return fmt.Errorf("panel store: add passkey: %w", err)
	}
	return nil
}

// UsePasskey stores a passkey's state after it signed in, such as its sign
// count.
func (s *Store) UsePasskey(ctx context.Context, c webauthn.Credential) error {
	data, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("panel store: encode passkey: %w", err)
	}
	if err := queries.New(s.db).UsePasskey(ctx, queries.UsePasskeyParams{ID: c.ID, Credential: string(data), UsedAt: time.Now().UnixMilli()}); err != nil {
		return fmt.Errorf("panel store: use passkey: %w", err)
	}
	return nil
}

// RemovePasskey deletes a passkey and reports whether it existed.
func (s *Store) RemovePasskey(ctx context.Context, id []byte) (bool, error) {
	n, err := queries.New(s.db).RemovePasskey(ctx, id)
	if err != nil {
		return false, fmt.Errorf("panel store: remove passkey: %w", err)
	}
	return n > 0, nil
}
