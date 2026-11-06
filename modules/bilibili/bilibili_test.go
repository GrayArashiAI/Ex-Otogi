package bilibili

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

func TestVideosIn(t *testing.T) {
	t.Parallel()
	link := otogi.TextMessage("看这个 https://www.bilibili.com/video/BV1gm4a6yEor?p=2 还有 b23.tv/AbCdEf1 和 m.bilibili.com/video/av170001")
	var r otogi.RichText
	r.Mark(otogi.AnnotationLink, "https://bilibili.com/video/BV17x411w7KC", func() { r.Write("this one") })
	r.Write(" and again https://b23.tv/BV1gm4a6yEor and https://bili2233.cn/xyz")
	for name, c := range map[string]struct {
		msg  otogi.Message
		want []found
	}{
		"text, in order": {link, []found{{id: "BV1gm4a6yEor"}, {short: "https://b23.tv/AbCdEf1"}, {id: "av170001"}}},
		"behind formatting, no repeats, at most three": {r.Message(), []found{{id: "BV1gm4a6yEor"}, {short: "https://b23.tv/xyz"}, {id: "BV17x411w7KC"}}},
		"nothing":       {otogi.TextMessage("bilibili.com is a site; BV1gm4a6yEor alone is not a link; bilibili.com/video/BV1gm4a6yEorX is too long"), nil},
		"case of an av": {otogi.TextMessage("bilibili.com/video/AV42"), []found{{id: "av42"}}},
	} {
		if got := videosIn(c.msg); !slices.Equal(got, c.want) {
			t.Errorf("%s: %+v, want %+v", name, got, c.want)
		}
	}
}

func TestFormatting(t *testing.T) {
	t.Parallel()
	for n, want := range map[int64]string{0: "0", 9999: "9999", 10_000: "1万", 65_306: "6.5万", 45_842_283: "4584.2万", 123_456_789: "1.2亿"} {
		if got := count(n); got != want {
			t.Errorf("count(%d) = %q, want %q", n, got, want)
		}
	}
	for s, want := range map[int64]string{59: "0:59", 128: "2:08", 3600: "1:00:00", 3725: "1:02:05"} {
		if got := duration(s); got != want {
			t.Errorf("duration(%d) = %q, want %q", s, got, want)
		}
	}
	if got := lines("a\n\n  b \n\n\nc"); got != "a\nb\nc" {
		t.Errorf("lines = %q", got)
	}
}

// fakeBilibili answers the module's requests; every host is sent to it.
type fakeBilibili struct {
	mu       sync.Mutex
	spi      int
	refuse   bool // the next view gets risk control's 412
	requests []string
}

func (f *fakeBilibili) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Host+r.URL.Path+"?"+r.URL.RawQuery+" cookie="+r.Header.Get("Cookie"))
	switch {
	case r.URL.Path == "/x/frontend/finger/spi":
		f.spi++
		_, _ = fmt.Fprintf(w, `{"code":0,"data":{"b_3":"B3-%d","b_4":"B4=="}}`, f.spi)
	case r.URL.Path == "/x/web-interface/view" && f.refuse:
		f.refuse = false
		w.WriteHeader(http.StatusPreconditionFailed)
		_, _ = w.Write([]byte("<html>risk control</html>"))
	case r.URL.Path == "/x/web-interface/view" && r.URL.Query().Get("bvid") == "BV1gone00000":
		_, _ = w.Write([]byte(`{"code":-404,"message":"啥都木有"}`))
	case r.URL.Path == "/x/web-interface/view":
		_, _ = w.Write([]byte(`{"code":0,"data":{"bvid":"BV1gm4a6yEor","title":"🔥让银河燃烧吧🔥","pic":"http://i1.hdslb.com/cover.jpg","desc":"hello","duration":128,"pubdate":1793935800,"videos":1,
			"owner":{"name":"AozakiArisu","face":"https://i2.hdslb.com/face.jpg"},"stat":{"view":65306,"danmaku":130,"reply":382,"favorite":2218,"coin":1215,"share":2362,"like":4848},
			"honor_reply":{"honor":[{"desc":"热门收录"}]}}}`))
	case r.Host == "b23.tv":
		http.Redirect(w, r, "https://www.bilibili.com/video/BV1gm4a6yEor?share_source=copy", http.StatusFound)
	case strings.HasPrefix(r.URL.Path, "/cover.jpg"), strings.HasPrefix(r.URL.Path, "/face.jpg"):
		w.Header().Set("Content-Type", "image/webp")
		_, _ = w.Write([]byte("image bytes"))
	default:
		http.NotFound(w, r)
	}
}

// toFake sends every request, whatever its host, to the fake.
type toFake struct{ srv *httptest.Server }

func (t toFake) RoundTrip(r *http.Request) (*http.Response, error) {
	u, _ := url.Parse(t.srv.URL)
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = u.Scheme, u.Host
	return t.srv.Client().Transport.RoundTrip(r)
}

type host struct {
	otogi.Host
	records []otogi.Record
}

func (h *host) Logger() *slog.Logger                     { return slog.New(slog.DiscardHandler) }
func (h *host) Record(_ context.Context, r otogi.Record) { h.records = append(h.records, r) }

type invocation struct {
	otogi.Invocation
	sent []otogi.Message
	keys []string
}

func (i *invocation) Send(_ context.Context, slot string, msg otogi.Message) (otogi.EventRef, error) {
	i.sent, i.keys = append(i.sent, msg), append(i.keys, slot)
	return otogi.EventRef{}, nil
}

