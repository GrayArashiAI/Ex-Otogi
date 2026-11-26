package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	contract "github.com/TundraWork/Ex-Otogi/pkg/media"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
	"github.com/TundraWork/Ex-Otogi/pkg/webpanim"
)

const (
	// stickerCommand is the command operators manage stickers with.
	stickerCommand = "sticker"
	// maxStickerBytes keeps a sticker within what QQ takes in one upload,
	// as Otogi's QQ driver uploads: the test bot's /files took 10,000,500
	// bytes and refused 10,485,000 (40093011, too large), and the driver has
	// no chunked upload.
	maxStickerBytes = 9 << 20
	// listedStickers is the most stickers /sticker list shows, so that a
	// large collection does not flood the chat; the control panel shows
	// them all.
	listedStickers = 50
	// recentMedia is how long a message's media is kept for /sticker add
	// replying to it: QQ's media URLs last about an hour.
	recentMedia = 2 * time.Hour
	// describeSticker is how long adding a sticker waits for its
	// description.
	describeSticker = 30 * time.Second
)

// stickerTypes are the picture types kept, by content, each kept and sent
// as it is, so a platform that knows a picture by its file's digest, as QQ
// does, knows it when someone posts it again. Both drivers send them as
// pictures: Telegram's GIFs are MP4 files, and a WebP sent on Telegram goes
// as a photo, its transparency made solid, which is untried. An animated
// WebP is not kept: QQ refuses its upload, so its driver sends it as a GIF,
// another file than the one kept, and a sticker someone posts again would
// not be known by its MD5.
var stickerTypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}

const stickerUsage = "Usage: /sticker add [note] (replying to a picture, or with one) | list | note <n> [note] | remove <n>"

// runSticker runs /sticker and replies with how it went.
func (m *Module) runSticker(ctx context.Context, e otogi.Event, inv otogi.Invocation) error {
	reply, err := m.sticker(ctx, e, inv)
	if err != nil {
		reply = strings.TrimSpace(reply + "\nSomething went wrong; see the event log.")
	}
	_, sendErr := inv.Send(ctx, "reply", otogi.Reply(reply, e.Ref()))
	return errors.Join(err, sendErr)
}

func (m *Module) sticker(ctx context.Context, e otogi.Event, inv otogi.Invocation) (string, error) {
	if e.Actor.ID == "" || !slices.Contains(m.operators(), e.ActorKey()) {
		return fmt.Sprintf("Only operators can manage stickers. Your ID is %s.", e.ActorKey()), nil
	}
	if e.Conversation.Kind != otogi.ConversationDirect && !m.host.Allowed(describeList, e.ChatKey()) {
		return "This chat is not on media's describe list.", nil
	}
	argv := e.Command.Argv
	if len(argv) == 0 {
		return stickerUsage, nil
	}
	switch strings.ToLower(argv[0]) {
	case "add":
		return m.addStickers(ctx, e, inv, after(e.Command.Args, 1))
	case "list":
		return m.listStickers(ctx)
	case "note", "remove":
		if len(argv) < 2 {
			return stickerUsage, nil
		}
		id, err := strconv.Atoi(argv[1])
		if err != nil {
			return stickerUsage, nil
		}
		if strings.ToLower(argv[0]) == "note" {
			return m.noteSticker(ctx, e, id, after(e.Command.Args, 2))
		}
		return m.removeSticker(ctx, e, id)
	}
	return stickerUsage, nil
}

// after returns args without its first n words.
func after(args string, n int) string {
	for range n {
		args = strings.TrimLeftFunc(args, unicode.IsSpace)
		i := strings.IndexFunc(args, unicode.IsSpace)
		if i < 0 {
			return ""
		}
		args = args[i:]
	}
	return strings.TrimSpace(args)
}

