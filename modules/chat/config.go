package chat

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Config is the module's settings.
type Config struct {
	Retention otogi.Duration  `json:"retention,omitempty" doc:"How long threads and the chat transcript are kept; default 720h (30 days)."`
	Timeout   otogi.Duration  `json:"timeout,omitempty" doc:"The longest one reply may take, tool calls included; default 5m. When less is left than two of its slowest model rounds, the agent stops calling tools and answers with what it has found."`
	Profiles  []ProfileConfig `json:"profiles" minItems:"1" doc:"The agents. Each answers in the chats on its allowlist."`
	Tools     []string        `json:"tools,omitempty" doc:"Modules whose tools agents may use, by module ID (notes, reminders, todou). Each has a list of the chats where agents get its tools; a chat on none of them gets none."`
}

// ProfileConfig configures one agent.
type ProfileConfig struct {
	Name          string   `json:"name" minLength:"1" doc:"Identifies the agent and names its allowlist. Its threads are stored under it: keep it stable."`
	Label         string   `json:"label,omitempty" doc:"When set, starts each reply in bold."`
	Keywords      []string `json:"keywords,omitempty" doc:"A message starting with one of them triggers the agent; so does replying to it."`
	Mention       bool     `json:"mention,omitempty" doc:"A message mentioning the bot triggers the agent too, as people address a bot in a QQ group. When several agents in a chat set it, the first answers."`
	Provider      string   `json:"provider" minLength:"1" doc:"A language model provider configured under llm in otogi.yaml, such as gemini."`
	Model         string   `json:"model" minLength:"1" doc:"The provider's model ID."`
	Thinking      string   `json:"thinking,omitempty" enum:"minimal,low,medium,high" doc:"How much the model thinks before answering; unset is the model's default."`
	MaxOutput     int      `json:"max_output,omitempty" minimum:"0" doc:"The most tokens a reply may have; 0 is the model's default."`
	Temperature   *float64 `json:"temperature,omitempty" minimum:"0" maximum:"2" doc:"Sampling temperature, 0 to 2; unset is the model's default (1 for Gemini). Higher is livelier, lower more predictable."`
	System        string   `json:"system,omitempty" format:"markdown" doc:"The system prompt: who the agent is and how it talks, in Markdown."`
	Context       *int     `json:"context,omitempty" minimum:"0" doc:"How many recent chat messages the agent sees; default 30, 0 for none."`
	MaxTurns      int      `json:"max_turns,omitempty" minimum:"0" doc:"How many turns of a thread are sent, newest kept; default 40."`
	Tools         []string `json:"tools,omitempty" enum:"google_search,url_context,code_execution" doc:"The model's built-in tools the agent uses everywhere it answers. Modules' tools are offered per chat: see Tools, after the profiles."`
	MaxToolRounds int      `json:"max_tool_rounds,omitempty" minimum:"0" doc:"The most rounds of tool calls in one reply; default 8."`
}

// profile is a validated agent.
type profile struct {
	ProfileConfig
	keywords []string
	context  int
	builtins []llm.ToolSpec
	provider llm.Provider
}

// toolsList names the allowlist of the chats where agents get a module's
// tools. Profile lists are named as their profiles, which may not start
// so.
func toolsList(module otogi.ModuleID) string { return "tools:" + string(module) }

// toolModules validates the modules whose tools agents may use.
func (c *Config) toolModules() ([]otogi.ModuleID, error) {
	var out []otogi.ModuleID
	var errs []error
	for _, t := range c.Tools {
		switch {
		case slices.Contains(builtinTools, t):
			errs = append(errs, fmt.Errorf("tools: %q is a built-in tool: set it on the profiles that use it", t))
		case t == "" || strings.ContainsAny(t, " :/"):
			errs = append(errs, fmt.Errorf("tools: %q: want a module ID", t))
		case slices.Contains(out, otogi.ModuleID(t)):
			errs = append(errs, fmt.Errorf("tools: %q is listed twice", t))
		default:
			out = append(out, otogi.ModuleID(t))
		}
	}
	return out, errors.Join(errs...)
}

var builtinTools = []string{llm.BuiltinGoogleSearch, llm.BuiltinURLContext, llm.BuiltinCodeExecution}

