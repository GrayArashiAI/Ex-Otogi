package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type recorder struct {
	mu      sync.Mutex
	records []otogi.Record
}

func (r *recorder) Record(_ context.Context, rec otogi.Record) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, rec)
}

func (r *recorder) last(t *testing.T) otogi.Record {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.records) == 0 {
		t.Fatal("no records")
	}
	return r.records[len(r.records)-1]
}

// payload is a record's payload as this provider writes it.
func payload(t *testing.T, r otogi.Record) map[string]any {
	t.Helper()
	p, ok := r.Payload.(map[string]any)
	if !ok {
		t.Fatalf("payload is %T", r.Payload)
	}
	return p
}

// server is a fake Chat Completions endpoint answering each request with
// the next response, and keeping the request bodies.
type server struct {
	mu        sync.Mutex
	bodies    []map[string]any
	responses []func(w http.ResponseWriter, r *http.Request)
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	s.mu.Lock()
	s.bodies = append(s.bodies, body)
	n := len(s.bodies)
	s.mu.Unlock()
	if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer key" {
		http.Error(w, "unexpected "+r.URL.Path, http.StatusNotFound)
		return
	}
	s.responses[min(n, len(s.responses))-1](w, r)
}

func (s *server) requests() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bodies
}

// fixture answers with a stream recorded from DeepSeek (testdata, trimmed).
func fixture(t *testing.T, name string) func(http.ResponseWriter, *http.Request) {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name + ".sse")
	if err != nil {
		t.Fatal(err)
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(data)
	}
}

// stream answers with the given chunks as server-sent events, then [DONE]
// unless the last chunk is "-", which cuts the stream instead.
func stream(chunks ...string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, ": keep-alive\n\n")
		for i, c := range chunks {
			if c == "-" && i == len(chunks)-1 {
				return
			}
			_, _ = io.WriteString(w, "data: "+c+"\n\n")
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}
}

func status(code int, body string, header ...string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		for i := 0; i+1 < len(header); i += 2 {
			w.Header().Set(header[i], header[i+1])
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = io.WriteString(w, body)
	}
}

// text is a chunk of reply text; finished is the last chunk of a reply.
func text(s string) string {
	data, _ := json.Marshal(s)
	return `{"choices":[{"index":0,"delta":{"content":` + string(data) + `},"finish_reason":null}],"usage":null}`
}

func finished(reason string) string {
	return `{"choices":[{"index":0,"delta":{"content":""},"finish_reason":"` + reason + `"}],"usage":{"prompt_tokens":5,"completion_tokens":3,"completion_tokens_details":{"reasoning_tokens":1},"prompt_cache_hit_tokens":2}}`
}

