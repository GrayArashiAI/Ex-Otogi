package openai

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/markdown"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// call is the state of one Generate: the model's turn, rebuilt from the
// stream's chunks, and what is needed to record it.
type call struct {
	name     string
	started  time.Time
	partial  func(otogi.Message) error
	attempts []*attempt

	// Per attempt; see reset.
	content     strings.Builder
	reasoning   strings.Builder
	tools       map[int]*toolCall
	serverCalls json.RawMessage
	annotations []annotation
	events      []json.RawMessage
	finish      string
	usage       llm.Usage
	serverUse   json.RawMessage

	// Across attempts: until the first output of the first attempt that had
	// any.
	firstOutput time.Duration
}

// attempt is one HTTP request of a call, for the record. Request headers
// are left out: they carry the API key.
type attempt struct {
	Started         time.Time   `json:"started"`
	Status          int         `json:"status,omitempty"`
	Headers         string      `json:"headers,omitempty"` // until the response headers
	Done            string      `json:"done,omitempty"`    // until the stream ended
	ResponseHeaders http.Header `json:"response_headers,omitempty"`
	Error           string      `json:"error,omitempty"`
	Body            string      `json:"body,omitempty"` // a failing response's body
	RetryAfter      string      `json:"retry_after,omitempty"`
	Wait            string      `json:"wait,omitempty"` // waited before the next attempt

	started time.Time
}

// since is the time since started, to the millisecond.
func since(started time.Time) string { return time.Since(started).Round(time.Millisecond).String() }

// begin starts the record of a new attempt.
func (c *call) begin() *attempt {
	now := time.Now()
	a := &attempt{Started: now, started: now}
	c.attempts = append(c.attempts, a)
	return a
}

// waited notes the delay before the next attempt.
func (c *call) waited(d time.Duration) {
	if len(c.attempts) > 0 {
		c.attempts[len(c.attempts)-1].Wait = d.String()
	}
}

// reset clears what an attempt builds, before the next.
func (c *call) reset() {
	c.content.Reset()
	c.reasoning.Reset()
	c.tools = map[int]*toolCall{}
	c.serverCalls, c.annotations, c.events, c.finish, c.usage, c.serverUse = nil, nil, nil, "", llm.Usage{}, nil
}

// toolCall is a function call, built from its streamed fragments.
type toolCall struct {
	ID        string
	Name      string
	Arguments strings.Builder
}