// profiles validates the configuration and resolves providers.
func (c *Config) profiles(providers func(string) (llm.Provider, error)) ([]*profile, error) {
	if c.Retention == 0 {
		c.Retention = otogi.Duration(30 * 24 * time.Hour)
	}
	if c.Timeout == 0 {
		c.Timeout = otogi.Duration(5 * time.Minute)
	}
	var errs []error
	if c.Retention < 0 || c.Timeout < 0 {
		errs = append(errs, errors.New("retention and timeout must not be negative"))
	}
	if len(c.Profiles) == 0 {
		errs = append(errs, errors.New("profiles: at least one is required"))
	}
	var out []*profile
	names, keywords := map[string]bool{}, map[string]string{}
	for i, pc := range c.Profiles {
		field := fmt.Sprintf("profiles[%d]", i)
		p := &profile{ProfileConfig: pc, context: 30}
		if pc.Context != nil {
			p.context = *pc.Context
		}
		if p.MaxTurns == 0 {
			p.MaxTurns = 40
		}
		if p.MaxToolRounds == 0 {
			p.MaxToolRounds = 8
		}
		switch {
		case pc.Name == "":
			errs = append(errs, fmt.Errorf("%s.name is required", field))
		case strings.HasPrefix(pc.Name, "tools:"):
			errs = append(errs, fmt.Errorf("%s.name %q: names starting with tools: are the lists of modules' tools", field, pc.Name))
		case names[pc.Name]:
			errs = append(errs, fmt.Errorf("%s.name %q is used twice", field, pc.Name))
		}
		names[pc.Name] = true
		if len(pc.Keywords) == 0 {
			errs = append(errs, fmt.Errorf("%s.keywords: at least one is required", field))
		}
		for _, k := range pc.Keywords {
			k = strings.TrimSpace(k)
			folded := strings.ToLower(k)
			switch {
			case k == "":
				errs = append(errs, fmt.Errorf("%s.keywords: empty keyword", field))
			case keywords[folded] != "" && keywords[folded] != pc.Name:
				errs = append(errs, fmt.Errorf("%s.keywords: %q also belongs to profile %q", field, k, keywords[folded]))
			}
			keywords[folded] = pc.Name
			p.keywords = append(p.keywords, k)
		}
		if pc.Model == "" {
			errs = append(errs, fmt.Errorf("%s.model is required", field))
		}
		if pc.Temperature != nil && (*pc.Temperature < 0 || *pc.Temperature > 2) {
			errs = append(errs, fmt.Errorf("%s.temperature %v: want 0 to 2", field, *pc.Temperature))
		}
		if !slices.Contains([]string{"", "minimal", "low", "medium", "high"}, pc.Thinking) {
			errs = append(errs, fmt.Errorf("%s.thinking %q: want minimal, low, medium, or high", field, pc.Thinking))
		}
		if p.context < 0 || p.MaxTurns < 0 || p.MaxOutput < 0 || p.MaxToolRounds < 0 {
			errs = append(errs, fmt.Errorf("%s: context, max_turns, max_output, and max_tool_rounds must not be negative", field))
		}
		for _, t := range pc.Tools {
			if !slices.Contains(builtinTools, t) {
				errs = append(errs, fmt.Errorf("%s.tools: %q: want a built-in (%s); modules' tools are set in tools, per chat", field, t, strings.Join(builtinTools, ", ")))
				continue
			}
			p.builtins = append(p.builtins, llm.ToolSpec{Builtin: t})
		}
		provider, err := providers(pc.Provider)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s.provider: %w", field, err))
		}
		p.provider = provider
		out = append(out, p)
	}
	return out, errors.Join(errs...)
}

// keyword reports whether text starts with one of the profile's keywords: as
// a word of its own, or directly followed by anything when the keyword ends
// in a CJK character, which needs no space.
func (p *profile) keyword(text string) bool {
	text = strings.TrimLeftFunc(text, unicode.IsSpace)
	for _, k := range p.keywords {
		n := utf8.RuneCountInString(k)
		end := 0
		for i := 0; i < n && end < len(text); i++ {
			_, size := utf8.DecodeRuneInString(text[end:])
			end += size
		}
		if !strings.EqualFold(text[:end], k) {
			continue
		}
		next, _ := utf8.DecodeRuneInString(text[end:])
		last, _ := utf8.DecodeLastRuneInString(k)
		if end == len(text) || unicode.IsSpace(next) || unicode.IsPunct(next) || unicode.IsSymbol(next) || cjk(last) {
			return true
		}
	}
	return false
}

func cjk(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}