func newTestModule(t *testing.T, render func(context.Context, card) ([]byte, error)) (*Module, *fakeBilibili, *host) {
	t.Helper()
	chrome := filepath.Join(t.TempDir(), "chrome-headless-shell")
	if err := os.WriteFile(chrome, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	m, err := New(Config{Chats: []string{"qq:group:G1"}, Chrome: chrome})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeBilibili{}
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

// A link gets a card, as a reply; a short link is followed; a missing video
// gets nothing; a chat not listed is left alone.
func TestCards(t *testing.T) {
	t.Parallel()
	var drawn []card
	m, fake, h := newTestModule(t, func(_ context.Context, c card) ([]byte, error) {
		drawn = append(drawn, c)
		return []byte("PNG"), nil
	})
	fake.refuse = true // risk control first: fresh cookies, then it works
	inv := &invocation{}
	if err := m.Handle(context.Background(), message("group:G1", "look https://b23.tv/AbC1, bilibili.com/video/BV1gm4a6yEor and bilibili.com/video/BV1gone00000"), inv); err != nil {
		t.Fatal(err)
	}
	if len(inv.sent) != 1 || inv.keys[0] != "card:BV1gm4a6yEor" {
		t.Fatalf("sent %d: %v", len(inv.sent), inv.keys)
	}
	msg := inv.sent[0]
	if p := msg.Parts[0]; p.Media == nil || string(p.Media.Source.Inline) != "PNG" || p.Media.MediaType != "image/png" || msg.Relations[0].Target.SourceEvent != "m1" {
		t.Fatalf("card message %+v", msg)
	}
	c := drawn[0]
	if c.Title != "🔥让银河燃烧吧🔥" || c.Owner != "AozakiArisu" || c.Duration != "2:08" || c.Stats[0].Value != "6.5万" || !slices.Equal(c.Honors, []string{"热门收录"}) ||
		c.Published != "2026-11-06 11:30 发布" || !strings.HasPrefix(string(c.Cover), "data:image/webp;base64,") {
		t.Fatalf("card %+v", c)
	}
	if fake.spi != 2 || !strings.Contains(strings.Join(fake.requests, "\n"), "cookie=buvid3=B3-2; buvid4=B4%3D%3D") {
		t.Fatalf("cookies: %d spi calls; requests %v", fake.spi, fake.requests)
	}
	var outcomes []string
	for _, r := range h.records {
		outcomes = append(outcomes, r.Kind+":"+r.Outcome)
	}
	if !slices.Equal(outcomes, []string{"bilibili.card:ok", "bilibili.card:skipped"}) {
		t.Fatalf("records %v", outcomes)
	}

	inv = &invocation{}
	if err := m.Handle(context.Background(), message("group:OTHER", "bilibili.com/video/BV1gm4a6yEor"), inv); err != nil || len(inv.sent) != 0 {
		t.Fatalf("unlisted chat: %v, %d sent", err, len(inv.sent))
	}
}

// When Chrome cannot draw the card, the reply is the card in words with the
// cover, and the record says so.
func TestCardInWordsWhenRenderingFails(t *testing.T) {
	t.Parallel()
	m, _, h := newTestModule(t, func(context.Context, card) ([]byte, error) { return nil, errors.New("chrome exited") })
	inv := &invocation{}
	if err := m.Handle(context.Background(), message("group:G1", "bilibili.com/video/BV1gm4a6yEor"), inv); err != nil {
		t.Fatal(err)
	}
	msg := inv.sent[0]
	if !strings.Contains(msg.PlainText(), "🔥让银河燃烧吧🔥\nAozakiArisu · 2:08 · 2026-11-06 发布\n6.5万播放") || len(msg.Parts) != 2 || msg.Parts[1].Media == nil {
		t.Fatalf("fallback %q %+v", msg.PlainText(), msg.Parts)
	}
	if r := h.records[0]; r.Outcome != "degraded" || r.Payload.(map[string]any)["render_error"] != "chrome exited" {
		t.Fatalf("record %+v", r)
	}
}

func TestConfig(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		cfg  Config
		want string
	}{
		{Config{Chrome: "/bin/sh"}, "chats: at least one"},
		{Config{Chats: []string{"G1"}, Chrome: "/bin/sh"}, `"G1": want <platform>:<chat id>`},
		{Config{Chats: []string{"qq:group:G1"}}, "chrome: the path"},
		{Config{Chats: []string{"qq:group:G1"}, Chrome: "/nonexistent/chrome"}, "no such file"},
	} {
		if _, err := New(c.cfg); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v, want %q", c.cfg, err, c.want)
		}
	}
}

// The real renderer, when a Chrome is at hand (OTOGI_CHROME): the card
// comes back as a PNG at twice its CSS width.
func TestRenderWithChrome(t *testing.T) {
	chrome := os.Getenv("OTOGI_CHROME")
	if chrome == "" {
		t.Skip("set OTOGI_CHROME to a chrome-headless-shell binary")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	png, err := renderer{chrome: chrome}.render(ctx, card{Title: "标题 🔥 title", Owner: "someone", Duration: "1:00", Published: "today", ID: "BV1xx411c7mD", Parts: 2,
		Stats: []stat{{icons["view"], "1万", "播放", false}}, Honors: []string{"热门收录"}, Desc: "line\nline"})
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
