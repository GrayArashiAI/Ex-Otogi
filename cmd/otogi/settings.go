package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/management"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/internal/kernel"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// moduleSettings keeps which modules run and their settings, and the
// settings outside modules (see sections.go), as revisions in the module
// store, and applies each new revision to the running process.
type moduleSettings struct {
	store    *modulestore.Storage
	kinds    map[otogi.ModuleID]moduleKind
	env      moduleEnv
	system   *system        // what the sections' settings apply to
	kernel   *kernel.Kernel // set once built from enabled
	recorder otogi.Recorder
	log      *slog.Logger
	// started is the revision of each section the process started with,
	// which loadSections sets.
	started map[otogi.ModuleID]int64

	mu sync.Mutex // one change at a time
	// usesProvider holds the modules that asked for a language model
	// provider while being built: see instance.
	usesProvider sync.Map
}

// enabled builds the modules whose current revision enables them, for the
// kernel to start with. A stored revision built when it was saved, so one
// that does not build now fails for the environment, such as an unset
// secret, or for a new version of the module. Such a module is left out, and
// so is each module requiring a service no module left provides; each left
// out is logged and recorded as module.not_started, and starts when its
// settings are saved again.
func (s *moduleSettings) enabled(ctx context.Context) ([]otogi.Module, error) {
	revisions, err := s.store.CurrentSettings(ctx)
	if err != nil {
		return nil, err
	}
	env, err := s.environment(ctx)
	if err != nil {
		return nil, err
	}
	var modules []otogi.Module
	revision := map[otogi.ModuleID]int64{}
	notStarted := func(module otogi.ModuleID, reason string) {
		s.log.Error("module not started", "module", module, "revision", revision[module], "reason", reason)
		s.recorder.Record(ctx, otogi.Record{
			At: time.Now(), Kind: "module.not_started", Component: "module:" + string(module), Outcome: "error",
			Payload: map[string]any{"revision": revision[module], "reason": reason},
		})
	}
	for _, r := range revisions {
		if !r.Enabled {
			continue
		}
		if _, ok := s.kinds[r.Module]; !ok {
			s.log.Warn("enabled module is not in this build; skipped", "module", r.Module, "revision", r.Revision)
			continue
		}
		revision[r.Module] = r.Revision
		m, err := s.instance(r.Module, r.Settings, env)
		if err != nil {
			notStarted(r.Module, err.Error())
			continue
		}
		modules = append(modules, m)
	}
	unmet := unmetRequirements(descriptors(modules))
	for _, id := range slices.Sorted(maps.Keys(unmet)) {
		notStarted(id, unmet[id])
	}
	return slices.DeleteFunc(modules, func(m otogi.Module) bool { _, ok := unmet[m.Descriptor().ID]; return ok }), nil
}

// unmetRequirements returns the modules of described that require a service
// none of the others left provides, each with the reason, until the rest
// have what they require.
func unmetRequirements(described []otogi.ModuleDescriptor) map[otogi.ModuleID]string {
	unmet := map[otogi.ModuleID]string{}
	for changed := true; changed; {
		changed = false
		provided := map[string]bool{}
		for _, d := range described {
			if _, out := unmet[d.ID]; !out {
				for _, name := range d.Provides {
					provided[name] = true
				}
			}
		}
		for _, d := range described {
			if _, out := unmet[d.ID]; !out {
				if reason := missingService(d, provided); reason != "" {
					unmet[d.ID], changed = reason, true
				}
			}
		}
	}
	return unmet
}

// missingService says which service a module requires that is not provided,
// or returns "" when all are.
func missingService(d otogi.ModuleDescriptor, provided map[string]bool) string {
	for _, name := range d.Requires {
		if !provided[name] {
			return fmt.Sprintf("requires service %q, which no running module provides", name)
		}
	}
	return ""
}

func descriptors(modules []otogi.Module) []otogi.ModuleDescriptor {
	out := make([]otogi.ModuleDescriptor, len(modules))
	for i, m := range modules {
		out[i] = m.Descriptor()
	}
	return out
}

