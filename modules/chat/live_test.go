package chat

import (
	"bytes"
	"cmp"
	"context"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/gemini"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/openai"
	"github.com/TundraWork/Ex-Otogi/modules/reminders"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	mediasvc "github.com/TundraWork/Ex-Otogi/pkg/media"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// TestLiveConversation runs the module against the real Gemini API; it runs
// only when OTOGI_GEMINI_API_KEY is set:
//
//	OTOGI_GEMINI_API_KEY=... go test -run Live -v ./modules/chat
func TestLiveConversation(t *testing.T) {
	key := os.Getenv("OTOGI_GEMINI_API_KEY")
	if key == "" {
		t.Skip("OTOGI_GEMINI_API_KEY is not set")
	}
	g, err := gemini.New(gemini.Config{APIKey: key, BaseURL: os.Getenv("OTOGI_GEMINI_BASE_URL"), Recorder: otogi.NopRecorder{}, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, ProfileConfig{Name: "otogi", Label: "🤖 Otogi", Keywords: []string{"otogi"}, Provider: "gemini", Model: "gemini-3.8-flash", Thinking: "low",
		System: "You are Otogi, a cheerful member of this chat. Answer in one or two short sentences."})
	h.m.profiles[0].provider = g

	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for x := range 32 {
		for y := range 32 {
			img.Set(x, y, color.RGBA{G: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	h.mustHandle(h.message("Bob", "the meeting moved to 3pm, bring the green folder"))
	photo := otogi.Part{ID: "p", Kind: otogi.PartMedia, Media: &otogi.Media{Role: otogi.MediaImage, MediaType: "image/png", Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: buf.Bytes()}}}
	ask := h.message("Alice", "otogi, when is the meeting, and what colour is this?", func(e *otogi.Event) {
		e.Message.Parts = append(e.Message.Parts, photo)
		e.Message.Roots = append(e.Message.Roots, "p")
	})
	h.mustHandle(ask)
	t.Logf("reply 1 (%d updates): %q", len(h.streams[0].updates), h.streams[0].final.PlainText())
	h.mustHandle(h.message("Alice", "and who told us?", replyingTo("agent-1")))
	t.Logf("reply 2 (%d updates): %q", len(h.streams[1].updates), h.streams[1].final.PlainText())
	for _, r := range h.host.records {
		if p, ok := r.Payload.(map[string]any); ok && r.Kind == "chat.reply" {
			t.Logf("%s %s trigger=%v finish=%v usage=%+v", r.Kind, r.Outcome, p["trigger"], p["finish"], p["usage"].(llm.Usage))
		}
	}
}

// TestLiveStickerThread checks, on the real APIs, that a thread ending with
// a send_sticker call and its result goes on: the model picks a sticker,
// then a reply to the sticker continues the thread. Gemini resends its own
// turns as they were; DeepSeek gets its reasoning back. Each runs only when
// its key is set:
//
//	OTOGI_GEMINI_API_KEY=... OTOGI_DEEPSEEK_API_KEY=... go test -run LiveSticker -v ./modules/chat
//
// OTOGI_GEMINI_MODEL and OTOGI_DEEPSEEK_MODEL pick other models.
func TestLiveStickerThread(t *testing.T) {
	quiet := slog.New(slog.DiscardHandler)
	for _, c := range []struct {
		name, key string
		provider  func(key string) (llm.Provider, string, error)
	}{
		{"gemini", "OTOGI_GEMINI_API_KEY", func(key string) (llm.Provider, string, error) {
			g, err := gemini.New(gemini.Config{APIKey: key, BaseURL: os.Getenv("OTOGI_GEMINI_BASE_URL"), Recorder: otogi.NopRecorder{}, Logger: quiet})
			return g, cmp.Or(os.Getenv("OTOGI_GEMINI_MODEL"), "gemini-3.8-flash"), err
		}},
		{"deepseek", "OTOGI_DEEPSEEK_API_KEY", func(key string) (llm.Provider, string, error) {
			p, err := openai.New(openai.Config{Name: "deepseek", BaseURL: cmp.Or(os.Getenv("OTOGI_DEEPSEEK_BASE_URL"), "https://api.deepseek.com"), APIKey: key, Recorder: otogi.NopRecorder{}, Logger: quiet})
			return p, cmp.Or(os.Getenv("OTOGI_DEEPSEEK_MODEL"), "deepseek-flash"), err
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			key := os.Getenv(c.key)
			if key == "" {
				t.Skip(c.key + " is not set")
			}
			p, model, err := c.provider(key)
			if err != nil {
				t.Fatal(err)
			}
			h := newHarnessWith(t, map[string]any{mediasvc.Service: newDescriber(), mediasvc.StickersService: newStickerBox()},
				ProfileConfig{Name: "otogi", Keywords: []string{"otogi"}, Provider: "live", Model: model, Media: &MediaConfig{Stickers: []int{1, 3}},
					System: "You are Otogi, a cheerful member of this chat. Answer in one short sentence. When someone asks for a sticker, send it with send_sticker."})
			h.m.profiles[0].provider = p
			h.mustHandle(h.message("Alice", "otogi, send me the dog sticker please"))
			_, payload := lastReplyRecord(t, h)
			st, _ := payload["sticker"].(*sentSticker)
			if st == nil || st.Ref == "" {
				t.Fatalf("no sticker sent; reply %q, record %+v", h.streams[0].final.PlainText(), payload)
			}
			t.Logf("reply 1: %q, then sticker %d (%s), usage %+v", h.streams[0].final.PlainText(), st.ID, st.Text, payload["usage"])
			h.mustHandle(h.message("Alice", "thanks! what was on it?", replyingTo(st.Ref)))
			r, payload := lastReplyRecord(t, h)
			if r.Outcome != "ok" || payload["trigger"] != "reply" {
				t.Fatalf("the reply to the sticker: %s %+v", r.Outcome, payload)
			}
			t.Logf("reply 2: %q", h.streams[len(h.streams)-1].final.PlainText())
		})
	}
}

// timers is a reminders host that keeps the timers set and the tools
// provided.
type timers struct {
	otogi.Host
	set      []otogi.Timer
	provided map[string]any
}

func (h *timers) SetTimer(_ context.Context, key string, at time.Time, payload []byte) error {
	h.set = append(h.set, otogi.Timer{Key: key, At: at, Payload: payload})
	return nil
}
func (h *timers) Provide(name string, service any) error {
	h.provided[name] = service
	return nil
}

func TestLiveTools(t *testing.T) {
	key := os.Getenv("OTOGI_GEMINI_API_KEY")
	if key == "" {
		t.Skip("OTOGI_GEMINI_API_KEY is not set")
	}
	g, err := gemini.New(gemini.Config{APIKey: key, BaseURL: os.Getenv("OTOGI_GEMINI_BASE_URL"), Recorder: otogi.NopRecorder{}, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	reminderHost := &timers{provided: map[string]any{}}
	if err := reminders.New(reminders.Config{}).Start(context.Background(), reminderHost); err != nil {
		t.Fatal(err)
	}
	storage, err := modulestore.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	h := &harness{t: t, host: &host{storage: storage, services: reminderHost.provided, lists: map[[2]string]string{{"otogi", group}: "test", {"tools:reminders", group}: "test"}}, llm: map[string]*provider{}, now: time.Now()}
	h.m, err = New(Config{Profiles: []ProfileConfig{{Name: "otogi", Label: "🤖 Otogi", Keywords: []string{"otogi"}, Provider: "gemini", Model: "gemini-3.8-flash", Thinking: "low",
		System: "You are Otogi, a helpful member of this chat. Answer briefly.", Tools: []string{"web_search"}}}, Tools: []string{"reminders"}}, func(string) (llm.Provider, error) { return g, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.m.now = time.Now
	if err := h.m.Start(context.Background(), h.host); err != nil {
		t.Fatal(err)
	}
	h.mustHandle(h.message("Alice", "otogi, remind me in 10 minutes to stretch. Also search the web: who won the most recent Ballon d'Or?", func(e *otogi.Event) { e.OccurredAt = time.Now() }))
	t.Logf("reply: %q", h.streams[0].final.PlainText())
	for _, r := range h.host.records {
		t.Logf("%s %s %v", r.Kind, r.Outcome, r.Payload)
	}
	if len(reminderHost.set) != 1 {
		t.Fatalf("%d reminders set", len(reminderHost.set))
	}
	if !strings.Contains(h.streams[0].final.PlainText(), "Sources:") {
		t.Fatal("no sources")
	}
}