// addStickers keeps the pictures of e's message, or of the message it
// replies to, with note.
func (m *Module) addStickers(ctx context.Context, e otogi.Event, inv otogi.Invocation, note string) (string, error) {
	media := mediaOf(e.Message)
	if len(media) == 0 {
		target, ok := replyTarget(e.Message)
		if !ok {
			return "Reply to a picture with /sticker add, or send the picture with the command.", nil
		}
		found, ok, err := m.store.recent(ctx, e.Destination(), string(target.SourceEvent), m.now().Add(-recentMedia))
		if err != nil {
			return "", err
		}
		if !ok {
			return "I can't find a picture in that message any more; send it again and add it right away.", nil
		}
		media = found
	}
	var lines []string
	var errs []error
	for _, md := range media {
		line, err := m.addSticker(ctx, e, inv, md, note)
		lines = append(lines, line)
		errs = append(errs, err)
	}
	return strings.Join(lines, "\n"), errors.Join(errs...)
}

// addSticker keeps one picture, md, with note.
func (m *Module) addSticker(ctx context.Context, e otogi.Event, inv otogi.Invocation, md otogi.Media, note string) (string, error) {
	if md.ByteSize > maxStickerBytes {
		return m.refused(ctx, &e, map[string]any{"action": "add", "by": e.ActorKey(), "bytes": md.ByteSize}, tooLarge(md.ByteSize)), nil
	}
	data := md.Source.Inline
	if md.Source.Kind != otogi.MediaSourceInline {
		var err error
		if data, err = inv.FetchMedia(ctx, e.Destination(), md.Source); err != nil {
			payload := map[string]any{"action": "add", "by": e.ActorKey(), "error": err.Error()}
			return m.refused(ctx, &e, payload, &contract.StickerRefusal{Reason: "gone", Message: "Couldn't fetch that picture any more; send it again and add it right away."}), nil
		}
	}
	st, _, err := m.keepSticker(ctx, md, data, note, "", e.ActorKey(), &e)
	var refusal *contract.StickerRefusal
	switch {
	case errors.As(err, &refusal):
		return refusal.Message, nil
	case err != nil:
		return "", err
	case st.text() == "":
		return fmt.Sprintf("Kept as sticker %d, but it has no description; give it a note with /sticker note %d <note> so agents can use it.", st.ID, st.ID), nil
	}
	return fmt.Sprintf("Kept as sticker %d: %s", st.ID, st.text()), nil
}

// tooLarge refuses a picture of size bytes.
func tooLarge(size int64) *contract.StickerRefusal {
	return &contract.StickerRefusal{Reason: "too large", Message: fmt.Sprintf("That picture is %.1f MiB; a sticker can be at most %d MiB.", float64(size)/(1<<20), maxStickerBytes>>20)}
}

// refused records a picture not kept, with payload, and returns the reply.
func (m *Module) refused(ctx context.Context, e *otogi.Event, payload map[string]any, r *contract.StickerRefusal) string {
	payload["reason"] = r.Reason
	if r.ID != 0 {
		payload["id"] = r.ID
	}
	m.recordSticker(ctx, e, "skipped", payload)
	return r.Message
}

