package media

import (
	"context"
	"image"
	"image/color"
	"log/slog"
	"os"
	"testing"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/gemini"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// TestLiveDescribe has Gemini describe a picture and an animation. It runs
// only when OTOGI_GEMINI_API_KEY is set, so it is not part of make quality:
//
//	OTOGI_GEMINI_API_KEY=... go test -run Live -v ./modules/media
//
// OTOGI_GEMINI_MODEL overrides the first step's model.
func TestLiveDescribe(t *testing.T) {
	key := os.Getenv("OTOGI_GEMINI_API_KEY")
	if key == "" {
		t.Skip("OTOGI_GEMINI_API_KEY is not set")
	}
	storage, err := modulestore.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	h := &harness{t: t, host: &host{storage: storage, allowed: map[string]bool{chat: true}, services: map[string]any{}}, inv: &invocation{files: map[string][]byte{}}}
	provider, err := gemini.New(gemini.Config{APIKey: key, BaseURL: os.Getenv("OTOGI_GEMINI_BASE_URL"), Recorder: h.host, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	first := "gemini-3.5-flash-lite"
	if m := os.Getenv("OTOGI_GEMINI_MODEL"); m != "" {
		first = m
	}
	h.m, err = New(Config{Language: "Chinese", Steps: []Step{
		{Provider: "gemini", Model: first, Thinking: "minimal"},
		{Provider: "gemini", Model: "gemini-3.1-flash-lite", Thinking: "minimal"},
	}}, func(string) (llm.Provider, error) { return provider, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.m.Start(context.Background(), h.host); err != nil {
		t.Fatal(err)
	}

	still := h.image("https://live/still", pngOf(t, color.RGBA{255, 0, 0, 255}))
	// A dot moving down the diagonal, a frame for each of animationFrames.
	var frames []gifFrame
	for x := range animationFrames {
		frames = append(frames, gifFrame{at: image.Pt(x%4, x%4), c: color.RGBA{0, 0, 255, 255}, delay: 20})
	}
	frames[0] = gifFrame{full: true, c: color.White, delay: 20}
	animated := h.image("https://live/anim", gifOf(t, frames))
	animated.Role, animated.MediaType = otogi.MediaAnimation, "image/gif"

	for _, md := range []otogi.Media{still, animated} {
		if err := h.handle(md); err != nil {
			t.Fatal(err)
		}
		d, ok, err := h.m.Lookup(context.Background(), from, md)
		if err != nil || !ok || d.Summary == "" {
			t.Fatalf("%s: Lookup = %+v, %v, %v; records %v", md.Source.URL, d, ok, err, h.host.kinds())
		}
		t.Logf("%s: %s %q, text %q", md.Source.URL, d.Kind, d.Summary, d.Text)
	}
	// The animation went as a video the model saw every frame of: at 64
	// tokens a frame, seen at a frame a second, past them in all, while a
	// video at its own speed would be one or two frames.
	h.host.mu.Lock()
	defer h.host.mu.Unlock()
	for _, r := range h.host.records {
		if p, _ := r.Payload.(map[string]any); r.Kind == "media.described" && p["frames"] != nil {
			in, _ := p["input_tokens"].(int)
			if in < animationFrames*64 || in > animationFrames*64+600 {
				t.Fatalf("the animation took %d input tokens, want %d for its frames and some for the text", in, animationFrames*64)
			}
			t.Logf("the animation took %d input tokens", in)
		}
	}
}
