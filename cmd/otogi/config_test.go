package main

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "otogi.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfigDefaultsAndExpansion(t *testing.T) {
	t.Setenv("TEST_APP_HASH", "secret")
	cfg, err := loadConfig(writeConfig(t, `
data_dir: /srv/otogi
telegram:
  - account: main
    app_id: 1
    app_hash: ${TEST_APP_HASH}   # ${UNSET_IN_COMMENT} is not expanded
`))
	if err != nil {
		t.Fatal(err)
	}
	a := cfg.Telegram[0]
	if a.AppHash != "secret" || cfg.Log.Level != "info" || *cfg.Management.Addr != "127.0.0.1:8090" || !filepath.IsAbs(cfg.path) {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestEnvironmentValuesTakeTheirFieldType(t *testing.T) {
	t.Setenv("TEST_APP_ID", "12345")
	cfg, err := loadConfig(writeConfig(t, "telegram:\n  - account: a\n    app_id: ${TEST_APP_ID}\n    app_hash: \"${TEST_APP_ID}\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Telegram[0].AppID != 12345 || cfg.Telegram[0].AppHash != "12345" {
		t.Fatalf("account = %+v", cfg.Telegram[0])
	}
}

func TestTypeErrorsReportTheOriginalLine(t *testing.T) {
	_, err := loadConfig(writeConfig(t, "# comment\n\n\nshutdown_timeout: soon\n"))
	if err == nil || !strings.Contains(err.Error(), "line 4") {
		t.Fatalf("error = %v, want line 4", err)
	}
}

func TestLoadConfigRejections(t *testing.T) {
	tests := map[string]struct{ body, want string }{
		"unknown key":        {"telegrma: []\n", "field telegrma not found"},
		"unset variable":     {"telegram:\n  - account: a\n    app_id: 1\n    app_hash: ${OTOGI_TEST_UNSET}\n", "OTOGI_TEST_UNSET"},
		"duplicate":          {"telegram:\n  - {account: a, app_id: 1, app_hash: h}\n  - {account: a, app_id: 1, app_hash: h}\n", "used twice"},
		"missing app hash":   {"telegram:\n  - {account: a, app_id: 1}\n", "app_hash"},
		"no upload threads":  {"telegram:\n  - {account: a, app_id: 1, app_hash: h, upload_threads: -1}\n", "upload_threads -1"},
		"bad level":          {"log: {level: loud}\n", "log.level"},
		"panel without oidc": {"management: {public_url: https://otogi.example.com}\n", "management.oidc.issuer"},
		"oidc without allow": {"management: {public_url: https://otogi.example.com, oidc: {issuer: https://opus.name, client_id: c, client_secret: s}}\n", "management.oidc.allow"},
		"bad duration":       {"shutdown_timeout: soon\n", "line 1"},
		"bad operator":       {"operators: [\"123\"]\n", "<platform>:<actor id>"},
		"gemini without key": {"llm:\n  gemini: {base_url: https://g.example}\n", "llm.gemini.api_key"},
		"gemini bad url":     {"llm:\n  gemini: {api_key: k, base_url: g.example/v1}\n", "llm.gemini.base_url"},
		"openai bad name":    {"llm:\n  openai: {DeepSeek: {api_key: k, base_url: https://d.example}}\n", "llm.openai.DeepSeek"},
		"openai gemini name": {"llm:\n  openai: {gemini: {api_key: k, base_url: https://d.example}}\n", "the Gemini provider's"},
		"openai without url": {"llm:\n  openai: {deepseek: {api_key: k}}\n", "llm.openai.deepseek.base_url"},
		"openai bad url":     {"llm:\n  openai: {deepseek: {api_key: k, base_url: d.example}}\n", "llm.openai.deepseek.base_url"},
		"openai without key": {"llm:\n  openai: {deepseek: {base_url: https://d.example}}\n", "llm.openai.deepseek.api_key"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := loadConfig(writeConfig(t, test.body))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("loadConfig error = %v, want it to mention %q", err, test.want)
			}
		})
	}
}

func TestLLMProviders(t *testing.T) {
	t.Setenv("TEST_GEMINI_KEY", "k")
	sys := newSystem(new(slog.LevelVar), otogi.NopRecorder{}, slog.New(slog.DiscardHandler))
	settings := llmSettings{
		Gemini: &geminiSettings{APIKey: "${TEST_GEMINI_KEY}", BaseURL: "https://gateway.example/gemini"},
		OpenAI: map[string]openAISettings{"deepseek": {APIKey: "${TEST_GEMINI_KEY}", BaseURL: "https://api.deepseek.com"}},
	}
	if _, err := sys.applyLLM(settings, os.LookupEnv); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gemini", "deepseek"} {
		if p, err := sys.provider(name); p == nil || err != nil {
			t.Fatalf("%s: %v, %v", name, p, err)
		}
	}
	if _, err := sys.provider("openai"); !errors.Is(err, otogi.ErrInvalid) {
		t.Fatalf("openai: %v, want ErrInvalid", err)
	}
}