// keepSticker keeps data, a picture, with note, and puts it in pack unless
// pack is empty: what /sticker add and the management plane share, the
// checks, the description, and the record. by is who keeps it; e is the
// command's event, nil from the management plane; md is what the message
// said of the picture, which may name a description kept already. A
// picture not kept is a *contract.StickerRefusal, recorded. One kept
// already is refused when pack is empty, and otherwise put in pack and
// returned with existing; a pack that is not there is contract.ErrNoPack,
// found before the picture is described.
func (m *Module) keepSticker(ctx context.Context, md otogi.Media, data []byte, note, pack, by string, e *otogi.Event) (st sticker, existing bool, err error) {
	payload := map[string]any{"action": "add", "by": by}
	if pack != "" {
		payload["pack"] = pack
	}
	refuse := func(r *contract.StickerRefusal) (sticker, bool, error) {
		m.refused(ctx, e, payload, r)
		return sticker{}, false, r
	}
	payload["bytes"] = len(data)
	if len(data) > maxStickerBytes {
		return refuse(tooLarge(int64(len(data))))
	}
	mediaType := http.DetectContentType(data)
	if !slices.Contains(stickerTypes, mediaType) {
		payload["media_type"] = mediaType
		return refuse(&contract.StickerRefusal{Reason: "not png, jpeg, gif, or webp", Message: "Only PNG, JPEG, GIF, and WebP pictures can be kept."})
	}
	if mediaType == "image/webp" {
		switch animated, short := webpanim.Animated(data); {
		case short:
			payload["media_type"] = mediaType
			return refuse(&contract.StickerRefusal{Reason: "not png, jpeg, gif, or webp", Message: "Only PNG, JPEG, GIF, and WebP pictures can be kept."})
		case animated:
			payload["media_type"] = mediaType
			return refuse(&contract.StickerRefusal{Reason: "animated webp", Message: "Animated WebP pictures cannot be kept: keep it as a GIF instead."})
		}
	}
	sum := sha256.Sum256(data)
	payload["sha256"] = hex.EncodeToString(sum[:])
	alreadyKept := func(id int) (sticker, bool, error) {
		return refuse(&contract.StickerRefusal{Reason: "already kept", Message: fmt.Sprintf("Already kept as sticker %d.", id), ID: id})
	}
	// packed returns sticker id, kept already and now in pack.
	packed := func(id int) (sticker, bool, error) {
		st, ok, err := m.store.sticker(ctx, id)
		if err == nil && !ok {
			err = contract.ErrNoSticker // removed meanwhile
		}
		return st, err == nil, err
	}
	id, ok, err := m.store.stickerByDigest(ctx, sum[:])
	switch {
	case err != nil:
		return sticker{}, false, err
	case ok && pack == "":
		return alreadyKept(id)
	case ok:
		if err := m.packSticker(ctx, pack, id, true, by, e); err != nil {
			return sticker{}, false, err
		}
		return packed(id)
	}
	// Checked before describing, so a picture for a pack that is not there
	// costs no model call; the pack is checked again as the picture is
	// written.
	if pack != "" {
		if err := packThere(ctx, m.store.db, pack); err != nil {
			return sticker{}, false, err
		}
	}
	from := ""
	if e != nil {
		from = e.ChatKey()
	}
	d, err := m.stickerDescription(ctx, from, md, data, sum[:])
	if err != nil {
		return sticker{}, false, err
	}
	now := m.now()
	st = sticker{SHA256: sum[:], MediaType: mediaType, Bytes: data, Size: int64(len(data)), Kind: d.Kind, Summary: d.Summary, Note: note, AddedBy: by, AddedAt: now}
	// The same picture kept twice at once: the second finds the first here.
	id, existed, joined, err := m.store.addSticker(ctx, st, pack, by, now)
	switch {
	case err != nil:
		return sticker{}, false, err
	case existed && pack == "":
		return alreadyKept(id)
	case existed:
		st, ok, err := packed(id)
		if ok && joined { // recorded when it changed anything, as Pack is
			payload := stickerPayload("pack", st, by)
			payload["pack"] = pack
			m.recordSticker(ctx, e, "ok", payload)
		}
		return st, ok, err
	}
	st.ID = id
	if pack != "" {
		st.Packs = []string{pack}
	}
	payload["id"], payload["kind"], payload["text"], payload["media_type"] = id, st.Kind, st.text(), mediaType
	m.recordSticker(ctx, e, "ok", payload)
	return st, false, nil
}

// stickerDescription returns the description of a picture being kept: the
// one kept for md or its bytes, else one made now, waiting at most
// describeSticker. A picture that is not described has an empty
// description; only storage errors are returned.
func (m *Module) stickerDescription(ctx context.Context, from string, md otogi.Media, data, sum []byte) (contract.Description, error) {
	k, src := mediaKey(md)
	if k.Algorithm == "" && src != "" {
		found, ok, err := m.store.source(ctx, src)
		if err != nil {
			return contract.Description{}, err
		}
		if ok {
			k = found
		}
	}
	byBytes := key{Algorithm: "sha256", Digest: sum}
	for _, known := range []key{k, byBytes} {
		if known.Algorithm == "" {
			continue
		}
		if d, ok, err := m.store.get(ctx, known); err != nil || ok {
			return d, err
		}
	}
	if k.Algorithm == "" {
		k = byBytes
	}
	p, err := m.prepare(md, data)
	if err != nil {
		return contract.Description{}, nil
	}
	dctx, cancel := context.WithTimeout(ctx, describeSticker)
	defer cancel()
	d, model, err := m.describe(dctx, from, k, p, len(data))
	if err != nil {
		return contract.Description{}, nil // each failure is recorded as media.failed
	}
	return d, m.store.put(ctx, k, d, model, m.now())
}

