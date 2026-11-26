package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Sections are the settings outside modules that are edited in the panel.
// Each has a Config type, as a module has, and its settings are saved,
// validated, kept as revisions, and restored by the code that does so for
// modules (moduleSettings.save), under an ID starting with "system:", which
// no module's ID can be. What differs is applying them: a section's settings
// go to the system, where the process reads them each time it uses them, or,
// for those the process reads once (see accounts.go), wait for its next
// start.

const (
	sectionPrefix = "system:"

	generalSection  otogi.ModuleID = sectionPrefix + "general"
	llmSection      otogi.ModuleID = sectionPrefix + "llm"
	accountsSection otogi.ModuleID = sectionPrefix + "accounts"
	processSection  otogi.ModuleID = sectionPrefix + "process"
)

// section is one group of settings outside modules.
type section struct {
	settingsSchema
	// apply validates settings and puts them into effect. undo puts back
	// what there was, for when the modules do not build with them.
	apply func(sys *system, settings []byte, secret func(name string) (string, bool)) (undo func(), err error)
	// rebuilds picks the running modules to rebuild once the settings are
	// applied; nil when modules read the settings each time they use them.
	rebuilds func(s *moduleSettings, module otogi.ModuleID) bool
	// onRestart marks settings the process reads when it starts: apply only
	// validates them, and a saved revision waits for the next start.
	onRestart bool
}

var sections = map[otogi.ModuleID]section{
	generalSection:  sectionOf((*system).applyGeneral, nil),
	llmSection:      sectionOf((*system).applyLLM, (*moduleSettings).builtWithProvider),
	accountsSection: onRestart(sectionOf(checkAccounts, nil)).keeping(accountNames...),
	processSection:  onRestart(sectionOf(checkProcess, nil)),
}

func onRestart(sec section) section {
	sec.onRestart = true
	return sec
}

// keeping gives a section's keyed lists: see keyedList.
func (sec section) keeping(lists ...keyedList) section {
	sec.keys = lists
	return sec
}

func sectionOf[C any](
	apply func(*system, C, func(string) (string, bool)) (func(), error),
	rebuilds func(*moduleSettings, otogi.ModuleID) bool,
) section {
	return section{
		settingsSchema: schemaOf[C](), rebuilds: rebuilds,
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
	Operators         []string       `json:"operators,omitempty" format:"actor" doc:"Who may use operator commands such as /status and /todou link: platform-qualified actor IDs as /whoami prints them, for example telegram:user:123456789."`
	CommandPrefixes   []string       `json:"command_prefixes" required:"false" default:"[\"/\"]" doc:"What starts a command: with /, the message \"/name args\" invokes a module's command. An empty list turns commands off."`
	EventlogRetention otogi.Duration `json:"eventlog_retention,omitempty" default:"720h" doc:"How long event log records are kept, and chat history with them."`
	LogLevel          string         `json:"log_level,omitempty" enum:"debug,info,warn,error" default:"info" doc:"The least severe lines the process log has."`
}

func (y *system) applyGeneral(c generalSettings, _ func(string) (string, bool)) (func(), error) {
	var errs []error
	for i, op := range c.Operators {
		if platform, actor, ok := strings.Cut(op, ":"); !ok || platform == "" || actor == "" {
			errs = append(errs, &otogi.SettingError{Path: fmt.Sprintf("operators[%d]", i), Err: fmt.Errorf("%q: want <platform>:<actor id>, as /whoami prints it", op)})
		}
	}
	for i, prefix := range c.CommandPrefixes {
		if prefix == "" {
			errs = append(errs, &otogi.SettingError{Path: fmt.Sprintf("command_prefixes[%d]", i), Err: errors.New("a prefix must not be empty")})
		}
	}
	level, err := logLevel(cmp.Or(c.LogLevel, "info"))
	if err != nil {
		errs = append(errs, &otogi.SettingError{Path: "log_level", Err: err})
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
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

// loadSections applies every section's current revision, before the modules
// are built; a section with no revision runs on its defaults. The revisions
// applied are kept as those the process started with.
func (s *moduleSettings) loadSections(ctx context.Context) error {
	stored, err := s.storedSecrets(ctx)
	if err != nil {
		return err
	}
	s.started = map[otogi.ModuleID]int64{}
	for _, id := range slices.Sorted(maps.Keys(sections)) {
		cur, err := s.current(ctx, id)
		if err != nil {
			return err
		}
		if _, err := sections[id].apply(s.system, cur.Settings, s.environmentWith(stored).secret); err != nil {
			return fmt.Errorf("%s, settings revision %d: %w", id, cur.Revision, err)
		}
		s.started[id] = cur.Revision
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
