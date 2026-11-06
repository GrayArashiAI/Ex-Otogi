package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/management"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/internal/kernel"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// moduleSettings keeps which modules run and their settings, as revisions
// in the module store, and applies each new revision to the running kernel.
type moduleSettings struct {
	store    *modulestore.Storage
	kinds    map[otogi.ModuleID]moduleKind
	env      moduleEnv
	kernel   *kernel.Kernel // set once built from enabled
	recorder otogi.Recorder
	log      *slog.Logger

	mu sync.Mutex // one change at a time
}

// enabled builds the modules whose current revision enables them, for the
// kernel to start with. A stored revision was built and started when it was
// saved, so a failure here is the environment's, such as an unset secret,
// and stops the process.
func (s *moduleSettings) enabled(ctx context.Context) ([]otogi.Module, error) {
	revisions, err := s.store.CurrentSettings(ctx)
	if err != nil {
		return nil, err
	}
	var modules []otogi.Module
	for _, r := range revisions {
		if !r.Enabled {
			continue
		}
		kind, ok := s.kinds[r.Module]
		if !ok {
			s.log.Warn("enabled module is not in this build; skipped", "module", r.Module, "revision", r.Revision)
			continue
		}
		m, err := kind.instance(r.Settings, s.env)
		if err != nil {
			return nil, fmt.Errorf("module %s, settings revision %d: %w", r.Module, r.Revision, err)
		}
		modules = append(modules, m)
	}
	return modules, nil
}

// current returns a module's current revision; one never saved is revision
// 0, disabled, with empty settings.
func (s *moduleSettings) current(ctx context.Context, module otogi.ModuleID) (modulestore.SettingsRevision, error) {
	revisions, err := s.store.SettingsRevisions(ctx, module, math.MaxInt64, 1)
	if err != nil || len(revisions) == 0 {
		return modulestore.SettingsRevision{Module: module, Settings: json.RawMessage("{}")}, err
	}
	return revisions[0], nil
}

// save applies a new revision of a module's settings and stores it. base is
// the revision it changes; nil enabled or settings keep the current ones.
// The module is built from the settings, which validates them, whether it
// runs or not. An enabled module replaces the running instance, with the
// modules holding its services rebuilt from their own current revisions; a
// disabled one is stopped. Nothing is stored unless that succeeds.
func (s *moduleSettings) save(ctx context.Context, module otogi.ModuleID, base int64, on *bool, settings json.RawMessage, by string) (modulestore.SettingsRevision, error) {
	kind, ok := s.kinds[module]
	if !ok {
		return modulestore.SettingsRevision{}, fmt.Errorf("module %q: %w", module, management.ErrNotFound)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, err := s.current(ctx, module)
	if err != nil {
		return modulestore.SettingsRevision{}, err
	}
	if cur.Revision != base {
		return modulestore.SettingsRevision{}, &management.StaleRevisionError{Current: cur.Revision}
	}
	enabled := cur.Enabled
	if on != nil {
		enabled = *on
	}
	if settings == nil {
		settings = cur.Settings
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, settings); err != nil {
		return modulestore.SettingsRevision{}, fmt.Errorf("settings are not JSON: %w: %w", otogi.ErrInvalid, err)
	}
	running := slices.ContainsFunc(s.kernel.Descriptors(), func(d otogi.ModuleDescriptor) bool { return d.ID == module })
	m, err := kind.instance(compact.Bytes(), s.env)
	if err != nil {
		return modulestore.SettingsRevision{}, err
	}
	var set []otogi.Module
	var remove, dependents []otogi.ModuleID
	switch {
	case enabled:
		set = append(set, m)
		dependents = s.kernel.Dependents(module)
		for _, id := range dependents {
			r, err := s.current(ctx, id)
			if err != nil {
				return modulestore.SettingsRevision{}, err
			}
			m, err := s.kinds[id].instance(r.Settings, s.env)
			if err != nil {
				return modulestore.SettingsRevision{}, fmt.Errorf("rebuild %s, which uses %s: %w", id, module, err)
			}
			set = append(set, m)
		}
	case running:
		remove = append(remove, module)
	}
	if len(set) > 0 || len(remove) > 0 {
		if err := s.kernel.Reconfigure(ctx, set, remove); err != nil {
			return modulestore.SettingsRevision{}, err
		}
	}
	r := modulestore.SettingsRevision{Module: module, Revision: base + 1, Enabled: enabled, Settings: compact.Bytes(), By: by, At: time.Now()}
	if err := s.store.AddSettings(ctx, r); err != nil {
		return modulestore.SettingsRevision{}, err
	}
	s.log.Info("module reconfigured", "module", module, "revision", r.Revision, "enabled", enabled, "by", by, "restarted", dependents)
	s.recorder.Record(ctx, otogi.Record{
		At: r.At, Kind: "module.reconfigured", Component: "module:" + string(module), Outcome: "ok",
		Payload: map[string]any{"revision": r.Revision, "enabled": enabled, "by": by, "settings": r.Settings, "restarted": dependents},
	})
	return r, nil
}
