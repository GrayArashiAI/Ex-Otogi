// Package twitter answers links to posts on X (Twitter) in allowlisted chats
// with a card: an image of the post laid out as X's own post page shows it,
// drawn from an HTML template by chrome-headless-shell. Posts come from
// FxTwitter's API, or from X's embed endpoint when FxTwitter fails.
package twitter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/cardimage"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Config is the module's section of otogi.yaml.
type Config struct {
	// Chrome is the chrome-headless-shell binary that draws the cards.
	Chrome string `yaml:"chrome"`
	// Timezone is where the cards' times are read, an IANA name such as
	// "Asia/Shanghai"; default the process's local time zone.
	Timezone string `yaml:"timezone"`
	// Translate, when set, follows the card of a post not in Chinese with a
	// second card, the post translated by a language model.
	Translate *Translate `yaml:"translate"`
}

// Module is the twitter module.
type Module struct {
	zone       *time.Location
	api        *client
	translator *translator // nil: no translated cards
	render     func(context.Context, card) ([]byte, error)
	host       otogi.Host
	now        func() time.Time
}

// New validates cfg and returns the module; providers finds the provider a
// translation names.
func New(cfg Config, providers func(string) (llm.Provider, error)) (*Module, error) {
	var errs []error
	if err := cardimage.Check(cfg.Chrome); err != nil {
		errs = append(errs, err)
	}
	zone := time.Local
	if cfg.Timezone != "" {
		var err error
		if zone, err = time.LoadLocation(cfg.Timezone); err != nil {
			errs = append(errs, fmt.Errorf("timezone: %w", err))
		}
	}
	var tr *translator
	if t := cfg.Translate; t != nil {
		p, err := providers(t.Provider)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("translate.provider: %w", err))
		case t.Model == "":
			errs = append(errs, errors.New("translate.model is required"))
		default:
			tr = &translator{cfg: *t, provider: p}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("twitter: %w", err)
	}
	renderer := cardimage.Renderer{Chrome: cfg.Chrome}
	return &Module{
		zone: zone, translator: tr,
		api:    &client{fx: "https://api.fxtwitter.com", syndication: "https://cdn.syndication.twimg.com", http: &http.Client{Timeout: 20 * time.Second}},
		render: func(ctx context.Context, c card) ([]byte, error) { return render(ctx, renderer, c) },
		now:    time.Now,
	}, nil
}

// Descriptor declares the module: people's new messages, in the chats on
// its allowlist.
func (*Module) Descriptor() otogi.ModuleDescriptor {
	return otogi.ModuleDescriptor{
		ID:         "twitter",
		Subscribes: []otogi.EventAction{otogi.ActionMessageCreated},
		Ordering:   otogi.OrderNone,
		Timeout:    5 * time.Minute, // a translation waits on a language model
		Allowlists: []otogi.AllowlistSpec{{Name: list, Title: "X", Description: "Links to posts on X get a card."}},
	}
}

// list is the module's allowlist.
const list = "twitter"

// Start keeps the host for records.
func (m *Module) Start(_ context.Context, host otogi.Host) error {
	m.host = host
	return nil
}

// Handle answers each post a message links with its card, as a reply.
func (m *Module) Handle(ctx context.Context, e otogi.Event, inv otogi.Invocation) error {
	if e.Command != nil || !m.host.Allowed(list, e.ChatKey()) {
		return nil
	}
	for _, id := range postsIn(e.Message) {
		if err := m.answer(ctx, e, inv, id); err != nil {
			return err
		}
	}
	return nil
}

// answer sends one post's card. A post that cannot be looked up gets no
// card; that is recorded and does not fail the message's other posts.
func (m *Module) answer(ctx context.Context, e otogi.Event, inv otogi.Invocation, id string) error {
	started := m.now()
	t, source, err := m.api.status(ctx, id)
	var gone *errGone
	switch {
	case errors.As(err, &gone):
		m.host.Record(ctx, otogi.Record{Kind: "twitter.card", Outcome: "skipped", Payload: map[string]any{"post": id, "source": source, "reason": gone.reason}})
		return nil
	case err != nil:
		m.host.Logger().Warn("twitter: look up failed", "post", id, "error", err)
		m.host.Record(ctx, otogi.Record{Kind: "twitter.card", Outcome: "error", Payload: map[string]any{"post": id, "step": "look up", "error": err.Error()}})
		return nil
	}
	fetched := m.now()
	pics, picErr := m.pictures(ctx, t)
	payload := map[string]any{"post": id, "source": source, "author": t.Author.Handle, "fetched": fetched.Sub(started).String()}
	if picErr != nil {
		// The card goes out with what it has; the missing pictures are noted.
		m.host.Logger().Warn("twitter: pictures not downloaded", "post", id, "error", picErr)
		payload["picture_error"] = picErr.Error()
	}
	png, err := m.render(ctx, newCard(t, pics, m.zone))
	payload["rendered"] = m.now().Sub(fetched).String()
	var msg otogi.Message
	if err != nil {
		m.host.Logger().Warn("twitter: card not rendered; sending text", "post", id, "error", err)
		payload["render_error"] = err.Error()
		msg = textCard(t)
	} else {
		payload["bytes"] = len(png)
		msg = otogi.Message{Roots: []otogi.PartID{"card"}, Parts: []otogi.Part{{ID: "card", Kind: otogi.PartMedia,
			Media: &otogi.Media{Role: otogi.MediaImage, MediaType: "image/png", ByteSize: int64(len(png)), Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: png}}}}}
	}
	msg.Relations = []otogi.Relation{{Kind: otogi.RelationReply, Target: e.Ref()}}
	if _, err := inv.Send(ctx, "card:"+id, msg); err != nil {
		return err
	}
	outcome := "ok"
	if payload["render_error"] != nil {
		outcome = "degraded"
	}
	m.host.Record(ctx, otogi.Record{Kind: "twitter.card", Outcome: outcome, Duration: m.now().Sub(started), Payload: payload})
	if m.translator != nil && t.Text != "" {
		return m.translated(ctx, e, inv, t, pics)
	}
	return nil
}

