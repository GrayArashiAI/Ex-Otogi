// Package media describes the images, animations, and videos people send
// in the chats on its describe list, once per piece of content, and
// provides the descriptions as the service contract.Service. It describes
// in its own Handle, as messages arrive; other modules only wait for or
// look up the result. A picture whose driver gives its digest (QQ's MD5) is
// known again without fetching it, unless its original is to be kept.
//
// With pictures set, it keeps the pictures and animations it describes, for
// Examine, which answers an agent's question about a piece of media: from
// the original it keeps, or the bytes the agent fetched again.
//
// Operators keep stickers with /sticker: their pictures, kept in the
// module's database, are what agents may send, provided as the service
// contract.Stickers.
package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	contract "github.com/TundraWork/Ex-Otogi/pkg/media"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

const (
	// describeList names the allowlist of chats whose media is described.
	describeList = "describe"
	// maxBytes is the largest media described. It bounds memory, not an
	// API limit (Gemini took inline videos of 103 MiB): describing holds
	// the media about 2.3 times over (raw and base64), and Handle runs
	// unordered, so several can be described at once. eku's 12 QQ videos
	// ran 11 to 67 seconds, about 1.7 MB per 21 seconds, so 25 MiB is
	// about four minutes of QQ video and three at once under 200 MB.
	maxBytes = 25 << 20
	// retention is how long a description is kept after the media was last
	// seen: as long as the chat module keeps its transcript by default.
	retention = 30 * 24 * time.Hour
	// handleTimeout bounds describing one message's media: a video takes
	// tens of seconds, and each step may retry.
	handleTimeout = 2 * time.Minute
)

// Module is the media module. Once started it is the media service.
type Module struct {
	steps     []step
	keep      int64 // the most the pictures kept may take, 0 when none are kept
	limits    stickerLimits
	language  string
	operators func() []string
	store     *store
	host      otogi.Host
	waits     *waits
	now       func() time.Time
}

// New resolves the steps' providers and builds the module. operators returns
// the platform-qualified actor IDs ("telegram:user:123") who may manage
// stickers, as they are now. It does no I/O.
func New(cfg Config, providers func(string) (llm.Provider, error), operators func() []string) (*Module, error) {
	steps, err := cfg.steps(providers)
	if err != nil {
		return nil, err
	}
	language := strings.TrimSpace(cfg.Language)
	if language == "" {
		language = defaultLanguage
	}
	return &Module{steps: steps, keep: cfg.keepBytes(), limits: stickerLimits{count: maxStickers, total: maxStickerTotal}, language: language, operators: operators, waits: newWaits(time.Now), now: time.Now}, nil
}

// Descriptor declares the module: it describes the media of people's new
// messages in the chats on its describe list, unordered; keeps stickers
// with /sticker; and provides the media and stickers services.
func (*Module) Descriptor() otogi.ModuleDescriptor {
	return otogi.ModuleDescriptor{
		ID:         "media",
		Subscribes: []otogi.EventAction{otogi.ActionMessageCreated},
		Ordering:   otogi.OrderNone,
		Timeout:    handleTimeout,
		Filter:     otogi.Filter{Directions: []otogi.Direction{otogi.DirectionIncoming}},
		Provides:   []string{contract.Service, contract.StickersService},
		Commands:   []otogi.CommandSpec{{Name: stickerCommand, Usage: "add [note] | list | note <n> [note] | remove <n>", Description: "manage the stickers agents may send (operators only)"}},
		Allowlists: []otogi.AllowlistSpec{{Name: describeList, Title: "Describe media", Description: "Images, animations, and videos in these chats are sent to the configured models and described."}},
	}
}

// Start opens the database, provides the media and stickers services, and
// schedules pruning.
func (m *Module) Start(ctx context.Context, host otogi.Host) error {
	db, err := host.Database(ctx)
	if err != nil {
		return err
	}
	if m.store, err = openStore(ctx, db); err != nil {
		return err
	}
	m.host = host
	if err := host.Provide(contract.Service, contract.Describer(m)); err != nil {
		return err
	}
	if err := host.Provide(contract.StickersService, contract.Stickers(stickers{m})); err != nil {
		return err
	}
	return host.Schedule("prune", "@every 1h", m.prune)
}

