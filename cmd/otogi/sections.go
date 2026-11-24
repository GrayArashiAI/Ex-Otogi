package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Sections are the settings outside modules that are edited in the panel.
// Each has a Config type, as a module has, and its settings are saved,
// validated, kept as revisions, and restored by the code that does so for
// modules (moduleSettings.save), under an ID starting with "system:", which
// no module's ID can be. What differs is applying them: a section's settings
// go to the system, where the process reads them each time it uses them.

const (
	sectionPrefix = "system:"

	generalSection otogi.ModuleID = sectionPrefix + "general"
	llmSection     otogi.ModuleID = sectionPrefix + "llm"

	// fileActor is who saved a revision imported from otogi.yaml.
	fileActor = "otogi.yaml"
)

// section is one group of settings outside modules.
type section struct {
	settingsSchema
	// file returns what otogi.yaml, where the settings were before, says
	// about the section: the keys it has, the settings they make, and the
	// secrets it spells out, by the name each is moved to.
	file func(cfg Config) (keys []string, settings any, literal map[string]string)
	// apply validates settings and puts them into effect. undo puts back
	// what there was, for when the modules do not build with them.
	apply func(sys *system, settings []byte, secret func(name string) (string, bool)) (undo func(), err error)
	// rebuilds picks the running modules to rebuild once the settings are
	// applied; nil when modules read the settings each time they use them.
	rebuilds func(s *moduleSettings, module otogi.ModuleID) bool
}

var sections = map[otogi.ModuleID]section{
	generalSection: sectionOf(generalFromFile, (*system).applyGeneral, nil),
	llmSection:     sectionOf(llmFromFile, (*system).applyLLM, (*moduleSettings).builtWithProvider),
}

func sectionOf[C any](
	file func(Config) ([]string, any, map[string]string),
	apply func(*system, C, func(string) (string, bool)) (func(), error),
	rebuilds func(*moduleSettings, otogi.ModuleID) bool,
) section {
	return section{
		settingsSchema: schemaOf[C](), file: file, rebuilds: rebuilds,
		apply: func(sys *system, settings []byte, secret func(string) (string, bool)) (func(), error) {
			c, err := decodeSettings[C](settings)
			if err != nil {
				return refused[func()](nil, err)
			}
			return refused(apply(sys, c, secret))
		},
	}
}

// system is the sections' settings as the process uses them now. Each is
// read where it is used, so a saved revision applies at once.
type system struct {
	operators atomic.Pointer[[]string]
	prefixes  atomic.Pointer[[]string] // the command parser's
	keep      atomic.Int64             // the event log's retention, a time.Duration
	level     *slog.LevelVar           // the process log's
	providers atomic.Pointer[map[string]llm.Provider]

	recorder otogi.Recorder // what providers are built with
	log      *slog.Logger
}

// newSystem returns a system with nothing set: loadSections sets it.
func newSystem(level *slog.LevelVar, recorder otogi.Recorder, log *slog.Logger) *system {
	return &system{level: level, recorder: recorder, log: log}
}

func (y *system) operatorList() []string { return *y.operators.Load() }

func (y *system) retention() time.Duration { return time.Duration(y.keep.Load()) }

// generalSettings is the system:general section.
type generalSettings struct {
	Operators         []string       `json:"operators,omitempty" doc:"Who may use operator commands such as /status and /todou link: platform-qualified actor IDs as /whoami prints them, for example telegram:user:123456789."`
	CommandPrefixes   []string       `json:"command_prefixes" required:"false" default:"[\"/\"]" doc:"What starts a command: with /, the message \"/name args\" invokes a module's command. An empty list turns commands off."`
	EventlogRetention otogi.Duration `json:"eventlog_retention,omitempty" default:"720h" doc:"How long event log records are kept, and chat history with them; 720h is 30 days."`
	LogLevel          string         `json:"log_level,omitempty" enum:"debug,info,warn,error" default:"info" doc:"The least severe lines the process log has."`
}

func (y *system) applyGeneral(c generalSettings, _ func(string) (string, bool)) (func(), error) {
	var errs []error
	for _, op := range c.Operators {
		if platform, actor, ok := strings.Cut(op, ":"); !ok || platform == "" || actor == "" {
			errs = append(errs, fmt.Errorf("operators: %q: want <platform>:<actor id>, as /whoami prints it", op))
		}
	}
	if slices.Contains(c.CommandPrefixes, "") {
		errs = append(errs, errors.New("command_prefixes: a prefix must not be empty"))
	}
	level, err := logLevel(cmp.Or(c.LogLevel, "info"))
	if err != nil {
		errs = append(errs, fmt.Errorf("log_level %w", err))
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("%w: %w", errors.Join(errs...), otogi.ErrInvalid)
	}
	if c.CommandPrefixes == nil {
		c.CommandPrefixes = []string{"/"}
	}
	y.operators.Store(&c.Operators)
	y.prefixes.Store(&c.CommandPrefixes)
	y.keep.Store(int64(cmp.Or(time.Duration(c.EventlogRetention), 30*24*time.Hour)))
	y.level.Set(level)
	return func() {}, nil // nothing that follows these can fail
}