func (m *Module) listStickers(ctx context.Context) (string, error) {
	all, err := m.store.stickers(ctx)
	if err != nil {
		return "", err
	}
	if len(all) == 0 {
		return "No stickers yet. Reply to a picture with /sticker add to keep it.", nil
	}
	var total int64
	for _, st := range all {
		total += st.Size
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d kept, %.1f MiB:", len(all), float64(total)/(1<<20))
	for _, st := range all[:min(len(all), listedStickers)] {
		kind, text := string(st.Kind), st.text()
		if kind == "" {
			kind = "-"
		}
		if text == "" {
			text = "(no description; agents don't see it until it has a note)"
		}
		fmt.Fprintf(&b, "\n%d. [%s] %s", st.ID, kind, text)
	}
	if more := len(all) - listedStickers; more > 0 {
		fmt.Fprintf(&b, "\n…and %d more; see the control panel.", more)
	}
	return b.String(), nil
}

func (m *Module) noteSticker(ctx context.Context, e otogi.Event, id int, note string) (string, error) {
	st, err := m.setNote(ctx, id, note, e.ActorKey(), &e)
	switch {
	case errors.Is(err, contract.ErrNoSticker):
		return fmt.Sprintf("There is no sticker %d.", id), nil
	case err != nil:
		return "", err
	case st.text() == "":
		return fmt.Sprintf("Sticker %d has no note or description now; agents don't see it.", id), nil
	}
	return fmt.Sprintf("Sticker %d now shows: %s", id, st.text()), nil
}

func (m *Module) removeSticker(ctx context.Context, e otogi.Event, id int) (string, error) {
	err := m.remove(ctx, id, e.ActorKey(), &e)
	switch {
	case errors.Is(err, contract.ErrNoSticker):
		return fmt.Sprintf("There is no sticker %d.", id), nil
	case err != nil:
		return "", err
	}
	return fmt.Sprintf("Removed sticker %d.", id), nil
}

// setNote sets sticker id's note, by by, on e when a command did it.
func (m *Module) setNote(ctx context.Context, id int, note, by string, e *otogi.Event) (sticker, error) {
	st, ok, err := m.store.setStickerNote(ctx, id, note)
	switch {
	case err != nil:
		return sticker{}, err
	case !ok:
		return sticker{}, contract.ErrNoSticker
	}
	m.recordSticker(ctx, e, "ok", stickerPayload("note", st, by))
	return st, nil
}

// remove removes sticker id, by by, on e when a command did it.
func (m *Module) remove(ctx context.Context, id int, by string, e *otogi.Event) error {
	st, ok, err := m.store.removeSticker(ctx, id)
	switch {
	case err != nil:
		return err
	case !ok:
		return contract.ErrNoSticker
	}
	m.recordSticker(ctx, e, "ok", stickerPayload("remove", st, by))
	return nil
}

// recordSticker records a sticker's change as media.sticker: on e when a
// command made it, on no event when the management plane did.
func (m *Module) recordSticker(ctx context.Context, e *otogi.Event, outcome string, payload map[string]any) {
	r := otogi.Record{Kind: "media.sticker", Outcome: outcome, Payload: payload}
	if e != nil {
		r.Event, r.Platform, r.Account, r.Conversation = e.ID, e.Platform, e.Account, e.Conversation.ID
	}
	m.host.Record(ctx, r)
}

// replyTarget returns the message msg replies to.
func replyTarget(msg otogi.Message) (otogi.EventRef, bool) {
	for _, r := range msg.Relations {
		if r.Kind == otogi.RelationReply {
			return r.Target, true
		}
	}
	return otogi.EventRef{}, false
}

// stickers is the stickers service: the module's stickers for agents.
type stickers struct{ m *Module }

// List returns the stickers that have a note or a description.
func (s stickers) List(ctx context.Context) ([]contract.Sticker, error) {
	all, err := s.m.store.stickers(ctx)
	if err != nil {
		return nil, err
	}
	var out []contract.Sticker
	for _, st := range all {
		if st.text() != "" {
			out = append(out, st.listed())
		}
	}
	return out, nil
}

// Picture returns sticker id's picture as inline media.
func (s stickers) Picture(ctx context.Context, id int) (otogi.Media, contract.Sticker, error) {
	st, ok, err := s.m.store.stickerPicture(ctx, id)
	if err != nil {
		return otogi.Media{}, contract.Sticker{}, err
	}
	if !ok {
		return otogi.Media{}, contract.Sticker{}, contract.ErrNoSticker
	}
	role, ext := otogi.MediaImage, strings.TrimPrefix(st.MediaType, "image/")
	if st.MediaType == "image/gif" {
		role = otogi.MediaAnimation
	}
	md := otogi.Media{
		Role: role, MediaType: st.MediaType, ByteSize: st.Size, Filename: fmt.Sprintf("sticker-%d.%s", id, ext),
		Hashes: []otogi.MediaHash{{Algorithm: "sha256", Value: st.SHA256}},
		Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: st.Bytes},
	}
	return md, st.listed(), nil
}

