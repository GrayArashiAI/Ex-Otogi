package main

import (
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"slices"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/gemini"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/openai"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// LLMConfig configures the language model providers modules can use. Each
// provider is optional; a module names the one it uses.
type LLMConfig struct {
	Gemini *GeminiConfig `yaml:"gemini"`
	// OpenAI are services that speak the OpenAI Chat Completions API, such
	// as DeepSeek, by the provider name modules use.
	OpenAI map[string]OpenAIConfig `yaml:"openai"`
}

// GeminiConfig configures the Google Gemini provider.
type GeminiConfig struct {
	// APIKey is the Gemini API key.
	APIKey string `yaml:"api_key"`
	// BaseURL is the API endpoint, with or without its version; empty means
	// https://generativelanguage.googleapis.com/v1beta. A proxy or gateway
	// that speaks the Gemini Interactions API works too.
	BaseURL string `yaml:"base_url"`
}

// OpenAIConfig configures one Chat Completions service.
type OpenAIConfig struct {
	// BaseURL is the API's base, without /chat/completions, for example
	// https://api.deepseek.com or https://api.openai.com/v1.
	BaseURL string `yaml:"base_url"`
	// APIKey authenticates requests.
	APIKey string `yaml:"api_key"`
}

// providerName is what an OpenAI-compatible service may be called.
var providerName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

func (c LLMConfig) validate() []error {
	var errs []error
	if g := c.Gemini; g != nil {
		if g.APIKey == "" {
			errs = append(errs, fmt.Errorf("llm.gemini.api_key is required"))
		}
		if g.BaseURL != "" && !httpURL(g.BaseURL) {
			errs = append(errs, fmt.Errorf("llm.gemini.base_url %q: want an absolute http(s) URL", g.BaseURL))
		}
	}
	for _, name := range sortedNames(c.OpenAI) {
		o := c.OpenAI[name]
		switch {
		case !providerName.MatchString(name):
			errs = append(errs, fmt.Errorf("llm.openai.%s: want a name of lowercase letters, digits, - and _", name))
		case name == gemini.Name:
			errs = append(errs, fmt.Errorf("llm.openai.%s: the name is the Gemini provider's", name))
		}
		if !httpURL(o.BaseURL) {
			errs = append(errs, fmt.Errorf("llm.openai.%s.base_url %q: want an absolute http(s) URL", name, o.BaseURL))
		}
		if o.APIKey == "" {
			errs = append(errs, fmt.Errorf("llm.openai.%s.api_key is required", name))
		}
	}
	return errs
}

// httpURL reports an absolute http or https URL.
func httpURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func sortedNames(m map[string]OpenAIConfig) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// buildProviders constructs the configured providers, by name. It does no
// I/O.
func buildProviders(cfg LLMConfig, recorder otogi.Recorder, log *slog.Logger) (map[string]llm.Provider, error) {
	providers := map[string]llm.Provider{}
	if g := cfg.Gemini; g != nil {
		p, err := gemini.New(gemini.Config{APIKey: g.APIKey, BaseURL: g.BaseURL, Recorder: recorder, Logger: log.With("component", "llm:"+gemini.Name)})
		if err != nil {
			return nil, fmt.Errorf("llm.gemini: %w", err)
		}
		providers[gemini.Name] = p
	}
	for _, name := range sortedNames(cfg.OpenAI) {
		o := cfg.OpenAI[name]
		p, err := openai.New(openai.Config{Name: name, BaseURL: o.BaseURL, APIKey: o.APIKey, Recorder: recorder, Logger: log.With("component", "llm:"+name)})
		if err != nil {
			return nil, fmt.Errorf("llm.openai.%s: %w", name, err)
		}
		providers[name] = p
	}
	return providers, nil
}

// providerLookup finds a provider a module's config names.
func providerLookup(providers map[string]llm.Provider) func(name string) (llm.Provider, error) {
	return func(name string) (llm.Provider, error) {
		p, ok := providers[name]
		if !ok {
			return nil, fmt.Errorf("provider %q is not configured under llm: %w", name, otogi.ErrInvalid)
		}
		return p, nil
	}
}
