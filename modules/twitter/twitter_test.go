package twitter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/cardimage"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

func TestPostsIn(t *testing.T) {
	t.Parallel()
	var r otogi.RichText
	r.Write("see https://x.com/SpaceX/status/2112637394654822514?s=20 and twitter.com/NASA/status/2118034216503955710, ")
	r.Mark(otogi.AnnotationLink, "https://fxtwitter.com/i/status/111", func() { r.Write("this") })
	r.Write(" mobile.twitter.com/a/statuses/222 vxtwitter.com/b/status/333 x.com/SpaceX/status/2112637394654822514")
	if got := postsIn(r.Message()); !slices.Equal(got, []string{"2112637394654822514", "2118034216503955710", "111"}) {
		t.Fatalf("posts = %v", got)
	}
	if got := postsIn(otogi.TextMessage("x.com/SpaceX and example.com/a/status/1 and x.com/home")); got != nil {
		t.Fatalf("not posts: %v", got)
	}
}

func TestFormatting(t *testing.T) {
	t.Parallel()
	for n, want := range map[int64]string{7: "7", 4379: "4,379", 1005: "1,005", 836_652: "83.6万", 123_456_789: "1.2亿"} {
		if got := count(n); got != want {
			t.Errorf("count(%d) = %q, want %q", n, got, want)
		}
	}
	zone := time.FixedZone("CST", 8*3600)
	for at, want := range map[time.Time]string{
		time.Date(2026, 11, 6, 15, 4, 0, 0, zone): "下午3:04 · 2026年11月6日",
		time.Date(2026, 11, 6, 0, 30, 0, 0, zone): "上午12:30 · 2026年11月6日",
		time.Date(2026, 11, 6, 12, 0, 0, 0, zone): "下午12:00 · 2026年11月6日",
	} {
		if got := clock(at); got != want {
			t.Errorf("clock(%v) = %q, want %q", at, got, want)
		}
	}
	if got := length(41.033); got != "0:41" {
		t.Errorf("length = %q", got)
	}
	got := spans("hi @NASA #Webb https://www.example.com/a/very/long/path/to/something ok")
	want := []span{{"hi ", false}, {"@NASA", true}, {" ", false}, {"#Webb", true}, {" ", false}, {"example.com/a/very/long/pa…", true}, {" ok", false}}
	if !slices.Equal(got, want) {
		t.Errorf("spans = %+v", got)
	}
	if text, cut := excerpt(strings.Repeat("word ", 200), 100); !cut || !strings.HasSuffix(text, "word…") || len([]rune(text)) > 101 {
		t.Errorf("excerpt = %q, %v", text, cut)
	}
}

// fakeX serves FxTwitter and the embed endpoint from the fixtures, and the
// pictures; a test sets how FxTwitter answers.
type fakeX struct {
	mu       sync.Mutex
	fx       func(id string) (int, string)
	requests []string
}

func (f *fakeX) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.URL.Path+"?"+r.URL.RawQuery)
	fx := f.fx
	f.mu.Unlock()
	switch {
	case strings.HasPrefix(r.URL.Path, "/2/status/"):
		code, file := fx(strings.TrimPrefix(r.URL.Path, "/2/status/"))
		w.WriteHeader(code)
		_, _ = w.Write(fixture(file))
	case r.URL.Path == "/tweet-result" && r.URL.Query().Get("id") == "2112637394654822514":
		_, _ = w.Write(fixture("syndication-quote.json"))
	case r.URL.Path == "/tweet-result":
		_, _ = w.Write([]byte(`{"__typename":"TweetTombstone","tombstone":{"text":{"text":"This Post was deleted by the Post author."}}}`))
	default:
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("picture"))
	}
}

func fixture(name string) []byte {
	if name == "" {
		return nil
	}
	if !strings.HasSuffix(name, ".json") {
		return []byte(name) // a literal body
	}
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		panic(err)
	}
	return data
}

