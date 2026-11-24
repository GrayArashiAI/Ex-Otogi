package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/capture"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/management"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/internal/kernel"
	"github.com/TundraWork/Ex-Otogi/modules/utility"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// sectionsHarness starts the runtime as run does: the sections loaded from
// the store and the config file, then a kernel with the enabled modules and
// the command parser. It returns what the bot sends as well.
func sectionsHarness(t *testing.T, storage *modulestore.Storage, cfg Config, log *slog.Logger) (*moduleSettings, *records, *capture.Effector) {
	t.Helper()
	ctx := context.Background()
	rec := &records{}
	sys := newSystem(new(slog.LevelVar), rec, log)
	s := &moduleSettings{store: storage, kinds: moduleKinds, recorder: rec, log: log, system: sys, env: moduleEnv{
		operators: sys.operatorList, llm: sys.provider,
		status: func(context.Context) utility.Status { return utility.Status{Version: "test"} },
	}}
	if err := s.loadSections(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	modules, err := s.enabled(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sent := capture.New()
	k, err := kernel.New(kernel.Config{Storage: storage}, modules, []otogi.Middleware{kernel.CommandParser(&sys.prefixes)}, sent, log)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = k.Stop(ctx) })
	s.kernel = k
	return s, rec, sent
}

func openStore(t *testing.T) *modulestore.Storage {
	t.Helper()
	storage, err := modulestore.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	return storage
}