func generalFromFile(cfg Config) ([]string, any, map[string]string) {
	return cfg.moved.general, generalSettings{
		Operators: cfg.Operators, CommandPrefixes: cfg.Commands.Prefixes,
		EventlogRetention: otogi.Duration(cfg.EventLog.Retention), LogLevel: strings.ToLower(cfg.Log.Level),
	}, nil
}

// llmFromFile keeps each key as the file writes it: a ${NAME} stays that
// reference, and a key spelt out is moved to a secret the settings refer to.
func llmFromFile(cfg Config) ([]string, any, map[string]string) {
	written := cfg.moved.llm
	if written == nil {
		return nil, nil, nil
	}
	literal := map[string]string{}
	return []string{"llm"}, cfg.LLM.settings(func(provider, key string) otogi.Secret {
		if ref := written.apiKey(provider); otogi.SecretReference.MatchString(ref) {
			return otogi.Secret(ref)
		}
		name := "OTOGI_LLM_" + strings.ToUpper(strings.ReplaceAll(provider, "-", "_")) + "_API_KEY"
		literal[name] = key
		return otogi.Secret("${" + name + "}")
	}), literal
}

// loadSections applies every section's current revision, before the modules
// are built. A section with no revision takes its first from the keys
// otogi.yaml has for it, saved by "otogi.yaml"; without those it runs on its
// defaults. Once a section has a revision the file's keys for it no longer
// apply, which one warning says.
func (s *moduleSettings) loadSections(ctx context.Context, cfg Config) error {
	stored, err := s.storedSecrets(ctx)
	if err != nil {
		return err
	}
	var ignored []string
	for _, id := range slices.Sorted(maps.Keys(sections)) {
		sec := sections[id]
		cur, err := s.current(ctx, id)
		if err != nil {
			return err
		}
		keys, settings, literal := sec.file(cfg)
		imported := cur.Revision == 0 && len(keys) > 0
		if imported {
			data, err := json.Marshal(settings)
			if err != nil {
				return fmt.Errorf("%s from the config file: %w", id, err)
			}
			cur = modulestore.SettingsRevision{Module: id, Revision: 1, Settings: data, By: fileActor, At: time.Now()}
			maps.Copy(stored, literal)
		} else {
			ignored = append(ignored, keys...)
		}
		if _, err := sec.apply(s.system, cur.Settings, s.environmentWith(stored).secret); err != nil {
			return fmt.Errorf("%s, settings revision %d: %w", id, cur.Revision, err)
		}
		if !imported {
			continue
		}
		for _, name := range slices.Sorted(maps.Keys(literal)) {
			if err := s.store.SetSecret(ctx, modulestore.Secret{Name: name, Value: literal[name], By: fileActor, At: cur.At}); err != nil {
				return err
			}
			s.secretChanged(ctx, cur.At, name, "set", fileActor, []otogi.ModuleID{})
		}
		if err := s.store.AddSettings(ctx, cur); err != nil {
			return err
		}
		s.reconfigured(ctx, cur, []otogi.ModuleID{})
	}
	if len(ignored) > 0 {
		s.log.Warn("the config file has settings that are now edited in the control panel: the file's no longer apply, and can be removed from it", "keys", ignored)
	}
	return nil
}

// applySection validates a section's settings against their schema, puts
// them into effect, and rebuilds the running modules the section says need
// it, with the modules holding their services. If those do not build or
// start, what there was is put back. It returns the modules restarted.
func (s *moduleSettings) applySection(ctx context.Context, id otogi.ModuleID, sec section, settings []byte, env moduleEnv) ([]otogi.ModuleID, error) {
	if err := sec.check(settings); err != nil {
		return nil, err
	}
	undo, err := sec.apply(s.system, settings, env.secret)
	if err != nil {
		return nil, err
	}
	if sec.rebuilds == nil {
		return []otogi.ModuleID{}, nil
	}
	restarted, err := s.restart(ctx, env, func(module otogi.ModuleID) bool { return sec.rebuilds(s, module) }, "the "+string(id)+" settings")
	if err != nil {
		undo()
		return nil, err
	}
	return restarted, nil
}
