package main

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"

	"go.yaml.in/yaml/v3"
)

// Config is the otogi.yaml file: where the data is, and the management API,
// which the panel is served by. Everything else is edited in the panel. See
// docs/operations.md.
type Config struct {
	DataDir    string           `yaml:"data_dir"`
	Management ManagementConfig `yaml:"management"`

	path string // the file's, absolute
}

// ManagementConfig configures the management HTTP API.
type ManagementConfig struct {
	// Addr is the listen address; nil means 127.0.0.1:8090, "" disables the API.
	Addr *string `yaml:"addr"`
	// Token is the bearer token; required when Addr is not loopback.
	Token string `yaml:"token"`
	// PublicURL is where the control panel is served, such as
	// https://otogi.example.com. Empty turns the panel's sign-in off.
	PublicURL string `yaml:"public_url"`
	// OIDC is the identity provider the panel signs in with; required with
	// PublicURL.
	OIDC OIDCConfig `yaml:"oidc"`
}

// OIDCConfig is the panel's client at an OpenID Connect provider.
type OIDCConfig struct {
	// Issuer is the provider's issuer URL, such as https://opus.name.
	Issuer string `yaml:"issuer"`
	// ClientID and ClientSecret are the client registered for the panel,
	// with the redirect URI <public_url>/api/auth/callback.
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
	// Allow lists the provider usernames that may use the panel.
	Allow []string `yaml:"allow"`
}

// loadConfig reads, expands, strictly decodes, defaults, and validates the
// config file.
func loadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := expandEnv(&root); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	var cfg Config
	if cfg.path, err = filepath.Abs(path); err != nil {
		return Config{}, fmt.Errorf("config file path: %w", err)
	}
	if err := decodeStrict(&root, &cfg); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.DataDir == "" {
		c.DataDir = "data"
	}
	if c.Management.Addr == nil {
		addr := "127.0.0.1:8090"
		c.Management.Addr = &addr
	}
}

func (c Config) validate() error {
	var errs []error
	if u := c.Management.PublicURL; u != "" {
		if parsed, err := url.Parse(u); err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.Path != "" {
			errs = append(errs, fmt.Errorf("management.public_url %q: want an origin, such as https://otogi.example.com", u))
		}
		o := c.Management.OIDC
		if parsed, err := url.Parse(o.Issuer); err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			errs = append(errs, fmt.Errorf("management.oidc.issuer %q: want the provider's https issuer URL, which public_url needs to sign in", o.Issuer))
		}
		if o.ClientID == "" || o.ClientSecret == "" {
			errs = append(errs, errors.New("management.oidc: client_id and client_secret are required with public_url"))
		}
		if len(o.Allow) == 0 {
			errs = append(errs, errors.New("management.oidc.allow: list the provider usernames that may use the panel"))
		}
	}
	return errors.Join(errs...)
}

var envReference = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expandEnv replaces ${VAR} in every scalar value with the environment
// variable's value. A reference to an unset variable is an error.
func expandEnv(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		var missing []string
		n.Value = envReference.ReplaceAllStringFunc(n.Value, func(ref string) string {
			name := envReference.FindStringSubmatch(ref)[1]
			value, ok := os.LookupEnv(name)
			if !ok {
				missing = append(missing, name)
			}
			return value
		})
		if len(missing) > 0 {
			return fmt.Errorf("line %d: environment variable %v is not set", n.Line, missing)
		}
	}
	for _, child := range n.Content {
		if err := expandEnv(child); err != nil {
			return err
		}
	}
	return nil
}

// decodeStrict decodes n into v and rejects keys v does not define. Type
// errors carry the original file's line numbers. The unknown-key check
// re-encodes the tree, which shifts lines, so its errors name only the key.
func decodeStrict(n *yaml.Node, v any) error {
	if err := n.Decode(v); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	data, err := yaml.Marshal(n)
	if err != nil {
		return fmt.Errorf("re-encode config: %w", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(reflect.New(reflect.TypeOf(v).Elem()).Interface()); err != nil {
		return errors.New(linePrefix.ReplaceAllString(err.Error(), ""))
	}
	return nil
}

var linePrefix = regexp.MustCompile(`line \d+: `)