// translated sends the post's card again, translated, unless it is in
// Chinese. A failed translation is recorded and sends nothing.
func (m *Module) translated(ctx context.Context, e otogi.Event, inv otogi.Invocation, t tweet, pics pictures) error {
	started := m.now()
	tr, needed, err := m.translator.translate(ctx, t)
	payload := map[string]any{"post": t.ID, "model": m.translator.cfg.Model, "took": m.now().Sub(started).String()}
	switch {
	case err != nil:
		m.host.Logger().Warn("twitter: translation failed", "post", t.ID, "error", err)
		payload["error"] = err.Error()
		m.host.Record(ctx, otogi.Record{Kind: "twitter.translation", Outcome: "error", Payload: payload})
		return nil
	case !needed:
		m.host.Logger().Debug("twitter: post is in Chinese; no translation", "post", t.ID)
		return nil
	}
	payload["language"] = tr.Language
	t.Text = tr.Post
	if t.Quote != nil && tr.Quote != "" {
		q := *t.Quote
		q.Text = tr.Quote
		t.Quote = &q
	}
	c := newCard(t, pics, m.zone)
	c.Translated = "由 " + providerName(m.translator.cfg.Provider) + " 翻译自" + tr.Language
	var msg otogi.Message
	if png, err := m.render(ctx, c); err != nil {
		m.host.Logger().Warn("twitter: translated card not rendered; sending text", "post", t.ID, "error", err)
		payload["render_error"] = err.Error()
		msg = textCard(t)
	} else {
		msg = otogi.Message{Roots: []otogi.PartID{"card"}, Parts: []otogi.Part{{ID: "card", Kind: otogi.PartMedia,
			Media: &otogi.Media{Role: otogi.MediaImage, MediaType: "image/png", ByteSize: int64(len(png)), Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: png}}}}}
	}
	msg.Relations = []otogi.Relation{{Kind: otogi.RelationReply, Target: e.Ref()}}
	if _, err := inv.Send(ctx, "translation:"+t.ID, msg); err != nil {
		return err
	}
	outcome := "ok"
	if payload["render_error"] != nil {
		outcome = "degraded"
	}
	m.host.Record(ctx, otogi.Record{Kind: "twitter.translation", Outcome: outcome, Duration: m.now().Sub(started), Payload: payload})
	return nil
}

// providerName is a provider as a card names it: gemini is Gemini.
func providerName(p string) string {
	if p == "" {
		return p
	}
	return strings.ToUpper(p[:1]) + p[1:]
}

// pictures downloads what the card shows: avatars, and the media at the
// size its place on the card needs.
func (m *Module) pictures(ctx context.Context, t tweet) (pictures, error) {
	pics := pictures{}
	var errs []error
	get := func(src, size string) {
		if src == "" || pics[src] != "" {
			return
		}
		data, mime, err := m.api.picture(ctx, src, size)
		if err != nil {
			errs = append(errs, err)
			return
		}
		pics[src] = cardimage.DataURI(data, mime)
	}
	get(t.Author.Avatar, "")
	size := "small"
	if len(t.Media) == 1 {
		size = "medium"
	}
	for _, md := range t.Media[:min(len(t.Media), 4)] {
		get(md.Image, size)
	}
	if q := t.Quote; q != nil {
		get(q.Author.Avatar, "")
		for _, md := range q.Media[:min(len(q.Media), 4)] {
			get(md.Image, "small")
		}
	}
	return pics, errors.Join(errs...)
}

// textCard is the post in words, for when the card cannot be drawn. It
// holds no link: the message it answers has one, and QQ groups refuse
// messages with links.
func textCard(t tweet) otogi.Message {
	var r otogi.RichText
	r.Mark(otogi.AnnotationBold, "", func() { r.Write(t.Author.Name) })
	text, _ := excerpt(t.Text, maxText)
	r.Write(" @" + t.Author.Handle + "\n" + text)
	return r.Message()
}
