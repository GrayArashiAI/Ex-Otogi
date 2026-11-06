// Package bilibili answers Bilibili video links in allowlisted chats with a
// card: an image of the video's cover, title, uploader, counters, honors,
// and description, drawn from an HTML template by chrome-headless-shell.
package bilibili

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/cardimage"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Config is the module's section of otogi.yaml.
type Config struct {
	// Chats lists where links get cards, as /whoami prints them
	// ("telegram:channel:123", "qq:group:ABC").
	Chats []string `yaml:"chats"`
	// Chrome is the chrome-headless-shell binary that draws the cards.
	Chrome string `yaml:"chrome"`
}

// Module is the bilibili module.
type Module struct {
	chats  map[string]bool
	api    *client
	render func(context.Context, card) ([]byte, error)
	host   otogi.Host
	now    func() time.Time
}

// New validates cfg and returns the module.
func New(cfg Config) (*Module, error) {
	var errs []error
	if len(cfg.Chats) == 0 {
		errs = append(errs, errors.New("chats: at least one is required"))
	}
	chats := map[string]bool{}
	for _, c := range cfg.Chats {
		if platform, id, ok := strings.Cut(c, ":"); !ok || platform == "" || id == "" {
			errs = append(errs, fmt.Errorf("chats: %q: want <platform>:<chat id>, as /whoami prints it", c))
		}
		chats[c] = true
	}
	if err := cardimage.Check(cfg.Chrome); err != nil {
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("bilibili: %w", err)
	}
	return &Module{
		chats: chats,
		api:   &client{api: "https://api.bilibili.com", http: &http.Client{Timeout: 20 * time.Second}, now: time.Now},
		render: func(ctx context.Context, c card) ([]byte, error) {
			return render(ctx, cardimage.Renderer{Chrome: cfg.Chrome}, c)
		},
		now: time.Now,
	}, nil
}

// Descriptor declares the module: people's new messages.
func (*Module) Descriptor() otogi.ModuleDescriptor {
	return otogi.ModuleDescriptor{
		ID:         "bilibili",
		Subscribes: []otogi.EventAction{otogi.ActionMessageCreated},
		Ordering:   otogi.OrderNone,
		Timeout:    2 * time.Minute,
	}
}

// Start keeps the host for records.
func (m *Module) Start(_ context.Context, host otogi.Host) error {
	m.host = host
	return nil
}

// Handle answers each video a message names with its card, as a reply.
func (m *Module) Handle(ctx context.Context, e otogi.Event, inv otogi.Invocation) error {
	if e.Command != nil || !m.chats[string(e.Platform)+":"+string(e.Conversation.ID)] {
		return nil
	}
	answered := map[string]bool{} // a short link may name a video linked too
	for _, f := range videosIn(e.Message) {
		if err := m.answer(ctx, e, inv, f, answered); err != nil {
			return err
		}
	}
	return nil
}

// answer sends one video's card. A video that cannot be looked up gets no
// card; that is recorded and does not fail the message's other videos.
func (m *Module) answer(ctx context.Context, e otogi.Event, inv otogi.Invocation, f found, answered map[string]bool) error {
	started := m.now()
	link := f.short
	if f.id == "" {
		target, err := m.api.resolve(ctx, f.short)
		if err != nil {
			m.failed(ctx, link, "resolve", err)
			return nil
		}
		id, ok := idIn(target)
		if !ok {
			m.host.Logger().Debug("bilibili: short link is not a video", "link", link, "target", target)
			return nil
		}
		f.id = id
	}
	if answered[f.id] {
		return nil
	}
	answered[f.id] = true
	if link == "" {
		link = "https://www.bilibili.com/video/" + f.id
	}
	v, err := m.api.view(ctx, f.id)
	var apiErr *apiError
	switch {
	case errors.As(err, &apiErr) && apiErr.gone():
		m.host.Record(ctx, otogi.Record{Kind: "bilibili.card", Outcome: "skipped", Payload: map[string]any{"link": link, "video": f.id, "reason": "not found or not visible", "code": apiErr.Code, "message": apiErr.Message}})
		return nil
	case err != nil:
		m.failed(ctx, link, "look up", err)
		return nil
	}
	fetched := m.now()
	cover, coverErr := m.api.image(ctx, v.Pic, "@960w_540h_1c.webp")
	face, faceErr := m.api.image(ctx, v.Owner.Face, "@96w_96h_1c.webp")
	payload := map[string]any{"link": link, "video": v.BVID, "title": v.Title, "fetched": fetched.Sub(started).String()}
	if err := errors.Join(coverErr, faceErr); err != nil {
		// The card goes out with what it has; the missing picture is noted.
		m.host.Logger().Warn("bilibili: picture not downloaded", "video", v.BVID, "error", err)
		payload["picture_error"] = err.Error()
	}
	png, err := m.render(ctx, newCard(v, cover, face))
	payload["rendered"] = m.now().Sub(fetched).String()
	var msg otogi.Message
	if err != nil {
		m.host.Logger().Warn("bilibili: card not rendered; sending text", "video", v.BVID, "error", err)
		payload["render_error"] = err.Error()
		msg = textCard(v, cover)
	} else {
		payload["bytes"] = len(png)
		msg = imageMessage(png, "image/png")
	}
	msg.Relations = []otogi.Relation{{Kind: otogi.RelationReply, Target: e.Ref()}}
	if _, err := inv.Send(ctx, "card:"+v.BVID, msg); err != nil {
		return err
	}
	outcome := "ok"
	if payload["render_error"] != nil {
		outcome = "degraded"
	}
	m.host.Record(ctx, otogi.Record{Kind: "bilibili.card", Outcome: outcome, Duration: m.now().Sub(started), Payload: payload})
	return nil
}

func (m *Module) failed(ctx context.Context, link, step string, err error) {
	m.host.Logger().Warn("bilibili: "+step+" failed", "link", link, "error", err)
	m.host.Record(ctx, otogi.Record{Kind: "bilibili.card", Outcome: "error", Payload: map[string]any{"link": link, "step": step, "error": err.Error()}})
}

func imageMessage(data []byte, mime string) otogi.Message {
	return otogi.Message{Roots: []otogi.PartID{"card"}, Parts: []otogi.Part{{ID: "card", Kind: otogi.PartMedia,
		Media: &otogi.Media{Role: otogi.MediaImage, MediaType: mime, ByteSize: int64(len(data)), Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: data}}}}}
}

// textCard is the card in words, with the cover, for when it cannot be
// drawn. It holds no link: the message it answers has one, and QQ groups
// refuse messages with links.
func textCard(v video, cover image) otogi.Message {
	var t otogi.RichText
	t.Mark(otogi.AnnotationBold, "", func() { t.Write(v.Title) })
	t.Write(fmt.Sprintf("\n%s · %s · %s 发布\n%s播放 · %s弹幕 · %s点赞 · %s投币 · %s收藏",
		v.Owner.Name, duration(v.Duration), time.Unix(v.PubDate, 0).In(china).Format("2006-01-02"),
		count(v.Stat.View), count(v.Stat.Danmaku), count(v.Stat.Like), count(v.Stat.Coin), count(v.Stat.Favorite)))
	msg := t.Message()
	if len(cover.data) > 0 {
		img := imageMessage(cover.data, cover.mime)
		img.Parts[0].ID = "cover"
		msg.Parts = append(msg.Parts, img.Parts[0])
		msg.Roots = append(msg.Roots, "cover")
	}
	return msg
}