// prune deletes what is kept past its time, and the pictures kept whose
// descriptions are gone or that are past the pictures' limit.
func (m *Module) prune(ctx context.Context, _ otogi.Invocation) error {
	now := m.now()
	n, err := m.store.prune(ctx, now.Add(-retention), now.Add(-recentMedia))
	if err != nil {
		return err
	}
	originals, err := m.store.pruneOriginals(ctx, m.keep)
	if err != nil {
		return err
	}
	m.host.Logger().Debug("media: pruned", "rows", n, "originals", originals)
	return nil
}

// Handle runs /sticker, and describes each piece of media of a message in
// a chat on the describe list, keeping it for recentMedia so /sticker add
// can reply to it. A step that fails is recorded as media.failed and not
// returned, so it is not recorded twice; fetching and storage errors are
// returned.
func (m *Module) Handle(ctx context.Context, e otogi.Event, inv otogi.Invocation) error {
	if e.Command != nil && e.Command.Name == stickerCommand {
		return m.runSticker(ctx, e, inv)
	}
	if !m.host.Allowed(describeList, e.ChatKey()) {
		return nil
	}
	media := mediaOf(e.Message)
	if len(media) == 0 {
		return nil
	}
	var errs []error
	if e.SourceID != "" {
		if err := m.store.putRecent(ctx, e.Destination(), string(e.SourceID), media, m.now()); err != nil {
			errs = append(errs, err)
		}
	}
	for _, md := range media {
		if err := m.handleOne(ctx, e, inv, md); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// mediaOf returns the media of msg.
func mediaOf(msg otogi.Message) []otogi.Media {
	var out []otogi.Media
	for _, p := range msg.Parts {
		if p.Kind == otogi.PartMedia && p.Media != nil {
			out = append(out, *p.Media)
		}
	}
	return out
}

// handleOne describes md unless it is kept already. However it ends,
// returning an error, timing out, or panicking, it hands its result to
// whoever is waiting in Describe.
func (m *Module) handleOne(ctx context.Context, e otogi.Event, inv otogi.Invocation, md otogi.Media) error {
	from, chat := e.Destination(), e.ChatKey()
	k, src := mediaKey(md)
	// Waiters know md by this key, not by the digest it gets once fetched.
	wk := waitKey(k, src)
	res := result{err: fmt.Errorf("media: not described: %w", otogi.ErrUnavailable)}
	defer func() { m.waits.finish(wk, res) }()

	if !describable(md) {
		m.skip(ctx, chat, md, md.ByteSize, "not described")
		res.err = contract.ErrNotDescribed
		return nil
	}
	if md.ByteSize > maxBytes {
		m.skip(ctx, chat, md, md.ByteSize, "too large")
		res.err = contract.ErrNotDescribed
		return nil
	}
	if k.Algorithm != "" {
		if d, ok, err := m.kept(ctx, chat, k); err != nil || ok {
			res = result{d: d, err: err}
			if err != nil || !m.keepsOriginal(d, md) {
				return err
			}
			m.waits.finish(wk, res) // the description first: keeping the picture does not hold it up
			return m.keepAgain(ctx, inv, from, chat, k, md, nil)
		}
	}
	data := md.Source.Inline
	if md.Source.Kind != otogi.MediaSourceInline {
		var err error
		if data, err = inv.FetchMedia(ctx, from, md.Source); err != nil {
			return fmt.Errorf("media: fetch: %w", err)
		}
	}
	if len(data) > maxBytes {
		m.skip(ctx, chat, md, int64(len(data)), "too large")
		res.err = contract.ErrNotDescribed
		return nil
	}
	if k.Algorithm == "" {
		sum := sha256.Sum256(data)
		k = key{Algorithm: "sha256", Digest: sum[:]}
		if err := m.store.putSource(ctx, src, k, m.now()); err != nil {
			return err
		}
		if d, ok, err := m.kept(ctx, chat, k); err != nil || ok {
			res = result{d: d, err: err}
			if err != nil || !m.keepsOriginal(d, md) {
				return err
			}
			m.waits.finish(wk, res)
			return m.keepAgain(ctx, inv, from, chat, k, md, data)
		}
	}
	p, err := m.prepare(md, data)
	if err != nil {
		reason := "not described"
		if errors.Is(err, errTooLarge) {
			reason = "too large"
		}
		m.skip(ctx, chat, md, int64(len(data)), reason)
		res.err = contract.ErrNotDescribed
		return nil
	}
	d, model, err := m.describe(ctx, chat, k, p, len(data))
	if err != nil {
		if errors.Is(err, otogi.ErrUnavailable) {
			res.err = err
			return nil // each failure is recorded as media.failed
		}
		return err
	}
	if err := m.store.put(ctx, k, d, model, m.now()); err != nil {
		return err
	}
	res = result{d: withID(d, k)}
	if m.keep == 0 || p.video {
		return nil
	}
	m.waits.finish(wk, res)
	return m.store.keepOriginal(ctx, k, chat, http.DetectContentType(data), data, m.now())
}

// keepsOriginal reports whether md, described as d, has its original kept:
// a picture or animation, not a kept sticker, while pictures are kept.
func (m *Module) keepsOriginal(d contract.Description, md otogi.Media) bool {
	return m.keep > 0 && d.Sticker == 0 && md.Role != otogi.MediaVideo
}

// keepAgain keeps the original of md, described under k already, as seen in
// chat now: marked seen when it is kept, or else kept, fetched unless data
// has it. Content that turns out not to be a picture is not kept.
func (m *Module) keepAgain(ctx context.Context, inv otogi.Invocation, from otogi.Destination, chat string, k key, md otogi.Media, data []byte) error {
	if kept, err := m.store.seenOriginal(ctx, k, chat, m.now()); err != nil || kept {
		return err
	}
	if data == nil {
		data = md.Source.Inline
		if md.Source.Kind != otogi.MediaSourceInline {
			var err error
			if data, err = inv.FetchMedia(ctx, from, md.Source); err != nil {
				return fmt.Errorf("media: fetch to keep: %w", err)
			}
		}
	}
	mediaType := http.DetectContentType(data)
	if len(data) > maxBytes || !strings.HasPrefix(mediaType, "image/") {
		return nil
	}
	return m.store.keepOriginal(ctx, k, chat, mediaType, data, m.now())
}

// withID gives d, described under k, its ID: none for a kept sticker.
func withID(d contract.Description, k key) contract.Description {
	if d.Sticker == 0 && len(k.Digest) >= 3 {
		d.ID = hex.EncodeToString(k.Digest[:3])
	}
	return d
}

// kept returns what is kept of the media under k, seen in chat from: the
// sticker it is, recorded as described from the sticker since no model was
// asked, or else its description; either way its description is marked
// seen.
func (m *Module) kept(ctx context.Context, from string, k key) (contract.Description, bool, error) {
	d, ok, err := m.stickerOf(ctx, k)
	if err != nil {
		return d, false, err
	}
	if ok {
		m.host.Record(ctx, otogi.Record{Kind: "media.described", Outcome: "ok", Payload: map[string]any{
			"from": from, "key": k.String(), "sticker": d.Sticker, "kind": d.Kind, "summary": d.Summary, "text": d.Text}})
	} else if d, ok, err = m.store.get(ctx, k); err != nil || !ok {
		return d, ok, err
	}
	return withID(d, k), true, m.store.seen(ctx, k, m.now())
}

// stickerOf returns the kept sticker under k as a description: its number
// and its text, which is read each time, so a note applies at once, with the
// kind and text of the media's description while there is one, else the kind
// it was kept with. A sticker with neither a note nor a summary is none here:
// it is described like any picture, and known once it has a note.
func (m *Module) stickerOf(ctx context.Context, k key) (contract.Description, bool, error) {
	st, ok, err := m.store.stickerByKey(ctx, k)
	if err != nil || !ok || st.text() == "" {
		return contract.Description{}, false, err
	}
	d := contract.Description{Kind: st.Kind, Summary: st.text(), Sticker: st.ID}
	described, ok, err := m.store.get(ctx, k)
	if err != nil {
		return contract.Description{}, false, err
	}
	if ok {
		d.Kind, d.Text = described.Kind, described.Text
	}
	return d, true, nil
}

// Describe returns the description of md, waiting until ctx ends for
// Handle to finish with it; see contract.Describer.
func (m *Module) Describe(ctx context.Context, from otogi.Destination, md otogi.Media) (contract.Description, error) {
	if !m.host.Allowed(describeList, chatKey(from)) {
		return contract.Description{}, contract.ErrNotAllowed
	}
	if !describable(md) || md.ByteSize > maxBytes {
		return contract.Description{}, contract.ErrNotDescribed
	}
	if d, ok, err := m.Lookup(ctx, from, md); err != nil || ok {
		return d, err
	}
	k, src := mediaKey(md)
	r, err := m.waits.wait(ctx, waitKey(k, src))
	if err != nil {
		return contract.Description{}, err
	}
	return r.d, r.err
}

// Lookup returns the kept description of md without waiting; see
// contract.Describer.
func (m *Module) Lookup(ctx context.Context, from otogi.Destination, md otogi.Media) (contract.Description, bool, error) {
	if !m.host.Allowed(describeList, chatKey(from)) {
		return contract.Description{}, false, nil
	}
	k, src := mediaKey(md)
	if k.Algorithm == "" {
		if src == "" {
			return contract.Description{}, false, nil
		}
		var ok bool
		var err error
		if k, ok, err = m.store.source(ctx, src); err != nil || !ok {
			return contract.Description{}, false, err
		}
	}
	if d, ok, err := m.stickerOf(ctx, k); err != nil || ok {
		return d, ok, err
	}
	d, ok, err := m.store.get(ctx, k)
	if err != nil || !ok {
		return d, ok, err
	}
	return withID(d, k), true, nil
}

// describable reports whether md is a kind of media that is described:
// an image, sticker, animation, or video whose type, when known, is one.
func describable(md otogi.Media) bool {
	switch md.Role {
	case otogi.MediaImage, otogi.MediaSticker, otogi.MediaAnimation, otogi.MediaVideo:
	default:
		return false
	}
	return md.MediaType == "" || strings.HasPrefix(md.MediaType, "image/") || strings.HasPrefix(md.MediaType, "video/")
}

// mediaKey is how md is known: by the first digest its driver gave, or the
// SHA-256 of inline bytes; otherwise by where it came from, src, until it
// is fetched.
func mediaKey(md otogi.Media) (key, string) {
	if len(md.Hashes) > 0 && len(md.Hashes[0].Value) > 0 {
		return key{Algorithm: md.Hashes[0].Algorithm, Digest: md.Hashes[0].Value}, ""
	}
	if md.Source.Kind == otogi.MediaSourceInline {
		sum := sha256.Sum256(md.Source.Inline)
		return key{Algorithm: "sha256", Digest: sum[:]}, ""
	}
	if md.Source.URL != "" {
		return key{}, "url:" + md.Source.URL
	}
	locator, _ := json.Marshal(md.Source.Locator)
	return key{}, "locator:" + string(locator)
}

// waitKey is what Handle and Describe agree to call md by.
func waitKey(k key, src string) string {
	if k.Algorithm != "" {
		return k.String()
	}
	return src
}

// chatKey is a destination's chat, as allowlists name it.
func chatKey(d otogi.Destination) string { return string(d.Platform) + ":" + string(d.Conversation) }
