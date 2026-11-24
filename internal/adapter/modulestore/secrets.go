package modulestore

import (
	"context"
	"fmt"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore/queries"
)

// Secret is a secret set in the control panel, which settings refer to by
// name, with who set it and when.
type Secret struct {
	Name  string
	Value string
	By    string
	At    time.Time
}

// Secrets returns every stored secret, by name.
func (s *Storage) Secrets(ctx context.Context) ([]Secret, error) {
	rows, err := queries.New(s.db).Secrets(ctx)
	if err != nil {
		return nil, fmt.Errorf("module store: secrets: %w", err)
	}
	out := make([]Secret, len(rows))
	for i, r := range rows {
		out[i] = Secret{Name: r.Name, Value: r.Value, By: r.By, At: time.UnixMilli(r.At)}
	}
	return out, nil
}

// SetSecret stores a secret, replacing the one of its name.
func (s *Storage) SetSecret(ctx context.Context, secret Secret) error {
	err := queries.New(s.db).SetSecret(ctx, queries.SetSecretParams{Name: secret.Name, Value: secret.Value, By: secret.By, At: secret.At.UnixMilli()})
	if err != nil {
		return fmt.Errorf("module store: set secret %s: %w", secret.Name, err)
	}
	return nil
}

// DeleteSecret removes the secret of a name.
func (s *Storage) DeleteSecret(ctx context.Context, name string) error {
	if err := queries.New(s.db).DeleteSecret(ctx, name); err != nil {
		return fmt.Errorf("module store: delete secret %s: %w", name, err)
	}
	return nil
}
