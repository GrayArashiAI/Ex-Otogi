package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/capture"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/internal/kernel"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// failing is a module that provides a service, and whose Start fails while
// its settings say so, or runs stop first when one is given.
type failing struct {
	fail bool
	stop *atomic.Pointer[context.CancelFunc]
}

type failingConfig struct {
	Fail bool `json:"fail"`
}

func (*failing) Descriptor() otogi.ModuleDescriptor {
	return otogi.ModuleDescriptor{ID: "failing", Provides: []string{"failing.service"}}
}

func (*failing) Handle(context.Context, otogi.Event, otogi.Invocation) error { return nil }

func (m *failing) Start(ctx context.Context, host otogi.Host) error {
	if stop := m.stop.Load(); stop != nil {
		(*stop)()
		return ctx.Err()
	}
	if m.fail {
		return errors.New("its database does not migrate")
	}
	return host.Provide("failing.service", "the service")
}

// dependent requires the failing module's service.
type dependent struct{}

func (dependent) Descriptor() otogi.ModuleDescriptor {
	return otogi.ModuleDescriptor{ID: "dependent", Requires: []string{"failing.service"}}
}

func (dependent) Handle(context.Context, otogi.Event, otogi.Invocation) error { return nil }

// notStartedHarness stores the revisions, then starts a kernel and the
// modules that are on in it, as run does.
func notStartedHarness(t *testing.T, revisions ...modulestore.SettingsRevision) (*moduleSettings, *records, *atomic.Pointer[context.CancelFunc]) {
	t.Helper()
	ctx := context.Background()
	storage := openStore(t)
	for _, r := range revisions {
		if err := storage.AddSettings(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	stop := &atomic.Pointer[context.CancelFunc]{}
	rec := &records{}
	s := &moduleSettings{store: storage, recorder: rec, log: slog.New(slog.DiscardHandler), kinds: map[otogi.ModuleID]moduleKind{
		"failing": kind(func(c failingConfig, _ moduleEnv) (otogi.Module, error) {
			return &failing{fail: c.Fail, stop: stop}, nil
		}),
		"dependent": kind(func(struct{}, moduleEnv) (otogi.Module, error) { return dependent{}, nil }),
		"plain":     kind(func(struct{}, moduleEnv) (otogi.Module, error) { return plain{}, nil }),
	}}
	k, err := kernel.New(kernel.Config{Storage: storage}, nil, nil, capture.New(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	startModules(t, s, k)
	return s, rec, stop
}

// notStartedRecords returns the module.not_started records, as
// "component revision reason".
func notStartedRecords(rec *records) []string {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	var out []string
	for _, r := range rec.list {
		if r.Kind == "module.not_started" {
			p := r.Payload.(map[string]any)
			out = append(out, fmt.Sprintf("%s %d %s", r.Component, p["revision"], p["reason"]))
		}
	}
	return out
}

// A module whose Start fails at startup stays out, with the modules needing
// its service, and the rest run; saving other settings meanwhile works;
// saving it refuses while it still fails and starts it, with those needing
// it, once it does not.
func TestAModuleThatFailsToStartStaysOut(t *testing.T) {
	ctx := context.Background()
	s, rec, _ := notStartedHarness(t,
		modulestore.SettingsRevision{Module: "failing", Revision: 1, Enabled: true, Settings: json.RawMessage(`{"fail": true}`)},
		modulestore.SettingsRevision{Module: "dependent", Revision: 1, Enabled: true, Settings: json.RawMessage(`{}`)},
		modulestore.SettingsRevision{Module: "plain", Revision: 1, Enabled: true, Settings: json.RawMessage(`{}`)},
	)
	if got := running(s); !slices.Equal(got, []otogi.ModuleID{"plain"}) {
		t.Fatalf("running %v", got)
	}
	problems := func() map[string]string {
		t.Helper()
		modules, err := moduleAPI{settingsAPI{settings: s}}.Modules(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, m := range modules {
			out[m.ID] = m.Problem
		}
		return out
	}
	got := problems()
	if !strings.Contains(got["failing"], "its database does not migrate") || !strings.Contains(got["dependent"], `requires service "failing.service"`) || got["plain"] != "" {
		t.Fatalf("problems %v", got)
	}
	records := notStartedRecords(rec)
	if len(records) != 2 || !strings.HasPrefix(records[0], "module:failing 1 ") || !strings.Contains(records[0], "its database does not migrate") ||
		!strings.HasPrefix(records[1], `module:dependent 1 requires service "failing.service"`) {
		t.Fatalf("module.not_started records %q", records)
	}

	// Saving another module works; the failing one is tried again, fails
	// as before, and is not recorded again.
	if _, err := s.save(ctx, "plain", 1, nil, json.RawMessage(`{}`), "Tundra"); err != nil {
		t.Fatalf("saving another module: %v", err)
	}
	if got := notStartedRecords(rec); len(got) != 2 {
		t.Fatalf("module.not_started records after saving another module: %q", got)
	}

	// Saving it while it still fails is refused, with its error.
	if _, err := s.save(ctx, "failing", 1, nil, json.RawMessage(`{"fail": true}`), "Tundra"); err == nil || !strings.Contains(err.Error(), "its database does not migrate") {
		t.Fatalf("saving the module that still fails: %v", err)
	}

	// Once it starts, so does the module needing it, and the problems go.
	if _, err := s.save(ctx, "failing", 1, nil, json.RawMessage(`{"fail": false}`), "Tundra"); err != nil {
		t.Fatal(err)
	}
	if got := running(s); !slices.Equal(got, []otogi.ModuleID{"dependent", "failing", "plain"}) {
		t.Fatalf("running after the fix %v", got)
	}
	if got := problems(); got["failing"] != "" || got["dependent"] != "" {
		t.Fatalf("problems after the fix %v", got)
	}
}

// Turning a module off while another waits to start leaves it off: the
// modules tried after a save are those of the revisions it stored.
func TestTurningAModuleOffWhileAnotherWaitsLeavesItOff(t *testing.T) {
	ctx := context.Background()
	s, _, _ := notStartedHarness(t,
		modulestore.SettingsRevision{Module: "failing", Revision: 1, Enabled: true, Settings: json.RawMessage(`{"fail": true}`)},
		modulestore.SettingsRevision{Module: "plain", Revision: 1, Enabled: true, Settings: json.RawMessage(`{}`)},
	)
	off := false
	if _, err := s.save(ctx, "plain", 1, &off, nil, "Tundra"); err != nil {
		t.Fatal(err)
	}
	if got := running(s); len(got) != 0 {
		t.Fatalf("running after turning plain off %v", got)
	}
	// Turning off the one that did not start forgets why.
	if _, err := s.save(ctx, "failing", 1, &off, nil, "Tundra"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.notStarted.get("failing"); ok {
		t.Fatal("a module turned off keeps a reason it did not start")
	}
}

// A stop asked for while a waiting module starts is not that module failing
// to start, and does not fail a save that is applied and stored already.
func TestAStopWhileAModuleStartsIsNotItFailing(t *testing.T) {
	ctx := context.Background()
	s, rec, stop := notStartedHarness(t,
		modulestore.SettingsRevision{Module: "failing", Revision: 1, Enabled: true, Settings: json.RawMessage(`{"fail": true}`)},
		modulestore.SettingsRevision{Module: "plain", Revision: 1, Enabled: true, Settings: json.RawMessage(`{}`)},
	)
	saveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop.Store(&cancel) // the request ends while failing starts
	r, err := s.save(saveCtx, "plain", 1, nil, json.RawMessage(`{}`), "Tundra")
	if err != nil || r.Revision != 2 {
		t.Fatalf("a save whose request ended while a waiting module started: %+v, %v", r, err)
	}
	if reason, _ := s.notStarted.get("failing"); !strings.Contains(reason, "its database does not migrate") {
		t.Fatalf("failing's reason became %q", reason)
	}
	if got := notStartedRecords(rec); len(got) != 1 {
		t.Fatalf("module.not_started records %q", got)
	}

	// At startup, the stop ends startWaiting with its error.
	startCtx, cancelStart := context.WithCancel(ctx)
	defer cancelStart()
	stop.Store(&cancelStart)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.startWaiting(startCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("startWaiting stopped meanwhile: %v", err)
	}
	if got := notStartedRecords(rec); len(got) != 1 {
		t.Fatalf("module.not_started records after a stop at startup %q", got)
	}
}
