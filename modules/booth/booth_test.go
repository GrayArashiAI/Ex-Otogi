package booth

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/cardimage"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

func TestItemsIn(t *testing.T) {
	t.Parallel()
	var r otogi.RichText
	r.Mark(otogi.AnnotationLink, "https://booth.pm/zh-cn/items/42", func() { r.Write("this one") })
	r.Write(" and https://pyrite9.booth.pm/items/9111596 and booth.pm/items/7 and booth.pm/ja/items/8")
	for name, c := range map[string]struct {
		msg  otogi.Message
		want []string
	}{
		"text, in order, no repeats":                     {otogi.TextMessage("https://booth.pm/ja/items/9111596 再看 booth.pm/en/items/123?x=1 和 https://booth.pm/ja/items/9111596"), []string{"9111596", "123"}},
		"behind formatting, shop domains, at most three": {r.Message(), []string{"42", "9111596", "7"}},
		"nothing": {otogi.TextMessage("booth.pm is a site; booth.pm/ja/browse/3D is no item; items/123 alone is not a link"), nil},
	} {
		if got := itemsIn(c.msg); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

func TestFormatting(t *testing.T) {
	t.Parallel()
	for n, want := range map[int64]string{0: "免费", 100: "¥100", 1200: "¥1,200", 1234567: "¥1,234,567"} {
		if got := yen(n); got != want {
			t.Errorf("yen(%d) = %q, want %q", n, got, want)
		}
	}
	it := item{Price: "¥ 500"}
	if got := price(it); got != "¥ 500" {
		t.Errorf("price without options = %q", got)
	}
	it.Variations = []variation{{"free", 0}, {"support", 1500}}
	if got := price(it); got != "免费 – ¥1,500" {
		t.Errorf("price range = %q", got)
	}
}

// fakeBooth answers the module's requests; every host is sent to it.
type fakeBooth struct {
	mu       sync.Mutex
	adult    bool
	requests []string
}

func (f *fakeBooth) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Host+r.URL.Path+" cookie="+r.Header.Get("Cookie"))
	switch {
	case r.Host == "booth.pm" && r.URL.Path == "/ja/items/9111596.json":
		data, _ := os.ReadFile("testdata/item.json")
		if f.adult {
			data = bytes.Replace(data, []byte(`"is_adult": false`), []byte(`"is_adult": true`), 1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	case r.Host == "booth.pximg.net" && strings.HasSuffix(r.URL.Path, ".jpg"):
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("jpeg " + r.URL.Path))
	default:
		http.NotFound(w, r)
	}
}

// toFake sends every request, whatever its host, to the fake.
type toFake struct{ srv *httptest.Server }

func (t toFake) RoundTrip(r *http.Request) (*http.Response, error) {
	u, _ := url.Parse(t.srv.URL)
	r = r.Clone(r.Context())
	r.Host, r.URL.Scheme, r.URL.Host = r.URL.Host, u.Scheme, u.Host
	return t.srv.Client().Transport.RoundTrip(r)
}

type host struct {
	otogi.Host
	records []otogi.Record
}

func (h *host) Logger() *slog.Logger                     { return slog.New(slog.DiscardHandler) }
func (h *host) Record(_ context.Context, r otogi.Record) { h.records = append(h.records, r) }
func (h *host) Allowed(l, chat string) bool              { return l == list && chat == "qq:group:G1" }

type invocation struct {
	otogi.Invocation
	sent []otogi.Message
	keys []string
}

func (i *invocation) Send(_ context.Context, slot string, msg otogi.Message) (otogi.EventRef, error) {
	i.sent, i.keys = append(i.sent, msg), append(i.keys, slot)
	return otogi.EventRef{}, nil
}

func newTestModule(t *testing.T, render func(context.Context, card) ([]byte, error)) (*Module, *fakeBooth, *host) {
	t.Helper()
	chrome := filepath.Join(t.TempDir(), "chrome-headless-shell")
	if err := os.WriteFile(chrome, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	m, err := New(Config{Chrome: chrome})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeBooth{}
	srv := httptest.NewTLSServer(fake)
	t.Cleanup(srv.Close)
	m.api.http = &http.Client{Transport: toFake{srv}}
	m.render = render
	h := &host{}
	if err := m.Start(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	return m, fake, h
}

func message(chat, text string) otogi.Event {
	return otogi.Event{ID: "e1", Platform: "qq", Account: "bot", Conversation: otogi.Conversation{ID: otogi.ConversationID(chat)}, SourceID: "m1",
		Actor: otogi.Actor{ID: "user:U1"}, Action: otogi.ActionMessageCreated, Direction: otogi.DirectionIncoming, Message: otogi.TextMessage(text)}
}

// A link gets a card, as a reply, showing the first picture that does not
// move; a missing item gets nothing; a chat not listed is left alone.
func TestCards(t *testing.T) {
	t.Parallel()
	var drawn []card
	m, fake, h := newTestModule(t, func(_ context.Context, c card) ([]byte, error) {
		drawn = append(drawn, c)
		return []byte("PNG"), nil
	})
	inv := &invocation{}
	if err := m.Handle(context.Background(), message("group:G1", "look https://booth.pm/ja/items/9111596 and booth.pm/items/1"), inv); err != nil {
		t.Fatal(err)
	}
	if len(inv.sent) != 1 || inv.keys[0] != "card:9111596" {
		t.Fatalf("sent %d: %v", len(inv.sent), inv.keys)
	}
	msg := inv.sent[0]
	if p := msg.Parts[0]; p.Media == nil || string(p.Media.Source.Inline) != "PNG" || msg.Relations[0].Target.SourceEvent != "m1" {
		t.Fatalf("card message %+v", msg)
	}
	c := drawn[0]
	if !strings.HasPrefix(c.Title, "(v0.1.0) [FREE] Pyrite9 Sleeve Roll") || c.Shop != "pyrite9" || c.Category != "3Dモデル › 3D装飾品" || c.Price != "免费 – ¥100" ||
		c.Wishes != "398" || c.Published != "2026-11-07 发布" || len(c.Options) != 2 || c.Options[1] != (option{"支援版 / Support", "¥100"}) || len(c.Tags) != maxTags || c.Adult {
		t.Fatalf("card %+v", c)
	}
	picture, _ := url.Parse(h.records[0].Payload.(map[string]any)["picture"].(string))
	if !strings.HasSuffix(picture.Path, "5e43ac65-77fc-4d78-b384-9faa8faddee6_base_resized.jpg") || !strings.HasPrefix(string(c.Picture), "data:image/jpeg;base64,") || c.Icon == "" {
		t.Fatalf("picture %s, card picture %.40s, icon %.40s", picture, c.Picture, c.Icon)
	}
	if got := strings.Join(fake.requests, "\n"); !strings.Contains(got, "booth.pm/ja/items/9111596.json cookie=adult=t") || !strings.Contains(got, "/c/128x128/users/") || strings.Contains(got, ".gif") {
		t.Fatalf("requests %s", got)
	}
	var outcomes []string
	for _, r := range h.records {
		outcomes = append(outcomes, r.Kind+":"+r.Outcome)
	}
	if !slices.Equal(outcomes, []string{"booth.card:ok", "booth.card:skipped"}) {
		t.Fatalf("records %v", outcomes)
	}

	inv = &invocation{}
	if err := m.Handle(context.Background(), message("group:OTHER", "booth.pm/ja/items/9111596"), inv); err != nil || len(inv.sent) != 0 {
		t.Fatalf("unlisted chat: %v, %d sent", err, len(inv.sent))
	}
}

// When Chrome cannot draw the card, the reply is the card in words with
// the picture, but an R-18 item's picture stays out.
func TestCardInWordsWhenRenderingFails(t *testing.T) {
	t.Parallel()
	m, fake, h := newTestModule(t, func(context.Context, card) ([]byte, error) { return nil, errors.New("chrome exited") })
	inv := &invocation{}
	if err := m.Handle(context.Background(), message("group:G1", "booth.pm/ja/items/9111596"), inv); err != nil {
		t.Fatal(err)
	}
	msg := inv.sent[0]
	if !strings.Contains(msg.PlainText(), "【VRChat】\npyrite9 · 免费 – ¥100 · ♥398") || len(msg.Parts) != 2 || msg.Parts[1].Media == nil {
		t.Fatalf("fallback %q %+v", msg.PlainText(), msg.Parts)
	}
	if r := h.records[0]; r.Outcome != "degraded" || r.Payload.(map[string]any)["render_error"] != "chrome exited" {
		t.Fatalf("record %+v", r)
	}
	fake.adult = true
	inv = &invocation{}
	if err := m.Handle(context.Background(), message("group:G1", "booth.pm/ja/items/9111596"), inv); err != nil {
		t.Fatal(err)
	}
	if msg := inv.sent[0]; len(msg.Parts) != 1 || !strings.HasSuffix(msg.PlainText(), " · R-18") {
		t.Fatalf("adult fallback %q %+v", msg.PlainText(), msg.Parts)
	}
}

func TestConfig(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		cfg  Config
		want string
	}{
		{Config{}, "the path of chrome-headless-shell is required"},
		{Config{Chrome: "/nonexistent/chrome"}, "no such file"},
	} {
		if _, err := New(c.cfg); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v, want %q", c.cfg, err, c.want)
		}
	}
}

// The real renderer, when a Chrome is at hand (OTOGI_CHROME): the card
// comes back as a PNG.
func TestRenderWithChrome(t *testing.T) {
	chrome := os.Getenv("OTOGI_CHROME")
	if chrome == "" {
		t.Skip("set OTOGI_CHROME to a chrome-headless-shell binary")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	png, err := render(ctx, cardimage.Renderer{Chrome: chrome}, card{Title: "袖まくりギミック 🔥 title", Shop: "shop", Category: "3Dモデル › 3D装飾品", Price: "免费 – ¥100", Wishes: "398",
		Options: []option{{"無料版", "免费"}, {"支援版", "¥100"}}, More: 1, Tags: []string{"VRChat", "無料"}, Desc: "line\nline", ID: "9111596", Published: "2026-11-07 发布", Adult: true, SoldOut: true})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(png, []byte("\x89PNG")) {
		t.Fatal("not a PNG")
	}
	if out := os.Getenv("OTOGI_CARD_OUT"); out != "" {
		_ = os.WriteFile(out, png, 0o600)
	}
}