// toFake sends every request, whatever its host, to the fake.
type toFake struct{ srv *httptest.Server }

func (t toFake) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = "http", strings.TrimPrefix(t.srv.URL, "http://")
	return http.DefaultTransport.RoundTrip(r)
}

func newClient(t *testing.T, fx func(string) (int, string)) (*client, *fakeX) {
	t.Helper()
	fake := &fakeX{fx: fx}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	return &client{fx: "https://api.fxtwitter.com", syndication: "https://cdn.syndication.twimg.com", http: &http.Client{Transport: toFake{srv}}}, fake
}

func TestFxTwitter(t *testing.T) {
	t.Parallel()
	c, _ := newClient(t, func(string) (int, string) { return 200, "fx-quote.json" })
	tw, source, err := c.status(context.Background(), "2112637394654822514")
	if err != nil || source != "fxtwitter" {
		t.Fatal(source, err)
	}
	if tw.Author != (author{Name: "SpaceX", Handle: "SpaceX", Avatar: tw.Author.Avatar, Badge: "gold"}) || !strings.Contains(tw.Author.Avatar, "_200x200") {
		t.Errorf("author %+v", tw.Author)
	}
	if !strings.HasPrefix(tw.Text, "Our Q3 2026 earnings report is now public → https://ir.spacex.com/") || *tw.Views != 836652 || *tw.Likes != 4379 || *tw.Reposts != 557 {
		t.Errorf("post %+v", tw)
	}
	if q := tw.Quote; q == nil || q.Author.Handle != "SpaceX" || !strings.HasPrefix(q.Text, "Watch live") {
		t.Errorf("quote %+v", tw.Quote)
	}

	c, _ = newClient(t, func(string) (int, string) { return 200, "fx-video.json" })
	tw, _, _ = c.status(context.Background(), "2118034216503955710")
	if len(tw.Media) != 1 || tw.Media[0].Kind != "video" || tw.Media[0].Seconds < 41 || !strings.Contains(tw.Media[0].Image, "pbs.twimg.com") || tw.Author.Badge != "grey" {
		t.Errorf("video %+v, badge %q", tw.Media, tw.Author.Badge)
	}
}

// FxTwitter down: the embed endpoint answers, without views or reposts.
// FxTwitter saying a post is gone: it is gone, and nothing else is asked.
func TestFallback(t *testing.T) {
	t.Parallel()
	c, _ := newClient(t, func(string) (int, string) { return 503, "<html>down</html>" })
	tw, source, err := c.status(context.Background(), "2112637394654822514")
	if err != nil || source != "syndication" {
		t.Fatal(source, err)
	}
	if !strings.Contains(tw.Text, "https://ir.spacex.com/") || strings.Contains(tw.Text, "t.co") || tw.Views != nil || tw.Reposts != nil || *tw.Likes == 0 {
		t.Errorf("post %+v", tw)
	}
	if tw.Author.Badge != "gold" || !strings.Contains(tw.Author.Avatar, "_200x200") || tw.Quote == nil {
		t.Errorf("author %+v, quote %v", tw.Author, tw.Quote)
	}
	if _, _, err := c.status(context.Background(), "999"); !errors.As(err, new(*errGone)) {
		t.Errorf("tombstone = %v", err)
	}

	c, fake := newClient(t, func(string) (int, string) { return 404, `{"code":404,"status":null}` })
	if _, _, err := c.status(context.Background(), "1"); !errors.As(err, new(*errGone)) {
		t.Fatalf("gone = %v", err)
	}
	for _, r := range fake.requests {
		if strings.HasPrefix(r, "/tweet-result") {
			t.Fatal("a post FxTwitter reported gone was asked of the embed endpoint")
		}
	}
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
	sent    []otogi.Message
	keys    []string
	refused map[string]bool // slots the platform refuses for their content
}

