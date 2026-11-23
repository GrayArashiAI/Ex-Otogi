package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// The live tests call the real DeepSeek API. They run only when
// OTOGI_DEEPSEEK_API_KEY is set, so they are not part of make quality:
//
//	OTOGI_DEEPSEEK_API_KEY=... go test -run Live -v ./internal/adapter/openai
//
// OTOGI_DEEPSEEK_BASE_URL and OTOGI_DEEPSEEK_MODEL override the defaults.
func liveProvider(t *testing.T) (*Provider, *recorder, string) {
	t.Helper()
	key := os.Getenv("OTOGI_DEEPSEEK_API_KEY")
	if key == "" {
		t.Skip("OTOGI_DEEPSEEK_API_KEY is not set")
	}
	base := os.Getenv("OTOGI_DEEPSEEK_BASE_URL")
	if base == "" {
		base = "https://api.deepseek.com"
	}
	model := os.Getenv("OTOGI_DEEPSEEK_MODEL")
	if model == "" {
		model = "deepseek-flash"
	}
	rec := &recorder{}
	p, err := New(Config{Name: "deepseek", BaseURL: base, APIKey: key, Recorder: rec, Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	return p, rec, model
}

// logRecords shows each call's record, for what the real API sent.
func logRecords(t *testing.T, rec *recorder) {
	t.Helper()
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, r := range rec.records {
		data, _ := json.MarshalIndent(r.Payload, "", "  ")
		t.Logf("%s %s %v\n%s", r.Kind, r.Outcome, r.Duration, data)
	}
}

func liveContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

func TestLiveReply(t *testing.T) {
	p, rec, model := liveProvider(t)
	defer logRecords(t, rec)
	partials := 0
	resp, err := p.Generate(liveContext(t), llm.Request{Model: model, Thinking: "low", System: "Answer in one short sentence.",
		Turns: []llm.Turn{userText("What colour is the sky on a clear day?")}}, func(otogi.Message) error {
		partials++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if partials == 0 || resp.Finish != llm.FinishStop || resp.Usage.Input == 0 || resp.Usage.Output == 0 || !strings.Contains(strings.ToLower(resp.Markdown), "blue") {
		t.Fatalf("response = %+v, %d partials", resp, partials)
	}
}

// A function call, then its result with the assistant message sent back,
// reasoning included, as DeepSeek requires when tools are declared.
func TestLiveToolCall(t *testing.T) {
	p, rec, model := liveProvider(t)
	defer logRecords(t, rec)
	tools := []llm.ToolSpec{{Function: &llm.Function{Name: "set_reminder", Description: "Set a reminder for the user.",
		Parameters: json.RawMessage(`{"type":"object","properties":{"in":{"type":"string","description":"how long from now, such as 10m"},"text":{"type":"string"}},"required":["in","text"]}`)}}}
	turns := []llm.Turn{userText("Remind me to drink water in 10 minutes.")}
	first, err := p.Generate(liveContext(t), llm.Request{Model: model, Thinking: "low", Turns: turns, Tools: tools}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Finish != llm.FinishToolCalls || len(first.Turn.Calls) != 1 || first.Turn.Calls[0].Name != "set_reminder" {
		t.Fatalf("first = %+v", first)
	}
	call := first.Turn.Calls[0]
	turns = append(turns, first.Turn, llm.Turn{Role: llm.RoleTool, Results: []llm.Result{{CallID: call.ID, Name: call.Name, Output: json.RawMessage(`{"at":"16:40"}`)}}})
	second, err := p.Generate(liveContext(t), llm.Request{Model: model, Thinking: "low", Turns: turns, Tools: tools}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.Finish != llm.FinishStop || second.Markdown == "" {
		t.Fatalf("second = %+v", second)
	}
}

func TestLiveImage(t *testing.T) {
	p, rec, model := liveProvider(t)
	defer logRecords(t, rec)
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for x := range 32 {
		for y := range 32 {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	turn := llm.Turn{Role: llm.RoleUser, Content: otogi.Message{Parts: []otogi.Part{
		{ID: "t", Kind: otogi.PartText, Text: "What colour is this picture? One word."},
		{ID: "i", Kind: otogi.PartMedia, Media: &otogi.Media{Role: otogi.MediaImage, MediaType: "image/png", Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: buf.Bytes()}}},
	}}}
	resp, err := p.Generate(liveContext(t), llm.Request{Model: model, Thinking: "low", Turns: []llm.Turn{turn}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(resp.Markdown), "red") {
		t.Fatalf("reply = %q", resp.Markdown)
	}
}

// Web search declared with a function, which DeepSeek accepts though its
// documentation says otherwise.
func TestLiveWebSearch(t *testing.T) {
	p, rec, model := liveProvider(t)
	defer logRecords(t, rec)
	tools := []llm.ToolSpec{{Builtin: llm.BuiltinWebSearch}, {Function: &llm.Function{Name: "set_reminder", Description: "Set a reminder.", Parameters: json.RawMessage(`{"type":"object"}`)}}}
	resp, err := p.Generate(liveContext(t), llm.Request{Model: model, Thinking: "low", Tools: tools,
		Turns: []llm.Turn{userText("What is the latest stable version of the Go programming language? Search the web and answer briefly.")}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	use, _ := payload(t, rec.last(t))["server_tool_use"].(json.RawMessage)
	if resp.Finish != llm.FinishStop || len(resp.Sources) == 0 || !strings.Contains(string(use), `"tool_search"`) {
		t.Fatalf("response = %+v, use = %s", resp, use)
	}
}

// A page read in one turn is still there in the next, from the signature
// Native carries back.
func TestLiveReadURL(t *testing.T) {
	p, rec, model := liveProvider(t)
	defer logRecords(t, rec)
	turns := []llm.Turn{userText("Read https://go.dev/doc/devel/release and say in one sentence what the page is.")}
	first, err := p.Generate(liveContext(t), llm.Request{Model: model, Thinking: "low", Tools: []llm.ToolSpec{{Builtin: llm.BuiltinURLContext}}, Turns: turns}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(first.Turn.Native.Data), `"signature"`) {
		t.Fatalf("first native = %.300s", first.Turn.Native.Data)
	}
	turns = append(turns, first.Turn, userText("From that page, which Go version is listed first? Answer with the version only."))
	second, err := p.Generate(liveContext(t), llm.Request{Model: model, Thinking: "low", Turns: turns}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(second.Markdown, "go1.") && !strings.Contains(second.Markdown, "1.2") {
		t.Fatalf("second = %q", second.Markdown)
	}
}
