package main

import (
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
	t.Setenv("TEST_TOKEN", "secret")
	cfg, err := loadConfig(writeConfig(t, `
management:
  token: ${TEST_TOKEN}   # ${UNSET_IN_COMMENT} is not expanded
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Management.Token != "secret" || cfg.DataDir != "data" || *cfg.Management.Addr != "127.0.0.1:8090" || !filepath.IsAbs(cfg.path) {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestTypeErrorsReportTheOriginalLine(t *testing.T) {
	_, err := loadConfig(writeConfig(t, "# comment\n\n\nmanagement:\n  oidc:\n    allow: {a: b}\n"))
	if err == nil || !strings.Contains(err.Error(), "line 6") {
		t.Fatalf("error = %v, want line 6", err)
	}
}

func TestLoadConfigRejections(t *testing.T) {
	tests := map[string]struct{ body, want string }{
		"unknown key":        {"telegram: []\n", "field telegram not found"},
		"unset variable":     {"management:\n  token: ${OTOGI_TEST_UNSET}\n", "OTOGI_TEST_UNSET"},
		"panel without oidc": {"management: {public_url: https://otogi.example.com}\n", "management.oidc.issuer"},
		"oidc without allow": {"management: {public_url: https://otogi.example.com, oidc: {issuer: https://opus.name, client_id: c, client_secret: s}}\n", "management.oidc.allow"},
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
	if _, err := sys.provider("openai"); err == nil || !strings.Contains(err.Error(), `"openai"`) {
		t.Fatalf("openai: %v, want an error naming it", err)
	}
}
