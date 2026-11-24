package twitter

import (
	"context"
	"log/slog"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/gemini"
)

// TestLiveTranslate has Gemini translate posts part by part: what is in
// Chinese already is left out, and the rest comes back in Chinese. It runs only when OTOGI_GEMINI_API_KEY is
// set, so it is not part of make quality:
//
//	OTOGI_GEMINI_API_KEY=... go test -run Live -v ./modules/twitter
//
// OTOGI_GEMINI_BASE_URL and OTOGI_GEMINI_MODEL override the endpoint and
// the model.
func TestLiveTranslate(t *testing.T) {
	key := os.Getenv("OTOGI_GEMINI_API_KEY")
	if key == "" {
		t.Skip("OTOGI_GEMINI_API_KEY is not set")
	}
	provider, err := gemini.New(gemini.Config{APIKey: key, BaseURL: os.Getenv("OTOGI_GEMINI_BASE_URL"), Recorder: &host{}, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	model := "gemini-3.8-flash"
	if m := os.Getenv("OTOGI_GEMINI_MODEL"); m != "" {
		model = m
	}
	tr := &translator{cfg: Translate{Provider: "gemini", Model: model, Thinking: "low"}, provider: provider}
	const english, chinese = "Forking our code is fine. Calling it by our name is not.", "给没吃上瓜的朋友理一下今天这事的来龙去脉。"
	for _, c := range []struct {
		name string
		post tweet
		// Which parts come back translated.
		wantPost, quote, parent, parentQuote bool
	}{
		{"english replying to english", tweet{Text: "I doubt it will help much.", Parent: &tweet{Text: english}}, true, false, true, false},
		{"chinese replying to english", tweet{Text: "我觉得这没什么用。", Parent: &tweet{Text: english}}, false, false, true, false},
		{"english quoting chinese", tweet{Text: english, Quote: &tweet{Text: chinese}}, true, false, false, false},
		{"chinese", tweet{Text: chinese}, false, false, false, false},
		{"english replying to english that quotes chinese", tweet{Text: "I doubt it will help much.", Parent: &tweet{Text: english, Quote: &tweet{Text: chinese}}}, true, false, true, false},
		{"pictures replying to english", tweet{Media: []media{{Kind: "photo"}}, Parent: &tweet{Text: english}}, false, false, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			// A request the provider never answers fails its own case.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			got, needed, err := tr.translate(ctx, c.post)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("needed %v: %+v", needed, got)
			for _, p := range []struct {
				name, got string
				want      bool
			}{{"post", got.Post, c.wantPost}, {"quote", got.Quote, c.quote}, {"parent", got.Parent, c.parent}, {"parent's quote", got.ParentQuote, c.parentQuote}} {
				// A translation is in Chinese; translate itself leaves out
				// one that is the original given back.
				if (p.got != "") != p.want || p.want && !han.MatchString(p.got) {
					t.Errorf("%s: translation %q, want one %v", p.name, p.got, p.want)
				}
			}
		})
	}
}

var han = regexp.MustCompile(`\p{Han}`)