func (i *invocation) Send(_ context.Context, slot string, msg otogi.Message) (otogi.EventRef, error) {
	i.keys = append(i.keys, slot)
	if i.refused[slot] {
		return otogi.EventRef{}, fmt.Errorf("qq POST /v2/groups/G1/messages: HTTP 400, code 40034006: 消息内容违规: %w", otogi.ErrRefused)
	}
	i.sent = append(i.sent, msg)
	return otogi.EventRef{}, nil
}

func chrome(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "chrome-headless-shell")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

// A link gets its card as a reply; a gone post gets nothing but a record; a
// chat not listed is left alone.
func TestCards(t *testing.T) {
	t.Parallel()
	m, err := New(Config{Chrome: chrome(t), Timezone: "Asia/Shanghai"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.api, _ = newClient(t, func(id string) (int, string) {
		if id == "1" {
			return 404, `{"code":404,"status":null}`
		}
		return 200, "fx-quote.json"
	})
	var drawn []card
	m.render = func(_ context.Context, c card) ([]byte, error) { drawn = append(drawn, c); return []byte("PNG"), nil }
	h := &host{}
	_ = m.Start(context.Background(), h)
	e := otogi.Event{Platform: "qq", Conversation: otogi.Conversation{ID: "group:G1"}, SourceID: "m1", Action: otogi.ActionMessageCreated,
		Message: otogi.TextMessage("https://x.com/SpaceX/status/2112637394654822514 x.com/a/status/1")}
	inv := &invocation{}
	if err := m.Handle(context.Background(), e, inv); err != nil {
		t.Fatal(err)
	}
	if len(inv.sent) != 1 || inv.keys[0] != "card:2112637394654822514" || string(inv.sent[0].Parts[0].Media.Source.Inline) != "PNG" || inv.sent[0].Relations[0].Target.SourceEvent != "m1" {
		t.Fatalf("sent %v %+v", inv.keys, inv.sent)
	}
	c := drawn[0]
	if c.When != "上午4:09 · 2026年10月21日" || c.Views != "83.6万" || c.Badge != "gold" || c.Quote == nil || c.Quote.Date != "10月20日" || c.Avatar == "" {
		t.Fatalf("card %+v", c)
	}
	var outcomes []string
	for _, r := range h.records {
		outcomes = append(outcomes, r.Outcome)
	}
	if !slices.Equal(outcomes, []string{"ok", "skipped"}) {
		t.Fatalf("records %v", outcomes)
	}
	e.Conversation.ID = "group:OTHER"
	if inv = (&invocation{}); m.Handle(context.Background(), e, inv) != nil || len(inv.sent) != 0 {
		t.Fatal("answered in a chat not listed")
	}
}

func noProviders(name string) (llm.Provider, error) {
	if name == "gemini" {
		return &model{}, nil
	}
	return nil, errors.New("no provider " + name)
}

// model answers translations with a fixed reply.
type model struct {
	reply    string
	err      error
	requests []llm.Request
}

func (m *model) Generate(_ context.Context, req llm.Request, _ func(otogi.Message) error) (llm.Response, error) {
	m.requests = append(m.requests, req)
	return llm.Response{Markdown: m.reply}, m.err
}

// A post not in Chinese gets its translation on its card, under the
// original, the quote's under the quote; a post in Chinese gets none; a
// reply out of form is recorded, and the card goes out without it.
func TestTranslatedCards(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, reply string
		translated  bool
		record      string
	}{
		{"english", "<language>英语</language>\n<post>我们 2026 年第三季度的财报现已公开 → https://ir.spacex.com/x @ElonMusk</post>\n<quote>观看直播</quote>", true, "ok"},
		{"chinese", "<language>中文</language>", false, ""},
		{"out of form", "Sure! Here is the translation.", false, "error"},
	} {
		t.Run(c.name, func(t *testing.T) {
			lm := &model{reply: c.reply}
			m, err := New(Config{Chrome: chrome(t), Translate: &Translate{Provider: "gemini", Model: "gemini-4-argon", Thinking: "low"}},
				func(string) (llm.Provider, error) { return lm, nil })
			if err != nil {
				t.Fatal(err)
			}
			m.api, _ = newClient(t, func(string) (int, string) { return 200, "fx-quote.json" })
			var drawn []card
			m.render = func(_ context.Context, c card) ([]byte, error) { drawn = append(drawn, c); return []byte("PNG"), nil }
			h := &host{}
			_ = m.Start(context.Background(), h)
			inv := &invocation{}
			e := otogi.Event{Platform: "qq", Conversation: otogi.Conversation{ID: "group:G1"}, SourceID: "m1", Action: otogi.ActionMessageCreated, Message: otogi.TextMessage("x.com/SpaceX/status/2112637394654822514")}
			if err := m.Handle(context.Background(), e, inv); err != nil {
				t.Fatal(err)
			}
			if len(inv.sent) != 1 || inv.keys[0] != "card:2112637394654822514" {
				t.Fatalf("%d cards sent: %v", len(inv.sent), inv.keys)
			}
			if req := lm.requests[0]; req.Model != "gemini-4-argon" || req.Thinking != "low" || !strings.Contains(req.Turns[0].Content.PlainText(), "<quote>Watch live") {
				t.Fatalf("request %+v", req)
			}
			card := drawn[0]
			if strings.HasPrefix(card.Text[0].Text, "我们") || strings.HasPrefix(card.Quote.Text[0].Text, "观看") {
				t.Fatalf("the original text was replaced: %+v", card.Text)
			}
			if c.translated {
				if tr := card.Translation; tr == nil || tr.By != "由 Gemini 翻译自英语" || tr.Text[0].Text != "我们 2026 年第三季度的财报现已公开 → " || card.Quote.Translation[0].Text != "观看直播" {
					t.Fatalf("translation on the card %+v, quote %+v", card.Translation, card.Quote.Translation)
				}
			} else if card.Translation != nil || card.Quote.Translation != nil {
				t.Fatalf("an untranslated card shows %+v", card.Translation)
			}
			var got string
			for _, r := range h.records {
				if r.Kind == "twitter.translation" {
					got = r.Outcome
				}
			}
			if got != c.record {
				t.Fatalf("translation record %q, want %q", got, c.record)
			}
		})
	}
}

func TestConfig(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		cfg  Config
		want string
	}{
		{Config{Chrome: chrome(t), Timezone: "Mars/Olympus"}, "timezone: unknown time zone"},
		{Config{}, "the path of chrome-headless-shell is required"},
		{Config{Chrome: chrome(t), Translate: &Translate{Provider: "openai", Model: "m"}}, "translate.provider: no provider openai"},
		{Config{Chrome: chrome(t), Translate: &Translate{Provider: "gemini"}}, "translate.model is required"},
	} {
		if _, err := New(c.cfg, noProviders); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v, want %q", c.cfg, err, c.want)
		}
	}
}

