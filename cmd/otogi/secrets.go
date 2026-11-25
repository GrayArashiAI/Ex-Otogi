package main

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/management"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Settings refer to a secret as ${NAME}. Its value is the one set in the
// panel, kept in the module store, or else the environment variable of that
// name. Modules read it when they are built, and so do the language model
// providers, so a changed secret reaches the running ones by rebuilding
// them.

// environment is what modules are built with now: s.env, and the secrets as
// the module store holds them at this moment.
func (s *moduleSettings) environment(ctx context.Context) (moduleEnv, error) {
	stored, err := s.storedSecrets(ctx)
	return s.environmentWith(stored), err
}

func (s *moduleSettings) environmentWith(stored map[string]string) moduleEnv {
	env := s.env
	env.secret = func(name string) (string, bool) {
		if v, ok := stored[name]; ok {
			return v, true
		}
		return os.LookupEnv(name)
	}
	return env
}

func (s *moduleSettings) storedSecrets(ctx context.Context) (map[string]string, error) {
	secrets, err := s.store.Secrets(ctx)
	values := make(map[string]string, len(secrets))
	for _, secret := range secrets {
		values[secret.Name] = secret.Value
	}
	return values, err
}

// secretUsers returns, for each secret name, the modules and sections whose
// current settings refer to it, sorted by ID, whether they run or not.
func (s *moduleSettings) secretUsers(ctx context.Context) (map[string][]otogi.ModuleID, error) {
	revisions, err := s.store.CurrentSettings(ctx)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(revisions, func(a, b modulestore.SettingsRevision) int { return cmp.Compare(a.Module, b.Module) })
	users := map[string][]otogi.ModuleID{}
	for _, r := range revisions {
		schema, ok := s.schema(r.Module)
		if !ok {
			continue // not in this build
		}
		names, err := schema.secrets(r.Settings)
		if err != nil {
			return nil, fmt.Errorf("%s, settings revision %d: %w", r.Module, r.Revision, err)
		}
		for _, name := range names {
			users[name] = append(users[name], r.Module)
		}
	}
	return users, nil
}

// secrets lists the secrets set in the panel and every other name settings
// refer to, by name, without their values.
func (s *moduleSettings) secrets(ctx context.Context) ([]management.Secret, error) {
	stored, err := s.store.Secrets(ctx)
	if err != nil {
		return nil, err
	}
	users, err := s.secretUsers(ctx)
	if err != nil {
		return nil, err
	}
	out := []management.Secret{}
	for _, secret := range stored {
		out = append(out, management.Secret{Name: secret.Name, Source: "panel", By: secret.By, At: secret.At, References: ids(users[secret.Name])})
		delete(users, secret.Name)
	}
	for name, modules := range users {
		source := "missing"
		if _, ok := os.LookupEnv(name); ok {
			source = "environment"
		}
		out = append(out, management.Secret{Name: name, Source: source, References: ids(modules)})
	}
	slices.SortFunc(out, func(a, b management.Secret) int { return cmp.Compare(a.Name, b.Name) })
	return out, nil
}

// setSecret sets a secret in the panel, or replaces it.
func (s *moduleSettings) setSecret(ctx context.Context, name, value, by string) ([]otogi.ModuleID, error) {
	return s.changeSecret(ctx, name, &value, by)
}

// deleteSecret deletes a secret set in the panel, which leaves the
// environment variable of its name. It is refused while settings refer to
// the secret and there is no such variable.
func (s *moduleSettings) deleteSecret(ctx context.Context, name, by string) ([]otogi.ModuleID, error) {
	return s.changeSecret(ctx, name, nil, by)
}

// changeSecret applies a secret's new value, or with nil its deletion, and
// stores it. The sections whose settings refer to the secret are applied
// again; the running modules whose settings refer to it, and those a section
// rebuilds, are rebuilt from their current revisions, with the modules
// holding their services, and replace the running instances. Nothing is
// stored, and the sections are put back, unless that succeeds. It returns
// the modules restarted.
func (s *moduleSettings) changeSecret(ctx context.Context, name string, value *string, by string) ([]otogi.ModuleID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, err := s.storedSecrets(ctx)
	if err != nil {
		return nil, err
	}
	users, err := s.secretUsers(ctx)
	if err != nil {
		return nil, err
	}
	_, had := stored[name]
	action := "set"
	switch {
	case value != nil && had:
		action = "replaced"
	case value != nil:
	case !had:
		return nil, fmt.Errorf("secret %s is not set in the panel: %w", name, management.ErrNotFound)
	default:
		action = "deleted"
		if _, ok := os.LookupEnv(name); !ok && len(users[name]) > 0 {
			return nil, fmt.Errorf("secret %s: the settings of %s refer to it, and there is no environment variable of that name: %w", name, strings.Join(ids(users[name]), ", "), management.ErrInUse)
		}
	}
	if value != nil {
		stored[name] = *value
	} else {
		delete(stored, name)
	}
	env := s.environmentWith(stored)
	var undo []func()
	back := func() {
		for _, u := range undo {
			u()
		}
	}
	use := func(module otogi.ModuleID) bool { return slices.Contains(users[name], module) }
	for _, id := range users[name] {
		sec, ok := sections[id]
		if !ok {
			continue
		}
		cur, err := s.current(ctx, id)
		if err != nil {
			back()
			return nil, err
		}
		u, err := sec.apply(s.system, cur.Settings, env.secret)
		if err != nil {
			back()
			return nil, fmt.Errorf("%s, which uses secret %s: %w", id, name, err)
		}
		undo = append(undo, u)
		if rebuilds, others := sec.rebuilds, use; rebuilds != nil {
			use = func(module otogi.ModuleID) bool { return others(module) || rebuilds(s, module) }
		}
	}
	restart, err := s.restart(ctx, env, use, "secret "+name)
	if err != nil {
		back()
		return nil, err
	}
	now := time.Now()
	if value != nil {
		err = s.store.SetSecret(ctx, modulestore.Secret{Name: name, Value: *value, By: by, At: now})
	} else {
		err = s.store.DeleteSecret(ctx, name)
	}
	if err != nil {
		return nil, err
	}
	restart = append(restart, s.startAfter(ctx, "secret "+name)...)
	s.secretChanged(ctx, now, name, action, by, restart)
	return restart, nil
}

// secretChanged logs and records a stored change of a secret, without its
// value.
func (s *moduleSettings) secretChanged(ctx context.Context, at time.Time, name, action, by string, restarted []otogi.ModuleID) {
	s.log.Info("secret changed", "name", name, "action", action, "by", by, "restarted", restarted)
	s.recorder.Record(ctx, otogi.Record{
		At: at, Kind: "secret.changed", Component: "secret:" + name, Outcome: "ok",
		Payload: map[string]any{"name": name, "action": action, "by": by, "restarted": restarted},
	})
}
