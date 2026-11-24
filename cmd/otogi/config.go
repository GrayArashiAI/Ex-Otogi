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
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Config is the otogi.yaml file. See docs/operations.md. The keys of the
// settings that moved to the panel (all but data_dir and management) are
// still read, for a section's first revision: see loadSections.
type Config struct {
	DataDir         string            `yaml:"data_dir"`
	ShutdownTimeout time.Duration     `yaml:"shutdown_timeout"`
	Log             LogConfig         `yaml:"log"`
	Kernel          KernelConfig      `yaml:"kernel"`
	Management      ManagementConfig  `yaml:"management"`
	EventLog        EventLogConfig    `yaml:"eventlog"`
	Commands        CommandsConfig    `yaml:"commands"`
	Operators       []string          `yaml:"operators"`
	Telegram        []TelegramAccount `yaml:"telegram"`
	QQ              []QQAccount       `yaml:"qq"`
	LLM             LLMConfig         `yaml:"llm"`

	path  string // the file's, absolute
	moved movedKeys
}

// movedKeys is which of the keys that moved to the panel the file has.
type movedKeys struct {
	general []string   // of operators, commands.prefixes, eventlog.retention, and log.level
	process []string   // of kernel.*, log.format, and shutdown_timeout
	llm     *LLMConfig // llm as the file writes it, ${NAME} not expanded; nil without it
	// accounts is the accounts' secrets as the file writes them, ${NAME} not
	// expanded.
	accounts struct {
		Telegram []struct {
			AppHash string `yaml:"app_hash"`
		} `yaml:"telegram"`
		QQ []struct {
			AppSecret string `yaml:"app_secret"`
		} `yaml:"qq"`
	}
}

// movedIn finds the moved keys in the file, before ${NAME} is expanded.
func movedIn(root *yaml.Node) (movedKeys, error) {
	var file struct {
		Operators yaml.Node `yaml:"operators"`
		Commands  struct {
			Prefixes yaml.Node `yaml:"prefixes"`
		} `yaml:"commands"`
		EventLog struct {
			Retention yaml.Node `yaml:"retention"`
		} `yaml:"eventlog"`
		Log struct {
			Level  yaml.Node `yaml:"level"`
			Format yaml.Node `yaml:"format"`
		} `yaml:"log"`
		Kernel struct {
			Workers        yaml.Node `yaml:"workers"`
			MaxInFlight    yaml.Node `yaml:"max_in_flight"`
			HandlerTimeout yaml.Node `yaml:"handler_timeout"`
		} `yaml:"kernel"`
		ShutdownTimeout yaml.Node  `yaml:"shutdown_timeout"`
		LLM             *LLMConfig `yaml:"llm"`
	}
	if err := root.Decode(&file); err != nil {
		return movedKeys{}, fmt.Errorf("decode: %w", err)
	}
	moved := movedKeys{llm: file.LLM}
	if err := root.Decode(&moved.accounts); err != nil {
		return movedKeys{}, fmt.Errorf("decode: %w", err)
	}
	type key struct {
		name string
		node yaml.Node
	}
	for _, k := range []key{
		{"operators", file.Operators}, {"commands.prefixes", file.Commands.Prefixes},
		{"eventlog.retention", file.EventLog.Retention}, {"log.level", file.Log.Level},
	} {
		if !k.node.IsZero() {
			moved.general = append(moved.general, k.name)
		}
	}
	for _, k := range []key{
		{"kernel.workers", file.Kernel.Workers}, {"kernel.max_in_flight", file.Kernel.MaxInFlight}, {"kernel.handler_timeout", file.Kernel.HandlerTimeout},
		{"log.format", file.Log.Format}, {"shutdown_timeout", file.ShutdownTimeout},
	} {
		if !k.node.IsZero() {
			moved.process = append(moved.process, k.name)
		}
	}
	return moved, nil
}

// LogConfig is the file's log key, which moved to the panel.
type LogConfig struct {
	Level  string `yaml:"level"`  // debug | info | warn | error
	Format string `yaml:"format"` // json | text
}

// KernelConfig is the file's kernel key, which moved to the panel.
type KernelConfig struct {
	Workers        int           `yaml:"workers"`
	MaxInFlight    int           `yaml:"max_in_flight"`
	HandlerTimeout time.Duration `yaml:"handler_timeout"`
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

// CommandsConfig is the file's commands key, which moved to the panel.
type CommandsConfig struct {
	// Prefixes start a command; nil means ["/"], an empty list disables parsing.
	Prefixes []string `yaml:"prefixes"`
}

// EventLogConfig is the file's eventlog key, which moved to the panel.
type EventLogConfig struct {
	// Retention is how long records are kept; default 30 days.
	Retention time.Duration `yaml:"retention"`
}

// TelegramAccount configures one Telegram user account: in the file, where
// the accounts were before they moved to the panel, and as its driver takes
// it (accountsSettings.resolve).
type TelegramAccount struct {
	Account  string `yaml:"account"`
	AppID    int    `yaml:"app_id"`
	AppHash  string `yaml:"app_hash"`
	Session  string `yaml:"session"`
	Database string `yaml:"database"`
	// UploadThreads is how many parts of a file upload at once; default 8.
	UploadThreads int `yaml:"upload_threads"`
}

// QQAccount configures one QQ bot (QQ Open Platform, q.qq.com), as
// TelegramAccount does an account.
type QQAccount struct {
	Account   string `yaml:"account"`
	AppID     string `yaml:"app_id"`
	AppSecret string `yaml:"app_secret"`
	Database  string `yaml:"database"`
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
	moved, err := movedIn(&root)
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := expandEnv(&root); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	cfg := Config{moved: moved}
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
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.Management.Addr == nil {
		addr := "127.0.0.1:8090"
		c.Management.Addr = &addr
	}
	if c.Commands.Prefixes == nil {
		c.Commands.Prefixes = []string{"/"}
	}
	if c.EventLog.Retention == 0 {
		c.EventLog.Retention = 30 * 24 * time.Hour
	}
}

func (c Config) validate() error {
	var errs []error
	if c.ShutdownTimeout < 0 {
		errs = append(errs, errors.New("shutdown_timeout must not be negative"))
	}
	if _, err := logLevel(c.Log.Level); err != nil {
		errs = append(errs, fmt.Errorf("log.level %w", err))
	}
	if c.Log.Format != "" && c.Log.Format != "json" && c.Log.Format != "text" {
		errs = append(errs, fmt.Errorf("log.format %q: want json or text", c.Log.Format))
	}
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
	if c.EventLog.Retention < 0 {
		errs = append(errs, errors.New("eventlog.retention must not be negative"))
	}
	if c.Kernel.Workers < 0 || c.Kernel.MaxInFlight < 0 {
		errs = append(errs, errors.New("kernel.workers and kernel.max_in_flight must not be negative"))
	}
	errs = append(errs, c.accounts().validate()...)
	errs = append(errs, c.LLM.validate()...)
	for _, op := range c.Operators {
		if platform, actor, ok := strings.Cut(op, ":"); !ok || platform == "" || actor == "" {
			errs = append(errs, fmt.Errorf("operators: %q: want <platform>:<actor id>, as /whoami prints it", op))
		}
	}
	return errors.Join(errs...)
}

var envReference = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expandEnv replaces ${VAR} in every scalar value with the environment
// variable's value. A reference to an unset variable is an error.
func expandEnv(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		original := n.Value
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
		if n.Style == 0 && envReference.MatchString(original) {
			n.Tag = "" // let YAML infer the type of the substituted value, e.g. an int
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
