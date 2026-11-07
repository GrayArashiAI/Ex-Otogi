package main

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/management"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// moduleAPI is the module runtime as the management API sees it.
type moduleAPI struct{ settings *moduleSettings }

var _ management.Modules = moduleAPI{}

func (a moduleAPI) Modules(ctx context.Context) ([]management.Module, error) {
	s := a.settings
	revisions, err := s.store.CurrentSettings(ctx)
	if err != nil {
		return nil, err
	}
	current := map[otogi.ModuleID]modulestore.SettingsRevision{}
	for _, r := range revisions {
		current[r.Module] = r
	}
	running := map[otogi.ModuleID]otogi.ModuleDescriptor{}
	for _, d := range s.kernel.Descriptors() {
		running[d.ID] = d
	}
	chats := map[[2]string]int{}
	for _, c := range s.kernel.Allowlists() {
		chats[[2]string{string(c.Module), c.List}]++
	}
	counts := s.kernel.Stats().Modules
	var out []management.Module
	for _, id := range slices.Sorted(maps.Keys(s.kinds)) {
		r, saved := current[id]
		m := management.Module{ID: string(id), Enabled: r.Enabled, Revision: r.Revision, Allowlists: []management.Allowlist{}, Commands: []management.Command{}}
		d, ok := running[id]
		m.Running = ok
		if !ok { // describe it as its current settings would build it
			settings := json.RawMessage("{}")
			if saved {
				settings = r.Settings
			}
			if instance, err := s.kinds[id].instance(settings, s.env); err != nil {
				if saved {
					m.Problem = err.Error()
				}
			} else {
				d = instance.Descriptor()
			}
		}
		for _, l := range d.Allowlists {
			m.Allowlists = append(m.Allowlists, management.Allowlist{Name: l.Name, Title: l.Title, Description: l.Description, About: string(l.About), Chats: chats[[2]string{string(id), l.Name}]})
		}
		for _, c := range d.Commands {
			m.Commands = append(m.Commands, management.Command{Name: c.Name, Usage: c.Usage, Description: c.Description})
		}
		c := counts[id]
		m.Counts = management.ModuleCounts{Completed: c.Completed, Slow: c.Slow, Failed: c.Failed, Panicked: c.Panicked}
		out = append(out, m)
	}
	return out, nil
}

func (a moduleAPI) kind(module string) (moduleKind, error) {
	k, ok := a.settings.kinds[otogi.ModuleID(module)]
	if !ok {
		return moduleKind{}, fmt.Errorf("module %q: %w", module, management.ErrNotFound)
	}
	return k, nil
}

func (a moduleAPI) Settings(ctx context.Context, module string) (management.Revision, map[string]any, error) {
	k, err := a.kind(module)
	if err != nil {
		return management.Revision{}, nil, err
	}
	r, err := a.settings.current(ctx, otogi.ModuleID(module))
	if err != nil {
		return management.Revision{}, nil, err
	}
	schema, err := k.schemaDocument()
	return revisionOut(r), schema, err
}

func (a moduleAPI) SaveSettings(ctx context.Context, module string, base int64, enabled *bool, settings json.RawMessage, by string) (management.Revision, error) {
	r, err := a.settings.save(ctx, otogi.ModuleID(module), base, enabled, settings, by)
	return revisionOut(r), err
}

func (a moduleAPI) Revisions(ctx context.Context, module string, before int64, limit int) ([]management.Revision, error) {
	if _, err := a.kind(module); err != nil {
		return nil, err
	}
	revisions, err := a.settings.store.SettingsRevisions(ctx, otogi.ModuleID(module), before, limit)
	out := make([]management.Revision, len(revisions))
	for i, r := range revisions {
		out[i] = revisionOut(r)
	}
	return out, err
}

func (a moduleAPI) Revision(ctx context.Context, module string, revision int64) (management.Revision, error) {
	r, found, err := a.settings.store.SettingsRevision(ctx, otogi.ModuleID(module), revision)
	if err == nil && !found {
		err = fmt.Errorf("module %q has no settings revision %d: %w", module, revision, management.ErrNotFound)
	}
	return revisionOut(r), err
}

func (a moduleAPI) Allowlists() []management.AllowedChat {
	lists := a.settings.kernel.Allowlists()
	out := make([]management.AllowedChat, len(lists))
	for i, c := range lists {
		out[i] = management.AllowedChat{Module: string(c.Module), List: c.List, Chat: c.Chat, At: c.At, By: c.By}
	}
	return out
}

func (a moduleAPI) SetAllowed(ctx context.Context, module, list, chat string, allowed bool, by string) error {
	return a.settings.kernel.SetAllowed(ctx, otogi.ModuleID(module), list, chat, allowed, by)
}

func revisionOut(r modulestore.SettingsRevision) management.Revision {
	return management.Revision{Module: string(r.Module), Revision: r.Revision, Enabled: r.Enabled, Settings: r.Settings, By: r.By, At: r.At}
}