// instance builds a module from settings (see moduleKind.instance). Each
// time the module asks for a language model provider while it is built, that
// is noted under its name: those are the modules a change of the providers
// rebuilds. A note is never taken back, so a module that no longer uses one
// is rebuilt once more than it needs.
func (s *moduleSettings) instance(module otogi.ModuleID, settings []byte, env moduleEnv) (otogi.Module, error) {
	lookup := env.llm
	env.llm = func(name string) (llm.Provider, error) {
		s.usesProvider.Store(module, true)
		return lookup(name)
	}
	return s.kinds[module].instance(settings, env)
}

// builtWithProvider reports a module noted as built with a provider.
func (s *moduleSettings) builtWithProvider(module otogi.ModuleID) bool {
	_, ok := s.usesProvider.Load(module)
	return ok
}

// schema finds the settings schema of a module or a section.
func (s *moduleSettings) schema(id otogi.ModuleID) (settingsSchema, bool) {
	if sec, ok := sections[id]; ok {
		return sec.settingsSchema, true
	}
	k, ok := s.kinds[id]
	return k.settingsSchema, ok
}

// current returns the current revision of a module or a section; one never
// saved is revision 0, disabled, with empty settings.
func (s *moduleSettings) current(ctx context.Context, module otogi.ModuleID) (modulestore.SettingsRevision, error) {
	revisions, err := s.store.SettingsRevisions(ctx, module, math.MaxInt64, 1)
	if err != nil || len(revisions) == 0 {
		return modulestore.SettingsRevision{Module: module, Settings: json.RawMessage("{}")}, err
	}
	return revisions[0], nil
}