// stickerAdmin is the service through which the management plane manages
// the stickers, as /sticker does.
type stickerAdmin struct{ m *Module }

// All returns every kept sticker and every pack.
func (s stickerAdmin) All(ctx context.Context) ([]contract.KeptSticker, []contract.StickerPack, error) {
	all, err := s.m.store.stickers(ctx)
	if err != nil {
		return nil, nil, err
	}
	packs, err := s.m.store.packs(ctx)
	if err != nil {
		return nil, nil, err
	}
	out := make([]contract.KeptSticker, len(all))
	for i, st := range all {
		out[i] = st.kept()
	}
	return out, packs, nil
}

// Picture is the stickers service's.
func (s stickerAdmin) Picture(ctx context.Context, id int) (otogi.Media, contract.Sticker, error) {
	return stickers(s).Picture(ctx, id)
}

// Keep keeps data with no note, in pack unless it is empty.
func (s stickerAdmin) Keep(ctx context.Context, data []byte, pack, by string) (contract.KeptSticker, bool, error) {
	md := otogi.Media{ByteSize: int64(len(data)), Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: data}}
	st, existing, err := s.m.keepSticker(ctx, md, data, "", pack, by, nil)
	if err != nil {
		return contract.KeptSticker{}, false, err
	}
	return st.kept(), existing, nil
}

// SetNote sets sticker id's note.
func (s stickerAdmin) SetNote(ctx context.Context, id int, note, by string) (contract.KeptSticker, error) {
	if _, err := s.m.setNote(ctx, id, note, by, nil); err != nil {
		return contract.KeptSticker{}, err
	}
	st, ok, err := s.m.store.sticker(ctx, id)
	if err == nil && !ok {
		err = contract.ErrNoSticker // removed meanwhile
	}
	return st.kept(), err
}

// Remove removes sticker id.
func (s stickerAdmin) Remove(ctx context.Context, id int, by string) error {
	return s.m.remove(ctx, id, by, nil)
}

// MakePack makes pack p.
func (s stickerAdmin) MakePack(ctx context.Context, p contract.StickerPack, by string) (contract.StickerPack, error) {
	return s.m.makePack(ctx, p, by)
}

// EditPack sets pack id's title and description.
func (s stickerAdmin) EditPack(ctx context.Context, id, title, description, by string) (contract.StickerPack, error) {
	return s.m.editPack(ctx, id, title, description, by)
}

// RemovePack removes pack id.
func (s stickerAdmin) RemovePack(ctx context.Context, id string, stickers contract.RemovePack, by string) ([]int, error) {
	return s.m.removePack(ctx, id, stickers, by)
}

// Pack puts sticker into pack.
func (s stickerAdmin) Pack(ctx context.Context, pack string, sticker int, by string) error {
	return s.m.packSticker(ctx, pack, sticker, true, by, nil)
}

// Unpack takes sticker out of pack.
func (s stickerAdmin) Unpack(ctx context.Context, pack string, sticker int, by string) error {
	return s.m.packSticker(ctx, pack, sticker, false, by, nil)
}
