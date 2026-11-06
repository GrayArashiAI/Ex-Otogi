package modulestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore/queries"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// SettingsRevision is one saved change to a module's settings.
type SettingsRevision struct {
	Module   otogi.ModuleID
	Revision int64 // 1, 2, ... per module
	Enabled  bool
	Settings json.RawMessage
	By       string
	At       time.Time
}

func revision(r queries.ModuleSetting) SettingsRevision {
	return SettingsRevision{Module: otogi.ModuleID(r.Module), Revision: r.Revision, Enabled: r.Enabled, Settings: json.RawMessage(r.Settings), By: r.By, At: time.UnixMilli(r.At)}
}

// CurrentSettings returns the latest revision of every module that has one.
func (s *Storage) CurrentSettings(ctx context.Context) ([]SettingsRevision, error) {
	rows, err := queries.New(s.db).CurrentSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("module store: current settings: %w", err)
	}
	out := make([]SettingsRevision, len(rows))
	for i, r := range rows {
		out[i] = revision(r)
	}
	return out, nil
}

// SettingsRevision returns one revision of a module's settings, or false
// when it has no such revision.
func (s *Storage) SettingsRevision(ctx context.Context, module otogi.ModuleID, rev int64) (SettingsRevision, bool, error) {
	r, err := queries.New(s.db).SettingsRevision(ctx, queries.SettingsRevisionParams{Module: string(module), Revision: rev})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return SettingsRevision{}, false, nil
	case err != nil:
		return SettingsRevision{}, false, fmt.Errorf("module store: settings revision %s/%d: %w", module, rev, err)
	}
	return revision(r), true, nil
}

// SettingsRevisions returns up to limit revisions of a module's settings
// older than before, newest first.
func (s *Storage) SettingsRevisions(ctx context.Context, module otogi.ModuleID, before int64, limit int) ([]SettingsRevision, error) {
	rows, err := queries.New(s.db).SettingsRevisions(ctx, queries.SettingsRevisionsParams{Module: string(module), Before: before, Limit: int64(limit)})
	if err != nil {
		return nil, fmt.Errorf("module store: settings revisions of %s: %w", module, err)
	}
	out := make([]SettingsRevision, len(rows))
	for i, r := range rows {
		out[i] = revision(r)
	}
	return out, nil
}

// AddSettings saves a revision; one with the same number fails.
func (s *Storage) AddSettings(ctx context.Context, r SettingsRevision) error {
	err := queries.New(s.db).AddSettings(ctx, queries.AddSettingsParams{
		Module: string(r.Module), Revision: r.Revision, Enabled: r.Enabled, Settings: string(r.Settings), By: r.By, At: r.At.UnixMilli(),
	})
	if err != nil {
		return fmt.Errorf("module store: add settings %s/%d: %w", r.Module, r.Revision, err)
	}
	return nil
}
