package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/management"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// keyed is a module that holds a secret from when it is built; it refuses
// the key "value-refused". Starting it makes its key the one in use.
type keyed struct {
	key   string
	inUse *atomic.Value
}

type keyedConfig struct {
	Key otogi.Secret `json:"key"`
}

func (*keyed) Descriptor() otogi.ModuleDescriptor { return otogi.ModuleDescriptor{ID: "keyed"} }

func (*keyed) Handle(context.Context, otogi.Event, otogi.Invocation) error { return nil }

func (m *keyed) Start(context.Context, otogi.Host) error {
	m.inUse.Store(m.key)
	return nil
}

// Secrets set in the panel win over the environment, reach the running
// modules that refer to them at once, and are stored only when those modules
// build with them. Their values stay out of the records and the log.
func TestSecrets(t *testing.T) {
	ctx := context.Background()
	storage, err := modulestore.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	s, rec := settingsHarness(t, storage)
	var log bytes.Buffer
	s.log = slog.New(slog.NewTextHandler(&log, &slog.HandlerOptions{Level: slog.LevelDebug}))
	var inUse atomic.Value
	s.kinds = map[otogi.ModuleID]moduleKind{"keyed": kind(func(c keyedConfig, env moduleEnv) (otogi.Module, error) {
		key, err := c.Key.Resolve(env.secret)
		if err != nil {
			return nil, err
		}
		if key == "value-refused" {
			return nil, errors.New("the key is refused")
		}
		return &keyed{key: key, inUse: &inUse}, nil
	})}
	on := true
	list := func() string {
		t.Helper()
		secrets, err := s.secrets(ctx)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(secrets)
		return string(data)
	}
	stored := func() string {
		t.Helper()
		secrets, err := storage.Secrets(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, secret := range secrets {
			out = append(out, secret.Name+"="+secret.Value)
		}
		return strings.Join(out, " ")
	}

	// A reference to nothing is refused, naming the secret.
	settings := json.RawMessage(`{"key": "${OTOGI_TEST_KEY}"}`)
	if _, err := s.save(ctx, "keyed", 0, &on, settings, "Tundra"); !errors.Is(err, otogi.ErrInvalid) || !strings.Contains(err.Error(), "OTOGI_TEST_KEY") {
		t.Fatalf("settings referring to a secret set nowhere: %v", err)
	}
	// The environment serves a secret the panel does not hold.
	t.Setenv("OTOGI_TEST_KEY", "value-from-environment")
	if _, err := s.save(ctx, "keyed", 0, &on, settings, "Tundra"); err != nil {
		t.Fatal(err)
	}
	if inUse.Load() != "value-from-environment" {
		t.Fatalf("in use: %v", inUse.Load())
	}
	if got, want := list(), `[{"name":"OTOGI_TEST_KEY","source":"environment","references":["keyed"]}]`; got != want {
		t.Fatalf("secrets %s, want %s", got, want)
	}

	// One set in the panel wins, and the module is rebuilt with it.
	restarted, err := s.setSecret(ctx, "OTOGI_TEST_KEY", "value-from-panel", "Tundra")
	if err != nil || !slices.Equal(restarted, []otogi.ModuleID{"keyed"}) {
		t.Fatalf("set: restarted %v, %v", restarted, err)
	}
	if inUse.Load() != "value-from-panel" {
		t.Fatalf("in use after setting: %v", inUse.Load())
	}
	if got := list(); !strings.Contains(got, `"source":"panel","by":"Tundra","at":"`) || !strings.Contains(got, `"references":["keyed"]`) {
		t.Fatalf("secrets %s", got)
	}
	if _, err := s.setSecret(ctx, "OTOGI_TEST_KEY", "value-replaced", "Luna"); err != nil || inUse.Load() != "value-replaced" {
		t.Fatalf("replace: %v, in use %v", err, inUse.Load())
	}
	// A value the module does not build with is not stored.
	if _, err := s.setSecret(ctx, "OTOGI_TEST_KEY", "value-refused", "Luna"); !errors.Is(err, otogi.ErrInvalid) || !strings.Contains(err.Error(), "keyed") {
		t.Fatalf("a value the module refuses: %v", err)
	}
	if inUse.Load() != "value-replaced" || stored() != "OTOGI_TEST_KEY=value-replaced" {
		t.Fatalf("after a refused value: in use %v, stored %s", inUse.Load(), stored())
	}
	// A secret nothing refers to is stored, and nothing restarts.
	if restarted, err := s.setSecret(ctx, "OTOGI_TEST_SPARE", "value-spare", "Luna"); err != nil || len(restarted) != 0 {
		t.Fatalf("set an unused secret: restarted %v, %v", restarted, err)
	}
	if got := list(); !strings.Contains(got, `{"name":"OTOGI_TEST_SPARE","source":"panel","by":"Luna"`) || !strings.Contains(got, `"references":[]`) {
		t.Fatalf("secrets %s", got)
	}
	// The next start builds from the stored value.
	inUse.Store("")
	if got := nextStart(t, s); !slices.Equal(got, []otogi.ModuleID{"keyed"}) || inUse.Load() != "value-replaced" {
		t.Fatalf("next start: running %v, in use %v", got, inUse.Load())
	}

	// Deleting falls back to the environment while it has the name, and is
	// refused once nothing would be left for the settings that refer to it.
	if restarted, err := s.deleteSecret(ctx, "OTOGI_TEST_KEY", "Luna"); err != nil || !slices.Equal(restarted, []otogi.ModuleID{"keyed"}) || inUse.Load() != "value-from-environment" {
		t.Fatalf("delete: restarted %v, %v, in use %v", restarted, err, inUse.Load())
	}
	if _, err := s.deleteSecret(ctx, "OTOGI_TEST_KEY", "Luna"); !errors.Is(err, management.ErrNotFound) {
		t.Fatalf("delete a secret not stored: %v", err)
	}
	if _, err := s.setSecret(ctx, "OTOGI_TEST_KEY", "value-again", "Luna"); err != nil {
		t.Fatal(err)
	}
	if err := os.Unsetenv("OTOGI_TEST_KEY"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.deleteSecret(ctx, "OTOGI_TEST_KEY", "Luna"); !errors.Is(err, management.ErrInUse) || !strings.Contains(err.Error(), "keyed") {
		t.Fatalf("delete a secret in use: %v", err)
	}
	if stored() != "OTOGI_TEST_KEY=value-again OTOGI_TEST_SPARE=value-spare" || inUse.Load() != "value-again" {
		t.Fatalf("after a refused delete: in use %v, stored %s", inUse.Load(), stored())
	}

	// A reference left dangling shows as such: here the environment lost
	// what the settings refer to.
	t.Setenv("OTOGI_TEST_GONE", "value-gone")
	if _, err := s.save(ctx, "keyed", 1, nil, json.RawMessage(`{"key": "${OTOGI_TEST_GONE}"}`), "Tundra"); err != nil {
		t.Fatal(err)
	}
	if err := os.Unsetenv("OTOGI_TEST_GONE"); err != nil {
		t.Fatal(err)
	}
	if got := list(); !strings.Contains(got, `{"name":"OTOGI_TEST_GONE","source":"missing","references":["keyed"]}`) {
		t.Fatalf("secrets %s", got)
	}

	var actions []string
	for _, r := range rec.list {
		if r.Kind == "secret.changed" {
			p := r.Payload.(map[string]any)
			actions = append(actions, r.Component+" "+p["action"].(string)+" by "+p["by"].(string))
		}
	}
	if want := []string{
		"secret:OTOGI_TEST_KEY set by Tundra", "secret:OTOGI_TEST_KEY replaced by Luna", "secret:OTOGI_TEST_SPARE set by Luna",
		"secret:OTOGI_TEST_KEY deleted by Luna", "secret:OTOGI_TEST_KEY set by Luna",
	}; !slices.Equal(actions, want) {
		t.Fatalf("secret.changed records %q, want %q", actions, want)
	}
	recorded, err := json.Marshal(rec.list)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "OTOGI_TEST_KEY") {
		t.Fatalf("the log does not name the secret changed: %s", log.String())
	}
	for _, text := range []string{string(recorded), log.String(), list()} {
		if strings.Contains(text, "value-") {
			t.Fatalf("a secret's value is in %s", text)
		}
	}
}
