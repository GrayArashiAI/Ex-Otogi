package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/capture"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/management"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/internal/kernel"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

type records struct {
	mu   sync.Mutex
	list []otogi.Record
}

func (r *records) Record(_ context.Context, rec otogi.Record) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.list = append(r.list, rec)
}

type silentModel struct{}

func (silentModel) Generate(context.Context, llm.Request, func(otogi.Message) error) (llm.Response, error) {
	return llm.Response{}, nil
}

// settingsHarness runs a kernel built from the stored settings, as run does.
func settingsHarness(t *testing.T, storage *modulestore.Storage) (*moduleSettings, *records) {
	t.Helper()
	rec := &records{}
	s := &moduleSettings{store: storage, kinds: moduleKinds, recorder: rec, log: slog.New(slog.DiscardHandler), env: moduleEnv{
		llm: func(string) (llm.Provider, error) { return silentModel{}, nil },
	}}
	modules, err := s.enabled(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	k, err := kernel.New(kernel.Config{Storage: storage}, modules, nil, capture.New(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = k.Stop(context.Background()) })
	s.kernel = k
	return s, rec
}

func running(s *moduleSettings) []otogi.ModuleID {
	var ids []otogi.ModuleID
	for _, d := range s.kernel.Descriptors() {
		ids = append(ids, d.ID)
	}
	return ids
}

// Saving a revision applies it to the running kernel; a module holding
// another's services is rebuilt with it; and the next start runs what the
// revisions say.
func TestModuleSettingsRevisions(t *testing.T) {
	ctx := context.Background()
	storage, err := modulestore.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	s, rec := settingsHarness(t, storage)
	on, off := true, false

	if _, err := s.save(ctx, "notes", 0, &on, json.RawMessage(`{"max_notes": 5}`), "Tundra's iPhone"); err != nil {
		t.Fatal(err)
	}
	chat := json.RawMessage(`{"profiles": [{"name": "Luna", "keywords": ["luna"], "provider": "gemini", "model": "m", "tools": ["notes"]}]}`)
	if _, err := s.save(ctx, "chat", 0, &on, chat, "Tundra's iPhone"); err != nil {
		t.Fatal(err)
	}
	if got := running(s); !slices.Equal(got, []otogi.ModuleID{"chat", "notes"}) {
		t.Fatalf("running %v", got)
	}
	if _, err := s.save(ctx, "notes", 0, &on, json.RawMessage(`{}`), "x"); !errors.As(err, new(*management.StaleRevisionError)) {
		t.Fatalf("saving over a stale revision: %v", err)
	}
	r, err := s.save(ctx, "notes", 1, nil, nil, "x") // the same settings again
	if err != nil {
		t.Fatal(err)
	}
	if r.Revision != 2 || string(r.Settings) != `{"max_notes":5}` {
		t.Fatalf("revision %+v", r)
	}
	last := rec.list[len(rec.list)-1]
	if p := last.Payload.(map[string]any); last.Kind != "module.reconfigured" || !slices.Equal(p["restarted"].([]otogi.ModuleID), []otogi.ModuleID{"chat"}) {
		t.Fatalf("record %+v", last)
	}
	if _, err := s.save(ctx, "notes", 2, &off, nil, "x"); err == nil || !strings.Contains(err.Error(), "chat") {
		t.Fatalf("turning off notes, which chat uses: %v", err)
	}
	if _, err := s.save(ctx, "chat", 1, &off, nil, "x"); err != nil {
		t.Fatal(err)
	}
	if got := running(s); !slices.Equal(got, []otogi.ModuleID{"notes"}) {
		t.Fatalf("running %v", got)
	}

	modules, err := s.enabled(ctx)
	if err != nil || len(modules) != 1 || modules[0].Descriptor().ID != "notes" {
		t.Fatalf("enabled after restart: %v, %v", modules, err)
	}
	if revs, _ := storage.SettingsRevisions(ctx, "notes", 3, 10); len(revs) != 2 || revs[0].Revision != 2 || revs[0].By != "x" || time.Since(revs[0].At) > time.Minute {
		t.Fatalf("history %+v", revs)
	}
}

// Settings that break their schema are refused field by field, before the
// module is built; a secret must be a reference.
func TestModuleSettingsValidation(t *testing.T) {
	ctx := context.Background()
	storage, err := modulestore.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	s, _ := settingsHarness(t, storage)
	for module, c := range map[otogi.ModuleID]struct{ settings, want string }{
		"chat":      {`{"profiles": [{"name": "Luna", "provider": "gemini", "model": "m", "temperature": 3}], "colour": "red"}`, "profiles[0].temperature;unexpected property"},
		"todou":     {`{"cli": "todou", "agents": {"neptune": {"server": "https://todou.test", "token": "todou_pat_x"}}}`, "agents.neptune.token"},
		"reminders": {`{"max_delay": "soon"}`, "max_delay"},
	} {
		_, err := s.save(ctx, module, 0, nil, json.RawMessage(c.settings), "x")
		var invalid *invalidSettings
		if !errors.As(err, &invalid) {
			t.Fatalf("%s: %v, want invalid settings", module, err)
		}
		var where []string
		for _, d := range invalid.details {
			var detail *huma.ErrorDetail
			if errors.As(d, &detail) {
				where = append(where, detail.Location)
			}
		}
		if got := strings.Join(where, ";"); !strings.Contains(got, strings.Split(c.want, ";")[0]) || strings.Contains(err.Error(), "todou_pat_x") {
			t.Errorf("%s: %v", module, err)
		}
	}
	if running(s) != nil {
		t.Fatal("a module with invalid settings runs")
	}
}

// Every module's settings have a schema document with the types it uses.
func TestSettingsSchemas(t *testing.T) {
	for id, k := range moduleKinds {
		doc, err := k.schemaDocument()
		if err != nil || doc["type"] != "object" {
			t.Fatalf("%s: %v %v", id, doc, err)
		}
	}
	doc, _ := moduleKinds["todou"].schemaDocument()
	data, _ := json.Marshal(doc)
	if !strings.Contains(string(data), `"format":"secret"`) || !strings.Contains(string(data), `"format":"duration"`) || !strings.Contains(string(data), `"$defs"`) {
		t.Fatalf("todou schema %s", data)
	}
}