// The real renderer, when a Chrome is at hand (OTOGI_CHROME), on a card with
// every part: the card comes back as a PNG.
func TestRenderWithChrome(t *testing.T) {
	path := os.Getenv("OTOGI_CHROME")
	if path == "" {
		t.Skip("set OTOGI_CHROME to a chrome-headless-shell binary")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	views := int64(1234)
	tw := tweet{Author: author{Name: "名字 🔥", Handle: "someone", Badge: "blue"}, Text: "Cat’s @x #y https://example.com", Time: time.Now(), Views: &views,
		Media: []media{{Kind: "video", Seconds: 41}, {Kind: "photo"}, {Kind: "gif"}}, Sensitive: true, QuoteGone: "这个帖子不可用。",
		Poll: &poll{Choices: []choice{{"A", 60}, {"B", 40}}, Votes: 10, Ended: true}, Note: "context", ReplyingTo: "other"}
	png, err := render(ctx, cardimage.Renderer{Chrome: path}, newCard(tw, pictures{}, time.Local))
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

// The card in words carries the translation too, links in full.
func TestTextCardCarriesTheTranslation(t *testing.T) {
	t.Parallel()
	tw := tweet{Author: author{Name: "SpaceX", Handle: "SpaceX"}, Text: "Earnings are out → https://ir.spacex.com/q3-2026-results"}
	msg := textCard(tw, &translatedText{By: "由 Gemini 翻译自英语", Plain: "财报已发布 → https://ir.spacex.com/q3-2026-results"})
	if got := msg.PlainText(); got != "SpaceX @SpaceX\nEarnings are out → https://ir.spacex.com/q3-2026-results\n\n由 Gemini 翻译自英语：\n财报已发布 → https://ir.spacex.com/q3-2026-results" {
		t.Fatalf("text card %q", got)
	}
}

// A card the platform refuses for what it shows goes out again with its
// pictures covered; one with no pictures to cover fails as it was.
func TestRefusedCardGoesOutCovered(t *testing.T) {
	t.Parallel()
	m, err := New(Config{Chrome: chrome(t)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fixture := "fx-video.json"
	m.api, _ = newClient(t, func(string) (int, string) { return 200, fixture })
	var drawn []card
	m.render = func(_ context.Context, c card) ([]byte, error) { drawn = append(drawn, c); return []byte("PNG"), nil }
	h := &host{}
	_ = m.Start(context.Background(), h)
	e := otogi.Event{Platform: "qq", Conversation: otogi.Conversation{ID: "group:G1"}, SourceID: "m1", Action: otogi.ActionMessageCreated,
		Message: otogi.TextMessage("x.com/a/status/2118034216503955710")}
	inv := &invocation{refused: map[string]bool{"card:2118034216503955710": true}}
	if err := m.Handle(context.Background(), e, inv); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(inv.keys, []string{"card:2118034216503955710", "card:2118034216503955710:covered"}) || len(inv.sent) != 1 {
		t.Fatalf("sends %v, %d delivered", inv.keys, len(inv.sent))
	}
	if drawn[0].Media.Covered || !drawn[1].Media.Covered {
		t.Fatalf("covered %v then %v", drawn[0].Media.Covered, drawn[1].Media.Covered)
	}
	if r := h.records[len(h.records)-1]; r.Outcome != "degraded" || !strings.Contains(r.Payload.(map[string]any)["refused"].(string), "40034006") {
		t.Fatalf("record %+v", r)
	}

	fixture = "fx-quote.json"
	inv = &invocation{refused: map[string]bool{"card:2112637394654822514": true}}
	e.Message = otogi.TextMessage("x.com/SpaceX/status/2112637394654822514")
	if err := m.Handle(context.Background(), e, inv); !errors.Is(err, otogi.ErrRefused) || len(inv.keys) != 1 {
		t.Fatalf("a card with nothing to cover: %v, sends %v", err, inv.keys)
	}
}

// Sensitive media is blurred and marked; refused media is under the cover
// sticker: both as the card package draws them.
func TestCardMarksPicturesShownLessThanPlainly(t *testing.T) {
	t.Parallel()
	c := newCard(tweet{Author: author{Name: "a", Handle: "a"}, Media: []media{{Kind: "photo", Image: "p"}}, Sensitive: true}, pictures{"p": "data:image/png;base64,AA=="}, time.UTC)
	c.cover()
	var html bytes.Buffer
	if err := cardTemplate.Execute(&html, c); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{".blurred img:not(.cover)", `class="media n1 blurred covered"`, `<img class="cover sticker" src="data:image/webp;base64,`, "敏感内容"} {
		if !strings.Contains(html.String(), want) {
			t.Errorf("card lacks %q", want)
		}
	}
}