// chunk is one streamed chunk.
type chunk struct {
	Choices []struct {
		Delta struct {
			Content          *string `json:"content"`
			ReasoningContent *string `json:"reasoning_content"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
			ServerToolCalls json.RawMessage `json:"server_tool_calls"`
			Annotations     []annotation    `json:"annotations"`
		} `json:"delta"`
		ServerToolEvent json.RawMessage `json:"server_tool_event"`
		FinishReason    *string         `json:"finish_reason"`
	} `json:"choices"`
	Usage *usage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// annotation marks text the reply cites.
type annotation struct {
	Type        string `json:"type"`
	URLCitation struct {
		URL   string `json:"url"`
		Title string `json:"title"`
	} `json:"url_citation"`
}

type usage struct {
	PromptTokens        int  `json:"prompt_tokens"`
	CompletionTokens    int  `json:"completion_tokens"`
	CacheHitTokens      *int `json:"prompt_cache_hit_tokens"`
	PromptTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
	ServerToolUse json.RawMessage `json:"server_tool_use"`
}

// llmUsage counts tokens as llm.Usage does: completion tokens include the
// thinking, which Thoughts counts apart.
func (u usage) llmUsage() llm.Usage {
	cached := u.PromptTokensDetails.CachedTokens
	if u.CacheHitTokens != nil {
		cached = *u.CacheHitTokens
	}
	thoughts := u.CompletionTokensDetails.ReasoningTokens
	return llm.Usage{Input: u.PromptTokens, Cached: cached, Output: u.CompletionTokens - thoughts, Thoughts: thoughts}
}

// maxLine bounds one line of the stream: the last chunk carries the pages a
// server tool read, signed, often hundreds of kilobytes.
const maxLine = 64 << 20

// read reads a stream to its end. A stream that ends before a finish reason,
// or carries an error, fails as unavailable, which the caller retries.
func (c *call) read(body io.Reader) error {
	lines := bufio.NewScanner(body)
	lines.Buffer(make([]byte, 64<<10), maxLine)
	for lines.Scan() {
		line := lines.Text()
		data, ok := strings.CutPrefix(line, "data:")
		if !ok { // blank lines, and comments such as ": keep-alive"
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			break
		}
		var ch chunk
		if err := json.Unmarshal([]byte(data), &ch); err != nil {
			return fmt.Errorf("openai %s: chunk: %w: %w", c.name, otogi.ErrUnavailable, err)
		}
		if err := c.apply(ch); err != nil {
			return err
		}
	}
	if err := lines.Err(); err != nil {
		return fmt.Errorf("openai %s: read stream: %w: %w", c.name, otogi.ErrUnavailable, err)
	}
	switch c.finish {
	case "":
		return fmt.Errorf("openai %s: the stream ended before the reply finished: %w", c.name, otogi.ErrUnavailable)
	case "insufficient_system_resource":
		return fmt.Errorf("openai %s: finish reason %s: %w", c.name, c.finish, otogi.ErrUnavailable)
	}
	return nil
}

// apply applies one chunk.
func (c *call) apply(ch chunk) error {
	if ch.Error != nil {
		return fmt.Errorf("openai %s: stream error: %s: %w", c.name, ch.Error.Message, otogi.ErrUnavailable)
	}
	if ch.Usage != nil {
		c.usage, c.serverUse = ch.Usage.llmUsage(), ch.Usage.ServerToolUse
	}
	text := false
	for _, choice := range ch.Choices {
		d := choice.Delta
		if d.ReasoningContent != nil {
			c.reasoning.WriteString(*d.ReasoningContent)
		}
		if d.Content != nil && *d.Content != "" {
			c.content.WriteString(*d.Content)
			text = true
		}
		for _, tc := range d.ToolCalls {
			t, ok := c.tools[tc.Index]
			if !ok {
				t = &toolCall{}
				c.tools[tc.Index] = t
			}
			if tc.ID != "" {
				t.ID = tc.ID
			}
			if tc.Function.Name != "" {
				t.Name = tc.Function.Name
			}
			t.Arguments.WriteString(tc.Function.Arguments)
		}
		if len(d.ServerToolCalls) > 0 && string(d.ServerToolCalls) != "null" {
			c.serverCalls = d.ServerToolCalls
		}
		c.annotations = append(c.annotations, d.Annotations...)
		if len(choice.ServerToolEvent) > 0 && string(choice.ServerToolEvent) != "null" {
			c.events = append(c.events, choice.ServerToolEvent)
		}
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			c.finish = *choice.FinishReason
		}
	}
	if !text {
		return nil
	}
	if c.firstOutput == 0 {
		c.firstOutput = time.Since(c.started)
	}
	if c.partial == nil {
		return nil
	}
	if err := c.partial(reply(c.content.String())); err != nil {
		return fmt.Errorf("openai %s: %w: %w", c.name, errStopped, err)
	}
	return nil
}

// reply is Markdown text as a message: one rich text part, or none when the
// text is blank.
func reply(md string) otogi.Message {
	if strings.TrimSpace(md) == "" {
		return otogi.Message{}
	}
	rich := markdown.Render(md)
	return otogi.Message{Roots: []otogi.PartID{"p0"}, Parts: []otogi.Part{{ID: "p0", Kind: otogi.PartText, Text: rich.String(), Annotations: rich.Annotations()}}}
}

// assistant is the model's turn as the assistant message the service sent,
// kept as llm.Native and resent as it is.
type assistant struct {
	Content          string          `json:"content"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	ToolCalls        []wireCall      `json:"tool_calls,omitempty"`
	ServerToolCalls  json.RawMessage `json:"server_tool_calls,omitempty"`
}

type wireCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// response is the finished turn.
func (c *call) response() llm.Response {
	md := c.content.String()
	native := assistant{Content: md, ReasoningContent: c.reasoning.String(), ServerToolCalls: c.serverCalls}
	resp := llm.Response{Markdown: md, Usage: c.usage, Finish: finishOf(c.finish)}
	indexes := make([]int, 0, len(c.tools))
	for i := range c.tools {
		indexes = append(indexes, i)
	}
	sort.Ints(indexes)
	for _, i := range indexes {
		t := c.tools[i]
		w := wireCall{ID: t.ID, Type: "function"}
		w.Function.Name, w.Function.Arguments = t.Name, t.Arguments.String()
		native.ToolCalls = append(native.ToolCalls, w)
		args := json.RawMessage(w.Function.Arguments)
		if !json.Valid(args) { // kept as a string, so the model sees its own malformed call
			args, _ = json.Marshal(w.Function.Arguments)
		}
		resp.Turn.Calls = append(resp.Turn.Calls, llm.Call{ID: t.ID, Name: t.Name, Args: args})
	}
	data, _ := json.Marshal(native)
	resp.Turn.Role, resp.Turn.Content, resp.Turn.Native = llm.RoleModel, reply(md), &llm.Native{Provider: c.name, Data: data}
	seen := map[string]bool{}
	for _, a := range c.annotations {
		if a.Type != "url_citation" || a.URLCitation.URL == "" || seen[a.URLCitation.URL] {
			continue
		}
		seen[a.URLCitation.URL] = true
		resp.Sources = append(resp.Sources, llm.Source{Title: a.URLCitation.Title, URL: a.URLCitation.URL})
	}
	if len(resp.Turn.Calls) > 0 {
		resp.Finish = llm.FinishToolCalls
	}
	if resp.Finish == llm.FinishBlocked || resp.Finish == llm.FinishOther {
		resp.Reason = c.finish
	}
	return resp
}

// finishOf maps a finish reason to a finish.
func finishOf(reason string) llm.Finish {
	switch reason {
	case "stop":
		return llm.FinishStop
	case "length":
		return llm.FinishMaxTokens
	case "tool_calls":
		return llm.FinishToolCalls
	case "content_filter":
		return llm.FinishBlocked
	default:
		return llm.FinishOther
	}
}