func setup(t *testing.T, responses ...func(http.ResponseWriter, *http.Request)) (*Provider, *server, *recorder) {
	t.Helper()
	srv := &server{responses: responses}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	rec := &recorder{}
	p, err := New(Config{Name: "deepseek", BaseURL: ts.URL + "/", APIKey: "key", Recorder: rec, Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	p.backoff = time.Millisecond
	return p, srv, rec
}

func userText(s string) llm.Turn {
	return llm.Turn{Role: llm.RoleUser, Content: otogi.TextMessage(s)}
}

// asJSON is v re-read as generic JSON, for comparing with request bodies.
func asJSON(t *testing.T, v any) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestGenerateText(t *testing.T) {
	p, srv, rec := setup(t, fixture(t, "text"))
	temperature := 0.5
	var partials []string
	resp, err := p.Generate(context.Background(), llm.Request{
		Model: "deepseek-flash", System: "Answer briefly.", Thinking: "low", MaxOutput: 200, Temperature: &temperature,
		Turns: []llm.Turn{userText("What colour is the sky?")},
	}, func(m otogi.Message) error {
		partials = append(partials, m.PlainText())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	const want = "On a clear day, the sky is blue."
	if resp.Markdown != want || resp.Turn.Content.PlainText() != want || resp.Finish != llm.FinishStop || resp.Turn.Role != llm.RoleModel {
		t.Fatalf("response = %+v", resp)
	}
	if len(partials) == 0 || partials[len(partials)-1] != want || len(partials[0]) >= len(want) {
		t.Fatalf("partials = %q", partials)
	}
	if resp.Usage != (llm.Usage{Input: 46, Cached: 0, Output: 11, Thoughts: 25}) {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	var native map[string]any
	if resp.Turn.Native == nil || resp.Turn.Native.Provider != "deepseek" || json.Unmarshal(resp.Turn.Native.Data, &native) != nil ||
		native["content"] != want || native["reasoning_content"] != "We need answer" {
		t.Fatalf("native = %+v", resp.Turn.Native)
	}

	body := srv.requests()[0]
	for k, v := range map[string]any{"model": "deepseek-flash", "stream": true, "reasoning_effort": "low", "max_tokens": 200.0, "temperature": 0.5} {
		if body[k] != v {
			t.Errorf("%s = %v, want %v", k, body[k], v)
		}
	}
	if !reflect.DeepEqual(body["stream_options"], map[string]any{"include_usage": true}) || body["tools"] != nil {
		t.Errorf("body = %v", body)
	}
	wantMessages := []any{
		map[string]any{"role": "system", "content": "Answer briefly."},
		map[string]any{"role": "user", "content": "What colour is the sky?"},
	}
	if !reflect.DeepEqual(body["messages"], wantMessages) {
		t.Errorf("messages = %v", body["messages"])
	}

	r := rec.last(t)
	if r.Kind != "llm.completed" || r.Outcome != "ok" || r.Component != "llm:deepseek" || payload(t, r)["reasoning"] != "We need answer" || payload(t, r)["reply"] != want {
		t.Fatalf("record = %+v", r)
	}
}

// A plain request gets the model's text as it is, trimmed, in the partials
// and the reply: emoticons and asterisks are not read as Markdown. Without
// Plain the same text is.
func TestPlainText(t *testing.T) {
	const raw, want = "\n(*/ω＼*) 2*3*4\n- a\n", "(*/ω＼*) 2*3*4\n- a"
	for _, plain := range []bool{true, false} {
		p, _, _ := setup(t, stream(text(raw[:3]), text(raw[3:]), finished("stop")))
		var last otogi.Message
		resp, err := p.Generate(context.Background(), llm.Request{Model: "m", Turns: []llm.Turn{userText("hi")}, Plain: plain}, func(m otogi.Message) error {
			last = m
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		content := resp.Turn.Content
		if resp.Markdown != raw {
			t.Fatalf("plain %v: Markdown %q", plain, resp.Markdown)
		}
		asIs := content.PlainText() == want && len(content.Parts[0].Annotations) == 0 && last.PlainText() == want && len(last.Parts[0].Annotations) == 0
		if asIs != plain {
			t.Fatalf("plain %v: reply %q %+v, last partial %q", plain, content.PlainText(), content.Parts[0].Annotations, last.PlainText())
		}
	}
}

// A video the model cannot take is sent as a text mark and listed in
// Withheld; with only text and images, Withheld is empty.
func TestWithheld(t *testing.T) {
	inline := func(id otogi.PartID, mime string) otogi.Part {
		return otogi.Part{ID: id, Kind: otogi.PartMedia, Media: &otogi.Media{MediaType: mime, Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: []byte("data")}}}
	}
	for name, c := range map[string]struct {
		parts []otogi.Part
		want  []llm.WithheldPart
	}{
		"video": {[]otogi.Part{{ID: "t", Kind: otogi.PartText, Text: "what is this?"}, inline("v", "video/mp4")}, []llm.WithheldPart{{Part: "v", MediaType: "video/mp4", Bytes: 4}}},
		"image": {[]otogi.Part{{ID: "t", Kind: otogi.PartText, Text: "what is this?"}, inline("i", "image/png")}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			p, _, _ := setup(t, fixture(t, "text"))
			resp, err := p.Generate(context.Background(), llm.Request{Model: "deepseek-flash", Turns: []llm.Turn{{Role: llm.RoleUser, Content: otogi.Message{Parts: c.parts}}}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(resp.Withheld, c.want) {
				t.Fatalf("withheld = %+v, want %+v", resp.Withheld, c.want)
			}
		})
	}
}

// A function call streams in fragments; the next turn sends the assistant
// message back as it came, reasoning included, and the results as tool
// messages.
func TestToolCallRoundTrip(t *testing.T) {
	p, srv, _ := setup(t, fixture(t, "tool"), fixture(t, "text"))
	reminder := llm.ToolSpec{Function: &llm.Function{Name: "set_reminder", Description: "Set a reminder.", Parameters: json.RawMessage(`{"type":"object"}`)}}
	ask := userText("Remind me to drink water in 10 minutes.")
	resp, err := p.Generate(context.Background(), llm.Request{Model: "deepseek-flash", Turns: []llm.Turn{ask}, Tools: []llm.ToolSpec{reminder}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	const id = "call_00_zQgJLoTWd2I4U5loRlxy2387"
	wantCalls := []llm.Call{{ID: id, Name: "set_reminder", Args: json.RawMessage(`{"in": "10m", "text": "Drink water"}`)}}
	if resp.Finish != llm.FinishToolCalls || !reflect.DeepEqual(resp.Turn.Calls, wantCalls) || len(resp.Turn.Content.Parts) != 0 {
		t.Fatalf("response = %+v", resp)
	}
	tools := srv.requests()[0]["tools"]
	if !reflect.DeepEqual(tools, []any{map[string]any{"type": "function", "function": map[string]any{"name": "set_reminder", "description": "Set a reminder.", "parameters": map[string]any{"type": "object"}}}}) {
		t.Fatalf("tools = %v", tools)
	}

	result := llm.Turn{Role: llm.RoleTool, Results: []llm.Result{{CallID: id, Name: "set_reminder", Output: json.RawMessage(`{"at":"16:40"}`)}}}
	if _, err := p.Generate(context.Background(), llm.Request{Model: "deepseek-flash", Turns: []llm.Turn{ask, resp.Turn, result}, Tools: []llm.ToolSpec{reminder}}, nil); err != nil {
		t.Fatal(err)
	}
	messages, _ := srv.requests()[1]["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("messages = %v", messages)
	}
	assistant, _ := messages[1].(map[string]any)
	calls, _ := assistant["tool_calls"].([]any)
	if assistant["role"] != "assistant" || assistant["content"] != "" || !strings.HasPrefix(assistant["reasoning_content"].(string), "The user wants") || len(calls) != 1 ||
		!reflect.DeepEqual(calls[0], map[string]any{"id": id, "type": "function", "function": map[string]any{"name": "set_reminder", "arguments": `{"in": "10m", "text": "Drink water"}`}}) {
		t.Fatalf("assistant = %v", assistant)
	}
	if !reflect.DeepEqual(messages[2], map[string]any{"role": "tool", "tool_call_id": id, "content": `{"at":"16:40"}`}) {
		t.Fatalf("tool = %v", messages[2])
	}
}

// DeepSeek's web search runs on the server: the reply cites its pages, the
// steps stay in Native, and the progress and the tool use go to the record.
func TestWebSearch(t *testing.T) {
	p, srv, rec := setup(t, fixture(t, "search"))
	reminder := llm.ToolSpec{Function: &llm.Function{Name: "set_reminder", Description: "d"}}
	resp, err := p.Generate(context.Background(), llm.Request{Model: "deepseek-flash", Turns: []llm.Turn{userText("Latest Go?")}, Tools: []llm.ToolSpec{{Builtin: llm.BuiltinWebSearch}, reminder}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tools, _ := srv.requests()[0]["tools"].([]any)
	if len(tools) != 2 || !reflect.DeepEqual(tools[0], map[string]any{"type": "deepseek_web_search"}) {
		t.Fatalf("tools = %v", tools)
	}
	wantSources := []llm.Source{{Title: "Release History", URL: "https://go.dev/doc/devel/release?me=130Wiki&where=CaelLabWiki"}}
	if !reflect.DeepEqual(resp.Sources, wantSources) || !strings.HasPrefix(resp.Turn.Content.PlainText(), "The latest stable Go release is Go 1.27.4") {
		t.Fatalf("response = %+v", resp)
	}
	if resp.Usage != (llm.Usage{Input: 18932, Cached: 11520, Output: 226, Thoughts: 374}) {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	var native struct {
		ServerToolCalls []any `json:"server_tool_calls"`
	}
	if err := json.Unmarshal(resp.Turn.Native.Data, &native); err != nil || len(native.ServerToolCalls) != 3 {
		t.Fatalf("native = %s", resp.Turn.Native.Data)
	}
	r := rec.last(t)
	events, _ := payload(t, r)["server_tool_events"].([]any)
	use, _ := payload(t, r)["server_tool_use"].(json.RawMessage)
	if len(events) != 9 || !strings.Contains(string(use), `"tool_search":4`) {
		t.Fatalf("record = %v", payload(t, r))
	}
}

// A page DeepSeek read comes back signed; the next turn sends the signature
// back so the model still has the page, and records keep only its digest.
func TestReadURL(t *testing.T) {
	p, srv, rec := setup(t, fixture(t, "readurl"), fixture(t, "text"))
	read := userText("Read https://go.dev/doc/devel/release")
	resp, err := p.Generate(context.Background(), llm.Request{Model: "deepseek-flash", Turns: []llm.Turn{read}, Tools: []llm.ToolSpec{{Builtin: llm.BuiltinURLContext}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tools := srv.requests()[0]["tools"]; !reflect.DeepEqual(tools, []any{map[string]any{"type": "deepseek_read_url"}}) {
		t.Fatalf("tools = %v", tools)
	}
	if _, err := p.Generate(context.Background(), llm.Request{Model: "deepseek-flash", Turns: []llm.Turn{read, resp.Turn, userText("Which is the newest?")}}, nil); err != nil {
		t.Fatal(err)
	}
	const signature = "b4k/SFLBGfK/3EKsVYgZRTYW1udLdP2v+qJYDp8tmZQBcg6l"
	messages, _ := srv.requests()[1]["messages"].([]any)
	sent, _ := json.Marshal(messages[1])
	if !strings.Contains(string(sent), `"signature":"`+signature+`"`) {
		t.Fatalf("assistant = %s", sent)
	}
	recorded, _ := json.Marshal(payload(t, rec.last(t))["messages"])
	if strings.Contains(string(recorded), signature) || !strings.Contains(string(recorded), `"sha256":`) {
		t.Fatalf("recorded messages = %s", recorded)
	}
}

func TestUnsupportedTool(t *testing.T) {
	p, srv, _ := setup(t, fixture(t, "text"))
	_, err := p.Generate(context.Background(), llm.Request{Model: "m", Turns: []llm.Turn{userText("hi")}, Tools: []llm.ToolSpec{{Builtin: llm.BuiltinCodeExecution}}}, nil)
	if !errors.Is(err, otogi.ErrUnsupported) || len(srv.requests()) != 0 {
		t.Fatalf("err = %v, %d requests", err, len(srv.requests()))
	}
}

// Images go as image_url parts; other media, and images in model turns,
// become text marks, listed for the record.
func TestMessages(t *testing.T) {
	p, err := New(Config{Name: "deepseek", BaseURL: "https://x", APIKey: "k", Recorder: otogi.NopRecorder{}, Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	inline := func(id, kind otogi.PartKind, mime string, data string) otogi.Part {
		return otogi.Part{ID: otogi.PartID(id), Kind: kind, Media: &otogi.Media{MediaType: mime, Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: []byte(data)}}}
	}
	user := llm.Turn{Role: llm.RoleUser, Content: otogi.Message{Parts: []otogi.Part{
		{ID: "t", Kind: otogi.PartText, Text: "look"},
		inline("i", otogi.PartMedia, "image/png", "png"),
		inline("v", otogi.PartMedia, "video/mp4", "mp4"),
		inline("f", otogi.PartFile, "application/pdf", "pdf"),
	}}}
	model := llm.Turn{Role: llm.RoleModel, Native: &llm.Native{Provider: "gemini", Data: json.RawMessage(`[]`)},
		Content: otogi.Message{Parts: []otogi.Part{{ID: "t", Kind: otogi.PartText, Text: "ok"}, inline("g", otogi.PartMedia, "image/png", "gen")}},
		Calls:   []llm.Call{{ID: "c1", Name: "add", Args: json.RawMessage(`{"x":1}`)}, {ID: "c2", Name: "add"}}}
	tool := llm.Turn{Role: llm.RoleTool, Results: []llm.Result{{CallID: "c1", Name: "add", Output: json.RawMessage(`2`)}, {CallID: "c2", Name: "add", Error: "boom"}}}
	messages, replaced, err := p.messages(llm.Request{Turns: []llm.Turn{user, model, tool}})
	if err != nil {
		t.Fatal(err)
	}
	want := []any{
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "look"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,cG5n"}},
			map[string]any{"type": "text", "text": "[video: not shown, this model takes text and images only]"},
			map[string]any{"type": "text", "text": "[file: not shown, this model takes text and images only]"},
		}},
		map[string]any{"role": "assistant", "content": "ok\n\n[image: not shown]", "tool_calls": []any{
			map[string]any{"id": "c1", "type": "function", "function": map[string]any{"name": "add", "arguments": `{"x":1}`}},
			map[string]any{"id": "c2", "type": "function", "function": map[string]any{"name": "add", "arguments": "{}"}},
		}},
		map[string]any{"role": "tool", "tool_call_id": "c1", "content": "2"},
		map[string]any{"role": "tool", "tool_call_id": "c2", "content": `{"error":"boom"}`},
	}
	if got := asJSON(t, messages); !reflect.DeepEqual(got, want) {
		t.Fatalf("messages = %v", got)
	}
	wantReplaced := []replacedPart{{Turn: 0, Part: "v", MediaType: "video/mp4", Bytes: 3}, {Turn: 0, Part: "f", MediaType: "application/pdf", Bytes: 3}, {Turn: 1, Part: "g", MediaType: "image/png", Bytes: 3}}
	if !reflect.DeepEqual(replaced, wantReplaced) {
		t.Fatalf("replaced = %+v", replaced)
	}

	for name, turn := range map[string]llm.Turn{
		"not inline": {Role: llm.RoleUser, Content: otogi.Message{Parts: []otogi.Part{{ID: "m", Kind: otogi.PartMedia, Media: &otogi.Media{Source: otogi.MediaSource{Kind: otogi.MediaSourceExternal}}}}}},
		"own native": {Role: llm.RoleModel, Native: &llm.Native{Provider: "deepseek", Data: json.RawMessage(`[`)}},
	} {
		if _, _, err := p.messages(llm.Request{Turns: []llm.Turn{turn}}); !errors.Is(err, otogi.ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
}

func TestRetries(t *testing.T) {
	ok := stream(text("fine"), finished("stop"))
	tests := map[string]struct {
		responses []func(http.ResponseWriter, *http.Request)
		requests  int
		want      error
	}{
		"rate limited":        {[]func(http.ResponseWriter, *http.Request){status(429, `{"error":{"message":"slow down"}}`), ok}, 2, nil},
		"server error":        {[]func(http.ResponseWriter, *http.Request){status(500, `{}`), ok}, 2, nil},
		"cut stream":          {[]func(http.ResponseWriter, *http.Request){stream(text("half"), "-"), ok}, 2, nil},
		"error chunk":         {[]func(http.ResponseWriter, *http.Request){stream(text("half"), `{"error":{"message":"overloaded"}}`), ok}, 2, nil},
		"no resources":        {[]func(http.ResponseWriter, *http.Request){stream(finished("insufficient_system_resource")), ok}, 2, nil},
		"bad request":         {[]func(http.ResponseWriter, *http.Request){status(400, `{"error":{"message":"bad model"}}`), ok}, 1, otogi.ErrInvalid},
		"insufficient credit": {[]func(http.ResponseWriter, *http.Request){status(402, `{"error":{"message":"Insufficient Balance"}}`), ok}, 1, otogi.ErrInvalid},
		"down":                {[]func(http.ResponseWriter, *http.Request){status(503, `{}`)}, 3, otogi.ErrUnavailable},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			p, srv, rec := setup(t, test.responses...)
			resp, err := p.Generate(context.Background(), llm.Request{Model: "m", Turns: []llm.Turn{userText("hi")}}, nil)
			if n := len(srv.requests()); n != test.requests {
				t.Fatalf("%d requests, want %d", n, test.requests)
			}
			if test.want == nil {
				if err != nil || resp.Markdown != "fine" {
					t.Fatalf("%q, %v", resp.Markdown, err)
				}
				attempts, _ := payload(t, rec.last(t))["http"].([]*attempt)
				if len(attempts) != 2 || attempts[0].Wait == "" {
					t.Fatalf("attempts = %+v", attempts)
				}
				return
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("err = %v, want %v", err, test.want)
			}
			if r := rec.last(t); r.Kind != "llm.failed" || r.Outcome != "error" {
				t.Fatalf("record = %+v", r)
			}
		})
	}
	t.Run("message", func(t *testing.T) {
		p, _, _ := setup(t, status(400, `{"error":{"message":"bad model"}}`))
		if _, err := p.Generate(context.Background(), llm.Request{Model: "m", Turns: []llm.Turn{userText("hi")}}, nil); err == nil || !strings.Contains(err.Error(), "HTTP 400: bad model") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("retry after", func(t *testing.T) {
		p, _, rec := setup(t, status(429, `{}`, "Retry-After", "1"), ok)
		if _, err := p.Generate(context.Background(), llm.Request{Model: "m", Turns: []llm.Turn{userText("hi")}}, nil); err != nil {
			t.Fatal(err)
		}
		if attempts, _ := payload(t, rec.last(t))["http"].([]*attempt); len(attempts) != 2 || attempts[0].Wait != "1s" {
			t.Fatalf("attempts = %+v", attempts)
		}
	})
	t.Run("partial error", func(t *testing.T) {
		p, srv, _ := setup(t, ok, ok)
		stop := errors.New("enough")
		_, err := p.Generate(context.Background(), llm.Request{Model: "m", Turns: []llm.Turn{userText("hi")}}, func(otogi.Message) error { return stop })
		if !errors.Is(err, stop) || len(srv.requests()) != 1 {
			t.Fatalf("err = %v, %d requests", err, len(srv.requests()))
		}
	})
	t.Run("canceled", func(t *testing.T) {
		p, _, _ := setup(t, ok)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := p.Generate(ctx, llm.Request{Model: "m", Turns: []llm.Turn{userText("hi")}}, nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestFinish(t *testing.T) {
	for reason, want := range map[string]struct {
		finish llm.Finish
		reason string
	}{
		"stop":           {llm.FinishStop, ""},
		"length":         {llm.FinishMaxTokens, ""},
		"content_filter": {llm.FinishBlocked, "content_filter"},
		"aborted":        {llm.FinishOther, "aborted"},
	} {
		p, _, _ := setup(t, stream(text("x"), finished(reason)))
		resp, err := p.Generate(context.Background(), llm.Request{Model: "m", Turns: []llm.Turn{userText("hi")}}, nil)
		if err != nil || resp.Finish != want.finish || resp.Reason != want.reason {
			t.Errorf("%s: %s %q, %v", reason, resp.Finish, resp.Reason, err)
		}
		if resp.Usage != (llm.Usage{Input: 5, Cached: 2, Output: 2, Thoughts: 1}) {
			t.Errorf("%s: usage = %+v", reason, resp.Usage)
		}
	}
}

// Arguments that are not JSON reach the model as a string, so it sees its
// own malformed call; Native keeps them as they came.
func TestMalformedArguments(t *testing.T) {
	p, _, _ := setup(t, stream(
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c","type":"function","function":{"name":"f","arguments":"{\"in\":"}}]},"finish_reason":null}]}`,
		finished("tool_calls")))
	resp, err := p.Generate(context.Background(), llm.Request{Model: "m", Turns: []llm.Turn{userText("hi")}}, nil)
	if err != nil || len(resp.Turn.Calls) != 1 || string(resp.Turn.Calls[0].Args) != `"{\"in\":"` {
		t.Fatalf("calls = %+v, %v", resp.Turn.Calls, err)
	}
	if !strings.Contains(string(resp.Turn.Native.Data), `"arguments":"{\"in\":"`) {
		t.Fatalf("native = %s", resp.Turn.Native.Data)
	}
}