func fileConfig(t *testing.T, body string) Config {
	t.Helper()
	cfg, err := loadConfig(writeConfig(t, body))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// say sends text from a person to the bot and returns what it answered.
func say(t *testing.T, s *moduleSettings, sent *capture.Effector, actor otogi.ActorID, text string) string {
	t.Helper()
	before := len(sent.Effects())
	done := make(chan otogi.Outcome, 1)
	err := s.kernel.Submit(context.Background(), otogi.Event{
		ID: otogi.EventID(text + time.Now().String()), Platform: "telegram", Account: "main", Conversation: otogi.Conversation{ID: "chat:1", Kind: otogi.ConversationGroup},
		Actor: otogi.Actor{ID: actor}, Action: otogi.ActionMessageCreated, Direction: otogi.DirectionIncoming, Message: otogi.TextMessage(text),
	}, func(o otogi.Outcome) { done <- o })
	if err != nil {
		t.Fatal(err)
	}
	<-done
	var out []string
	for _, e := range sent.Effects()[before:] {
		out = append(out, e.Message.PlainText())
	}
	return strings.Join(out, "\n")
}

// Every general setting applies when it is saved: nothing restarts, and the
// module built before the change sees it.
func TestGeneralSettingsApplyAtOnce(t *testing.T) {
	ctx := context.Background()
	storage := openStore(t)
	s, rec, sent := sectionsHarness(t, storage, fileConfig(t, ""), slog.New(slog.DiscardHandler))
	on := true
	if _, err := s.save(ctx, "utility", 0, &on, json.RawMessage(`{}`), "Tundra"); err != nil {
		t.Fatal(err)
	}

	// A fresh install runs on the defaults, and has no revision.
	if got := say(t, s, sent, "user:1", "/ping"); got != "Pong." {
		t.Fatalf("/ping on the defaults: %q", got)
	}
	if got := say(t, s, sent, "user:1", "/status"); !strings.Contains(got, "Only operators") {
		t.Fatalf("/status from nobody's operator: %q", got)
	}
	if s.system.retention() != 30*24*time.Hour || s.system.level.Level() != slog.LevelInfo {
		t.Fatalf("defaults: retention %s, level %s", s.system.retention(), s.system.level.Level())
	}
	if cur, _ := s.current(ctx, generalSection); cur.Revision != 0 {
		t.Fatalf("a fresh install has revision %d", cur.Revision)
	}

	r, err := s.save(ctx, generalSection, 0, nil, json.RawMessage(`{"operators": ["telegram:user:1"], "command_prefixes": ["!"], "eventlog_retention": "48h", "log_level": "debug"}`), "Tundra")
	if err != nil {
		t.Fatal(err)
	}
	if r.Revision != 1 || r.By != "Tundra" {
		t.Fatalf("revision %+v", r)
	}
	if got := say(t, s, sent, "user:1", "!ping"); got != "Pong." {
		t.Fatalf("!ping after the prefix became !: %q", got)
	}
	if got := say(t, s, sent, "user:1", "/ping"); got != "" {
		t.Fatalf("/ping after the prefix became !: %q", got)
	}
	if got := say(t, s, sent, "user:1", "!status"); !strings.Contains(got, "Version: test") {
		t.Fatalf("!status from a new operator: %q", got)
	}
	if got := say(t, s, sent, "user:2", "!status"); !strings.Contains(got, "Only operators") {
		t.Fatalf("!status from someone else: %q", got)
	}
	if s.system.retention() != 48*time.Hour || s.system.level.Level() != slog.LevelDebug {
		t.Fatalf("after saving: retention %s, level %s", s.system.retention(), s.system.level.Level())
	}
	last := rec.list[len(rec.list)-1]
	p, _ := last.Payload.(map[string]any)
	if last.Kind != "system.reconfigured" || last.Component != "system:general" || p["revision"] != int64(1) || p["by"] != "Tundra" ||
		len(p["restarted"].([]otogi.ModuleID)) != 0 || !strings.Contains(string(p["settings"].(json.RawMessage)), `"log_level":"debug"`) {
		t.Fatalf("record %+v", last)
	}

	// An empty list of prefixes turns commands off.
	if _, err := s.save(ctx, generalSection, 1, nil, json.RawMessage(`{"command_prefixes": []}`), "Tundra"); err != nil {
		t.Fatal(err)
	}
	if got := say(t, s, sent, "user:1", "!ping") + say(t, s, sent, "user:1", "/ping"); got != "" {
		t.Fatalf("a command with no prefix set: %q", got)
	}

	// What is refused changes nothing.
	for name, c := range map[string]struct{ settings, want string }{
		"an operator that is not an ID": {`{"operators": ["tundra"]}`, "operators"},
		"an unknown level":              {`{"log_level": "loud"}`, "log_level"},
		"an empty prefix":               {`{"command_prefixes": [""]}`, "command_prefixes"},
		"an unknown setting":            {`{"colour": "red"}`, "colour"},
		"a retention that is no time":   {`{"eventlog_retention": "soon"}`, "eventlog_retention"},
	} {
		if _, err := s.save(ctx, generalSection, 2, nil, json.RawMessage(c.settings), "Tundra"); !errors.Is(err, otogi.ErrInvalid) || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := s.save(ctx, generalSection, 1, nil, json.RawMessage(`{}`), "Tundra"); !errors.As(err, new(*management.StaleRevisionError)) {
		t.Fatalf("saving over a stale revision: %v", err)
	}
	if cur, _ := s.current(ctx, generalSection); cur.Revision != 2 || string(cur.Settings) != `{"command_prefixes":[]}` {
		t.Fatalf("after refused saves: %+v", cur)
	}
	// The settings left out are the defaults again.
	if s.system.retention() != 30*24*time.Hour || s.system.level.Level() != slog.LevelInfo || len(s.system.operatorList()) != 0 {
		t.Fatalf("after saving without them: retention %s, level %s, operators %v", s.system.retention(), s.system.level.Level(), s.system.operatorList())
	}
}

// asker is a module built with a language model provider, which it holds.
type asker struct {
	provider llm.Provider
	inUse    *atomic.Pointer[asker]
}

type askerConfig struct {
	Provider string `json:"provider"`
}

func (*asker) Descriptor() otogi.ModuleDescriptor { return otogi.ModuleDescriptor{ID: "asker"} }

func (*asker) Handle(context.Context, otogi.Event, otogi.Invocation) error { return nil }

func (m *asker) Start(context.Context, otogi.Host) error {
	m.inUse.Store(m)
	return nil
}

// modelService is a Chat Completions service that notes the key each
// request came with, and answers none.
func modelService(t *testing.T) (url string, keys func() []string) {
	t.Helper()
	var got atomic.Pointer[[]string]
	got.Store(&[]string{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next := append(slices.Clone(*got.Load()), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		got.Store(&next)
		http.Error(w, `{"error": {"message": "no"}}`, http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() []string { return *got.Load() }
}

// Saving the language model settings rebuilds the running modules built with
// a provider, and no others; a save they do not build with changes nothing;
// and a provider's replaced key reaches them too.
func TestLLMSettingsRebuildTheModulesUsingAProvider(t *testing.T) {
	ctx := context.Background()
	storage := openStore(t)
	var log bytes.Buffer
	s, rec, _ := sectionsHarness(t, storage, fileConfig(t, ""), slog.New(slog.NewTextHandler(&log, &slog.HandlerOptions{Level: slog.LevelDebug})))
	var inUse atomic.Pointer[asker]
	s.kinds = map[otogi.ModuleID]moduleKind{
		"asker": kind(func(c askerConfig, env moduleEnv) (otogi.Module, error) {
			p, err := env.llm(c.Provider)
			if err != nil {
				return nil, err
			}
			return &asker{provider: p, inUse: &inUse}, nil
		}),
		"plain": kind(func(struct{}, moduleEnv) (otogi.Module, error) { return plain{}, nil }),
	}
	first, firstKeys := modelService(t)
	second, secondKeys := modelService(t)
	ask := func() {
		t.Helper()
		_, _ = inUse.Load().provider.Generate(ctx, llm.Request{Model: "m", Turns: []llm.Turn{{Role: llm.RoleUser, Content: otogi.TextMessage("hi")}}}, nil)
	}
	llmAt := func(url string) json.RawMessage {
		return json.RawMessage(`{"openai": {"deepseek": {"base_url": "` + url + `", "api_key": "${OTOGI_TEST_LLM_KEY}"}}}`)
	}
	on := true

	// A key set nowhere is refused, naming it.
	if _, err := s.save(ctx, llmSection, 0, nil, llmAt(first), "Tundra"); !errors.Is(err, otogi.ErrInvalid) || !strings.Contains(err.Error(), "OTOGI_TEST_LLM_KEY") {
		t.Fatalf("a provider whose key is set nowhere: %v", err)
	}
	t.Setenv("OTOGI_TEST_LLM_KEY", "value-from-environment")
	if _, err := s.save(ctx, llmSection, 0, nil, llmAt(first), "Tundra"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.save(ctx, "asker", 0, &on, json.RawMessage(`{"provider": "deepseek"}`), "Tundra"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.save(ctx, "plain", 0, &on, json.RawMessage(`{}`), "Tundra"); err != nil {
		t.Fatal(err)
	}
	ask()
	if !slices.Equal(firstKeys(), []string{"value-from-environment"}) {
		t.Fatalf("the first service was asked with %q", firstKeys())
	}

	// The provider moves: the module using it is rebuilt, and asks there.
	before := inUse.Load()
	if _, err := s.save(ctx, llmSection, 1, nil, llmAt(second), "Tundra"); err != nil {
		t.Fatal(err)
	}
	last := rec.list[len(rec.list)-1]
	if p := last.Payload.(map[string]any); last.Kind != "system.reconfigured" || last.Component != "system:llm" || !slices.Equal(p["restarted"].([]otogi.ModuleID), []otogi.ModuleID{"asker"}) {
		t.Fatalf("record %+v", last)
	}
	if inUse.Load() == before {
		t.Fatal("the module using the provider was not rebuilt")
	}
	ask()
	if len(firstKeys()) != 1 || !slices.Equal(secondKeys(), []string{"value-from-environment"}) {
		t.Fatalf("after the provider moved: first %q, second %q", firstKeys(), secondKeys())
	}

	// Settings the module does not build with are refused, and nothing
	// changes: the provider is still there, and so is the revision.
	before = inUse.Load()
	for name, settings := range map[string]string{
		"without the provider the module names": `{}`,
		"a pasted key":                          `{"openai": {"deepseek": {"base_url": "` + first + `", "api_key": "value-pasted"}}}`,
		"an address that is no URL":             `{"openai": {"deepseek": {"base_url": "deepseek", "api_key": "${OTOGI_TEST_LLM_KEY}"}}}`,
		"Gemini's name":                         `{"openai": {"gemini": {"base_url": "` + first + `", "api_key": "${OTOGI_TEST_LLM_KEY}"}}}`,
	} {
		if _, err := s.save(ctx, llmSection, 2, nil, json.RawMessage(settings), "Tundra"); !errors.Is(err, otogi.ErrInvalid) || strings.Contains(err.Error(), "value-") {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if cur, _ := s.current(ctx, llmSection); cur.Revision != 2 || !bytes.Equal(cur.Settings, mustCompact(t, llmAt(second))) {
		t.Fatalf("after refused saves: %+v", cur)
	}
	if inUse.Load() != before {
		t.Fatal("a refused save rebuilt the module")
	}
	if p, err := s.env.llm("deepseek"); err != nil || p != inUse.Load().provider {
		t.Fatalf("after refused saves the provider is %v, %v", p, err)
	}

	// The secret a provider refers to shows as the section's, and replacing
	// it rebuilds the provider and the module.
	if got := secretList(t, s); got != `[{"name":"OTOGI_TEST_LLM_KEY","source":"environment","references":["system:llm"]}]` {
		t.Fatalf("secrets %s", got)
	}
	restarted, err := s.setSecret(ctx, "OTOGI_TEST_LLM_KEY", "value-from-panel", "Tundra")
	if err != nil || !slices.Equal(restarted, []otogi.ModuleID{"asker"}) {
		t.Fatalf("replacing the provider's key: restarted %v, %v", restarted, err)
	}
	ask()
	if !slices.Equal(secondKeys(), []string{"value-from-environment", "value-from-panel"}) {
		t.Fatalf("after replacing the key, asked with %q", secondKeys())
	}
	if err := os.Unsetenv("OTOGI_TEST_LLM_KEY"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.deleteSecret(ctx, "OTOGI_TEST_LLM_KEY", "Tundra"); !errors.Is(err, management.ErrInUse) || !strings.Contains(err.Error(), "system:llm") {
		t.Fatalf("deleting the key a provider refers to: %v", err)
	}

	// The next start builds the providers from the stored settings.
	again, _, _ := sectionsHarness(t, storage, fileConfig(t, ""), slog.New(slog.DiscardHandler))
	if _, err := again.env.llm("deepseek"); err != nil {
		t.Fatalf("the provider after a restart: %v", err)
	}

	recorded, err := json.Marshal(rec.list)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{string(recorded), log.String(), secretList(t, s)} {
		if strings.Contains(text, "value-") {
			t.Fatalf("a secret's value is in %s", text)
		}
	}
}

func mustCompact(t *testing.T, data json.RawMessage) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := json.Compact(&out, data); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func secretList(t *testing.T, s *moduleSettings) string {
	t.Helper()
	secrets, err := s.secrets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(secrets)
	return string(data)
}

func storedSecrets(t *testing.T, storage *modulestore.Storage) string {
	t.Helper()
	secrets, err := storage.Secrets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, secret := range secrets {
		out = append(out, secret.Name+"="+secret.Value+" by "+secret.By)
	}
	return strings.Join(out, " ")
}

const movedKeysFile = `
operators: ["telegram:user:1"]
commands:
  prefixes: ["!"]
eventlog:
  retention: 48h
log:
  level: WARN
`

// A section with no revision takes its first from the keys otogi.yaml has
// for it. Once it has one, the file's keys no longer apply, and the start
// says so once.
func TestSectionsAreImportedFromTheFileOnce(t *testing.T) {
	ctx := context.Background()
	storage := openStore(t)
	t.Setenv("OTOGI_TEST_GEMINI_KEY", "value-from-environment")
	file := movedKeysFile + "llm:\n  gemini:\n    api_key: ${OTOGI_TEST_GEMINI_KEY}\n    base_url: https://gateway.example/gemini\n"
	var log bytes.Buffer
	s, rec, sent := sectionsHarness(t, storage, fileConfig(t, file), slog.New(slog.NewTextHandler(&log, &slog.HandlerOptions{Level: slog.LevelDebug})))

	general, _ := s.current(ctx, generalSection)
	if general.Revision != 1 || general.By != "otogi.yaml" || string(general.Settings) != `{"operators":["telegram:user:1"],"command_prefixes":["!"],"eventlog_retention":"48h","log_level":"warn"}` {
		t.Fatalf("the general settings imported: %+v", general)
	}
	models, _ := s.current(ctx, llmSection)
	if models.Revision != 1 || models.By != "otogi.yaml" || string(models.Settings) != `{"gemini":{"api_key":"${OTOGI_TEST_GEMINI_KEY}","base_url":"https://gateway.example/gemini"}}` {
		t.Fatalf("the language model settings imported: %+v", models)
	}
	// A key the file takes from the environment stays there.
	if got := storedSecrets(t, storage); got != "" {
		t.Fatalf("secrets stored by the import: %s", got)
	}
	if s.system.retention() != 48*time.Hour || s.system.level.Level() != slog.LevelWarn || !slices.Equal(s.system.operatorList(), []string{"telegram:user:1"}) {
		t.Fatalf("imported: retention %s, level %s, operators %v", s.system.retention(), s.system.level.Level(), s.system.operatorList())
	}
	if _, err := s.env.llm("gemini"); err != nil {
		t.Fatal(err)
	}
	on := true
	if _, err := s.save(ctx, "utility", 0, &on, json.RawMessage(`{}`), "Tundra"); err != nil {
		t.Fatal(err)
	}
	if got := say(t, s, sent, "user:1", "!ping"); got != "Pong." {
		t.Fatalf("!ping with the file's prefix: %q", got)
	}
	var imported []string
	for _, r := range rec.list {
		if r.Kind == "system.reconfigured" {
			imported = append(imported, r.Component+" by "+r.Payload.(map[string]any)["by"].(string))
		}
	}
	if !slices.Equal(imported, []string{"system:general by otogi.yaml", "system:llm by otogi.yaml"}) {
		t.Fatalf("records of the import: %q", imported)
	}
	if strings.Contains(log.String(), "level=WARN") {
		t.Fatalf("the first start warns: %s", log.String())
	}

	// The second start: the file says other things now, and they are
	// ignored with one warning that names the keys.
	log.Reset()
	changed := strings.NewReplacer("user:1", "user:2", `"!"`, `"?"`, "48h", "72h", "WARN", "error", "gateway.example", "other.example").Replace(file)
	s, rec, _ = sectionsHarness(t, storage, fileConfig(t, changed), slog.New(slog.NewTextHandler(&log, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if s.system.retention() != 48*time.Hour || s.system.level.Level() != slog.LevelWarn || !slices.Equal(s.system.operatorList(), []string{"telegram:user:1"}) || !slices.Equal(*s.system.prefixes.Load(), []string{"!"}) {
		t.Fatalf("second start: retention %s, level %s, operators %v, prefixes %v", s.system.retention(), s.system.level.Level(), s.system.operatorList(), *s.system.prefixes.Load())
	}
	if cur, _ := s.current(ctx, llmSection); cur.Revision != 1 || !strings.Contains(string(cur.Settings), "gateway.example") {
		t.Fatalf("second start: %+v", cur)
	}
	if len(rec.list) != 0 {
		t.Fatalf("second start recorded %+v", rec.list)
	}
	if strings.Count(log.String(), "level=WARN") != 1 {
		t.Fatalf("second start: want one warning: %s", log.String())
	}
	for _, key := range []string{"operators", "commands.prefixes", "eventlog.retention", "log.level", "llm", "panel"} {
		if !strings.Contains(log.String(), key) {
			t.Fatalf("the warning does not name %s: %s", key, log.String())
		}
	}
	if strings.Contains(log.String(), "value-") {
		t.Fatalf("a secret's value is in %s", log.String())
	}
}

// A key written into the file itself is moved to the secrets, and the
// settings refer to it there: it is in no revision, record, or log line.
func TestImportMovesAKeyWrittenInTheFileToTheSecrets(t *testing.T) {
	ctx := context.Background()
	storage := openStore(t)
	t.Setenv("OTOGI_TEST_GEMINI_KEY", "value-from-environment")
	file := "llm:\n  gemini: {api_key: \"${OTOGI_TEST_GEMINI_KEY}\"}\n  openai:\n    deep-seek: {base_url: https://api.deepseek.com, api_key: value-literal}\n"
	var log bytes.Buffer
	s, rec, _ := sectionsHarness(t, storage, fileConfig(t, file), slog.New(slog.NewTextHandler(&log, &slog.HandlerOptions{Level: slog.LevelDebug})))

	models, _ := s.current(ctx, llmSection)
	if string(models.Settings) != `{"gemini":{"api_key":"${OTOGI_TEST_GEMINI_KEY}"},"openai":{"deep-seek":{"base_url":"https://api.deepseek.com","api_key":"${OTOGI_LLM_DEEP_SEEK_API_KEY}"}}}` {
		t.Fatalf("the language model settings imported: %s", models.Settings)
	}
	if got := storedSecrets(t, storage); got != "OTOGI_LLM_DEEP_SEEK_API_KEY=value-literal by otogi.yaml" {
		t.Fatalf("secrets stored by the import: %s", got)
	}
	if _, err := s.env.llm("deep-seek"); err != nil {
		t.Fatal(err)
	}
	if got := secretList(t, s); !strings.Contains(got, `{"name":"OTOGI_LLM_DEEP_SEEK_API_KEY","source":"panel","by":"otogi.yaml"`) || !strings.Contains(got, `"references":["system:llm"]`) {
		t.Fatalf("secrets %s", got)
	}
	if general, _ := s.current(ctx, generalSection); general.Revision != 0 {
		t.Fatalf("the general settings, which the file does not have: %+v", general)
	}
	recorded, err := json.Marshal(rec.list)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(recorded), `"secret.changed"`) || !strings.Contains(string(recorded), "OTOGI_LLM_DEEP_SEEK_API_KEY") {
		t.Fatalf("the import's records do not name the secret it set: %s", recorded)
	}
	revisions, err := storage.SettingsRevisions(ctx, llmSection, 9, 9)
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := json.Marshal(revisions)
	for _, text := range []string{string(recorded), log.String(), string(stored), secretList(t, s)} {
		if strings.Contains(text, "value-") {
			t.Fatalf("a secret's value is in %s", text)
		}
	}
}

// plain is a module that uses nothing a section changes.
type plain struct{}

func (plain) Descriptor() otogi.ModuleDescriptor { return otogi.ModuleDescriptor{ID: "plain"} }

func (plain) Handle(context.Context, otogi.Event, otogi.Invocation) error { return nil }