// save applies a new revision of a module's settings, or a section's, and
// stores it. base is the revision it changes; nil enabled or settings keep
// the current ones. Nothing is stored unless applying succeeds.
func (s *moduleSettings) save(ctx context.Context, id otogi.ModuleID, base int64, on *bool, settings json.RawMessage, by string) (modulestore.SettingsRevision, error) {
	sec, isSection := sections[id]
	if _, ok := s.kinds[id]; !ok && !isSection {
		return modulestore.SettingsRevision{}, fmt.Errorf("module %q: %w", id, management.ErrNotFound)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, err := s.current(ctx, id)
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
	env, err := s.environment(ctx)
	if err != nil {
		return modulestore.SettingsRevision{}, err
	}
	var restarted []otogi.ModuleID
	if isSection && sec.change != nil {
		if err := sec.change(cur.Settings, compact.Bytes()); err != nil {
			return modulestore.SettingsRevision{}, err
		}
	}
	if isSection {
		restarted, err = s.applySection(ctx, id, sec, compact.Bytes(), env)
	} else {
		restarted, err = s.applyModule(ctx, id, enabled, compact.Bytes(), env)
	}
	if err != nil {
		return modulestore.SettingsRevision{}, err
	}
	r := modulestore.SettingsRevision{Module: id, Revision: base + 1, Enabled: enabled, Settings: compact.Bytes(), By: by, At: time.Now()}
	if err := s.store.AddSettings(ctx, r); err != nil {
		return modulestore.SettingsRevision{}, err
	}
	s.reconfigured(ctx, r, restarted)
	return r, nil
}

// applyModule builds the module from the settings, which validates them,
// whether it runs or not. An enabled module replaces the running instance,
// with the modules holding its services rebuilt from their own current
// revisions; a disabled one is stopped. It returns the other modules
// restarted.
func (s *moduleSettings) applyModule(ctx context.Context, module otogi.ModuleID, enabled bool, settings []byte, env moduleEnv) ([]otogi.ModuleID, error) {
	running := slices.ContainsFunc(s.kernel.Descriptors(), func(d otogi.ModuleDescriptor) bool { return d.ID == module })
	m, err := s.instance(module, settings, env)
	if err != nil {
		return nil, err
	}
	var set []otogi.Module
	var remove, dependents []otogi.ModuleID
	switch {
	case enabled:
		dependents = s.kernel.Dependents(module)
		rebuilt, err := s.rebuilt(ctx, env, dependents, string(module))
		if err != nil {
			return nil, err
		}
		set = append([]otogi.Module{m}, rebuilt...)
		waiting, err := s.waiting(ctx, env, set)
		if err != nil {
			return nil, err
		}
		for _, w := range waiting {
			set, dependents = append(set, w), append(dependents, w.Descriptor().ID)
		}
	case running:
		remove = append(remove, module)
	}
	if len(set) > 0 || len(remove) > 0 {
		if err := s.kernel.Reconfigure(ctx, set, remove); err != nil {
			return nil, err
		}
	}
	return dependents, nil
}

// waiting builds the modules that are on and not running, other than those
// in set, and returns those that have the services they require once set
// joins the running modules: modules left out at startup (see enabled)
// start with the module whose save gives them what they lacked. One that
// does not build stays out.
func (s *moduleSettings) waiting(ctx context.Context, env moduleEnv, set []otogi.Module) ([]otogi.Module, error) {
	revisions, err := s.store.CurrentSettings(ctx)
	if err != nil {
		return nil, err
	}
	described := descriptors(set)
	taken := map[otogi.ModuleID]bool{}
	for _, d := range described {
		taken[d.ID] = true
	}
	for _, d := range s.kernel.Descriptors() {
		if !taken[d.ID] {
			described, taken[d.ID] = append(described, d), true
		}
	}
	var candidates []otogi.Module
	for _, r := range revisions {
		if _, ok := s.kinds[r.Module]; !ok || !r.Enabled || taken[r.Module] {
			continue
		}
		if m, err := s.instance(r.Module, r.Settings, env); err == nil {
			candidates = append(candidates, m)
		}
	}
	unmet := unmetRequirements(append(described, descriptors(candidates)...))
	return slices.DeleteFunc(candidates, func(m otogi.Module) bool { _, ok := unmet[m.Descriptor().ID]; return ok }), nil
}

// reconfigured logs and records a saved and applied revision.
func (s *moduleSettings) reconfigured(ctx context.Context, r modulestore.SettingsRevision, restarted []otogi.ModuleID) {
	if _, ok := sections[r.Module]; ok {
		s.log.Info("system reconfigured", "section", r.Module, "revision", r.Revision, "by", r.By, "restarted", restarted)
		s.recorder.Record(ctx, otogi.Record{
			At: r.At, Kind: "system.reconfigured", Component: string(r.Module), Outcome: "ok",
			Payload: map[string]any{"revision": r.Revision, "by": r.By, "settings": r.Settings, "restarted": restarted},
		})
		return
	}
	s.log.Info("module reconfigured", "module", r.Module, "revision", r.Revision, "enabled", r.Enabled, "by", r.By, "restarted", restarted)
	s.recorder.Record(ctx, otogi.Record{
		At: r.At, Kind: "module.reconfigured", Component: "module:" + string(r.Module), Outcome: "ok",
		Payload: map[string]any{"revision": r.Revision, "enabled": r.Enabled, "by": r.By, "settings": r.Settings, "restarted": restarted},
	})
}

// restart rebuilds the running modules use picks, and the modules holding
// their services, from their current revisions, and swaps them in at once.
// It returns the modules restarted; changed says why, for a failure.
func (s *moduleSettings) restart(ctx context.Context, env moduleEnv, use func(otogi.ModuleID) bool, changed string) ([]otogi.ModuleID, error) {
	restart := []otogi.ModuleID{}
	for _, d := range s.kernel.Descriptors() {
		if use(d.ID) {
			restart = append(restart, d.ID)
		}
	}
	restart = append(restart, s.kernel.Dependents(restart...)...)
	set, err := s.rebuilt(ctx, env, restart, changed)
	if err != nil {
		return nil, err
	}
	// What changed may be what a module left out at startup lacked, such
	// as a secret it refers to: it starts now.
	waiting, err := s.waiting(ctx, env, set)
	if err != nil {
		return nil, err
	}
	for _, w := range waiting {
		set, restart = append(set, w), append(restart, w.Descriptor().ID)
	}
	if len(set) > 0 {
		if err := s.kernel.Reconfigure(ctx, set, nil); err != nil {
			return nil, err
		}
	}
	return restart, nil
}

// rebuilt builds the given modules from their current revisions, to replace
// the running instances of those that use what changed.
func (s *moduleSettings) rebuilt(ctx context.Context, env moduleEnv, modules []otogi.ModuleID, changed string) ([]otogi.Module, error) {
	var out []otogi.Module
	for _, id := range modules {
		r, err := s.current(ctx, id)
		if err != nil {
			return nil, err
		}
		m, err := s.instance(id, r.Settings, env)
		if err != nil {
			return nil, fmt.Errorf("rebuild %s, which uses %s: %w", id, changed, err)
		}
		out = append(out, m)
	}
	return out, nil
}
