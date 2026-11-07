// Package booth answers Booth item links in allowlisted chats with a card:
// an image of the item's first still picture, name, shop, price and its
// options, wishes, tags, and description, drawn from an HTML template by
// chrome-headless-shell.
package booth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/cardimage"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Config is the module's settings.
type Config struct {
	Chrome string `json:"chrome" minLength:"1" doc:"The path of the chrome-headless-shell binary that draws the cards."`
}

// Module is the booth module.
type Module struct {
	api    *client
	render func(context.Context, card) ([]byte, error)
	host   otogi.Host
	now    func() time.Time
}

// New validates cfg and returns the module.
func New(cfg Config) (*Module, error) {
	if err := cardimage.Check(cfg.Chrome); err != nil {
		return nil, fmt.Errorf("booth: %w", err)
	}
	return &Module{
		api: &client{site: "https://booth.pm", http: &http.Client{Timeout: 20 * time.Second}},
		render: func(ctx context.Context, c card) ([]byte, error) {
			return render(ctx, cardimage.Renderer{Chrome: cfg.Chrome}, c)
		},
		now: time.Now,
	}, nil
}

// Descriptor declares the module: people's new messages, in the chats on
// its allowlist.
func (*Module) Descriptor() otogi.ModuleDescriptor {
	return otogi.ModuleDescriptor{
		ID:         "booth",
		Subscribes: []otogi.EventAction{otogi.ActionMessageCreated},
		Ordering:   otogi.OrderNone,
		Timeout:    2 * time.Minute,
		Allowlists: []otogi.AllowlistSpec{{Name: list, Title: "Booth", Description: "Links to Booth items get a card."}},
	}
}

// list is the module's allowlist.
const list = "booth"

// Start keeps the host for records.
func (m *Module) Start(_ context.Context, host otogi.Host) error {
	m.host = host
	return nil
}

// Handle answers each item a message names with its card, as a reply.
func (m *Module) Handle(ctx context.Context, e otogi.Event, inv otogi.Invocation) error {
	if e.Command != nil || !m.host.Allowed(list, e.ChatKey()) {
		return nil
	}
	for _, id := range itemsIn(e.Message) {
		if err := m.answer(ctx, e, inv, id); err != nil {
			return err
		}
	}
	return nil
}

// answer sends one item's card. An item that cannot be looked up gets no
// card; that is recorded and does not fail the message's other items.
func (m *Module) answer(ctx context.Context, e otogi.Event, inv otogi.Invocation, id string) error {
	started := m.now()
	link := "https://booth.pm/ja/items/" + id
	it, err := m.api.item(ctx, id)
	switch {
	case errors.Is(err, errGone):
		m.host.Record(ctx, otogi.Record{Kind: "booth.card", Outcome: "skipped", Payload: map[string]any{"link": link, "item": id, "reason": "not found or not public"}})
		return nil
	case err != nil:
		m.host.Logger().Warn("booth: look up failed", "link", link, "error", err)
		m.host.Record(ctx, otogi.Record{Kind: "booth.card", Outcome: "error", Payload: map[string]any{"link": link, "item": id, "step": "look up", "error": err.Error()}})
		return nil
	}
	fetched := m.now()
	preview := it.preview()
	picture, pictureErr := m.api.image(ctx, preview)
	icon, iconErr := m.api.image(ctx, it.icon())
	payload := map[string]any{"link": link, "item": id, "title": it.Name, "shop": it.Shop.Name, "picture": preview, "adult": it.Adult, "fetched": fetched.Sub(started).String()}
	if err := errors.Join(pictureErr, iconErr); err != nil {
		// The card goes out with what it has; the missing picture is noted.
		m.host.Logger().Warn("booth: picture not downloaded", "item", id, "error", err)
		payload["picture_error"] = err.Error()
	}
	png, err := m.render(ctx, newCard(it, picture, icon))
	payload["rendered"] = m.now().Sub(fetched).String()
	var msg otogi.Message
	if err != nil {
		m.host.Logger().Warn("booth: card not rendered; sending text", "item", id, "error", err)
		payload["render_error"] = err.Error()
		msg = textCard(it, picture)
	} else {
		payload["bytes"] = len(png)
		msg = imageMessage(png, "image/png")
	}
	msg.Relations = []otogi.Relation{{Kind: otogi.RelationReply, Target: e.Ref()}}
	if _, err := inv.Send(ctx, "card:"+id, msg); err != nil {
		return err
	}
	outcome := "ok"
	if payload["render_error"] != nil {
		outcome = "degraded"
	}
	m.host.Record(ctx, otogi.Record{Kind: "booth.card", Outcome: outcome, Duration: m.now().Sub(started), Payload: payload})
	return nil
}

func imageMessage(data []byte, mime string) otogi.Message {
	return otogi.Message{Roots: []otogi.PartID{"card"}, Parts: []otogi.Part{{ID: "card", Kind: otogi.PartMedia,
		Media: &otogi.Media{Role: otogi.MediaImage, MediaType: mime, ByteSize: int64(len(data)), Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: data}}}}}
}

// textCard is the card in words, with the picture unless the item is
// R-18, for when it cannot be drawn. It holds no link: the message it
// answers has one, and QQ groups refuse messages with links.
func textCard(it item, picture image) otogi.Message {
	var t otogi.RichText
	t.Mark(otogi.AnnotationBold, "", func() { t.Write(it.Name) })
	t.Write(fmt.Sprintf("\n%s · %s · ♥%d", it.Shop.Name, price(it), it.Wishes))
	if it.Adult {
		t.Write(" · R-18")
	}
	msg := t.Message()
	if len(picture.data) > 0 && !it.Adult {
		img := imageMessage(picture.data, picture.mime)
		img.Parts[0].ID = "picture"
		msg.Parts = append(msg.Parts, img.Parts[0])
		msg.Roots = append(msg.Roots, "picture")
	}
	return msg
}
