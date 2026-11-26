package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image/color"
	"strings"
	"sync"
	"testing"
	"time"

	contract "github.com/TundraWork/Ex-Otogi/pkg/media"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// replies is an invocation that also keeps what the module sends.
type replies struct {
	*invocation
	mu   sync.Mutex
	sent []otogi.Message
}

func (r *replies) Send(_ context.Context, _ string, msg otogi.Message) (otogi.EventRef, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, msg)
	return otogi.EventRef{SourceEvent: "sent"}, nil
}

// post handles a message with media, from source, in the test chat.
func (h *harness) post(source string, media ...otogi.Media) error {
	e := otogi.Event{Platform: from.Platform, Account: from.Account, Conversation: otogi.Conversation{ID: from.Conversation, Kind: otogi.ConversationGroup},
		Action: otogi.ActionMessageCreated, Direction: otogi.DirectionIncoming, SourceID: otogi.SourceEventID(source)}
	for i, md := range media {
		e.Message.Parts = append(e.Message.Parts, otogi.Part{ID: otogi.PartID(string(rune('p' + i))), Kind: otogi.PartMedia, Media: &md})
	}
	return h.m.Handle(context.Background(), e, h.inv)
}

// stickerCall is one /sticker command: its arguments, its sender, where it
// was sent, and the rest of its message.
type stickerCall struct {
	args    string
	actor   otogi.ActorID
	direct  bool
	replyTo string
	media   []otogi.Media
}

// sticker runs /sticker as c and returns the module's reply.
func (h *harness) sticker(c stickerCall) string {
	h.t.Helper()
	if c.actor == "" {
		c.actor = operator
	}
	conv := otogi.Conversation{ID: from.Conversation, Kind: otogi.ConversationGroup}
	if c.direct {
		conv = otogi.Conversation{ID: "user:op", Kind: otogi.ConversationDirect}
	}
	e := otogi.Event{ID: "cmd", Platform: from.Platform, Account: from.Account, Conversation: conv, Actor: otogi.Actor{ID: c.actor},
		Action: otogi.ActionMessageCreated, Direction: otogi.DirectionIncoming, SourceID: "cmd",
		Command: &otogi.Command{Name: "sticker", Args: c.args, Argv: strings.Fields(c.args)}}
	for i, md := range c.media {
		e.Message.Parts = append(e.Message.Parts, otogi.Part{ID: otogi.PartID(string(rune('p' + i))), Kind: otogi.PartMedia, Media: &md})
	}
	if c.replyTo != "" {
		e.Message.Relations = []otogi.Relation{{Kind: otogi.RelationReply, Target: otogi.EventRef{Platform: from.Platform, Account: from.Account, Conversation: conv.ID, SourceEvent: otogi.SourceEventID(c.replyTo)}}}
	}
	r := &replies{invocation: h.inv}
	if err := h.m.Handle(context.Background(), e, r); err != nil {
		h.t.Fatalf("/sticker %s: %v", c.args, err)
	}
	if len(r.sent) != 1 {
		h.t.Fatalf("/sticker %s sent %d replies, want 1", c.args, len(r.sent))
	}
	return r.sent[0].Parts[0].Text
}

func (h *harness) list() []contract.Sticker {
	h.t.Helper()
	all, err := h.host.services[contract.StickersService].(contract.Stickers).List(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	return all
}

func (h *harness) picture(id int) (otogi.Media, contract.Sticker, error) {
	return h.host.services[contract.StickersService].(contract.Stickers).Picture(context.Background(), id)
}

func wantPrefix(t *testing.T, got, prefix string) {
	t.Helper()
	if !strings.HasPrefix(got, prefix) {
		t.Fatalf("reply = %q, want it to start with %q", got, prefix)
	}
}

func TestStickerFromAReply(t *testing.T) {
	h := newHarness(t, 1)
	h.steps[0].answers = []answer{says(picture)}
	png := pngOf(t, color.RGBA{R: 255, A: 255})
	if err := h.post("m1", h.image("https://qq/1", png)); err != nil {
		t.Fatal(err)
	}
	wantPrefix(t, h.sticker(stickerCall{args: "add", replyTo: "m1"}), "Kept as sticker 1: 一只猫")
	if h.steps[0].calls() != 1 {
		t.Fatalf("model calls = %d, want 1: the kept description is reused", h.steps[0].calls())
	}
	all := h.list()
	if len(all) != 1 || all[0].ID != 1 || all[0].Kind != contract.KindMeme || !strings.HasPrefix(all[0].Text, "一只猫") {
		t.Fatalf("List = %+v", all)
	}
	md, st, err := h.picture(1)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(png)
	if md.Role != otogi.MediaImage || md.MediaType != "image/png" || string(md.Source.Inline) != string(png) || md.Source.Kind != otogi.MediaSourceInline ||
		len(md.Hashes) != 1 || md.Hashes[0].Algorithm != "sha256" || string(md.Hashes[0].Value) != string(sum[:]) || st.ID != 1 {
		t.Fatalf("Picture = %+v, %+v", md, st)
	}
	if p := h.host.last("media.sticker"); p["action"] != "add" || p["id"] != 1 || p["by"] != "qq:"+operator {
		t.Fatalf("media.sticker = %v", p)
	}
}

func TestStickerSentWithTheCommand(t *testing.T) {
	h := newHarness(t, 1)
	h.steps[0].answers = []answer{says(picture)}
	gif := gifOf(t, []gifFrame{{full: true, c: color.RGBA{G: 255, A: 255}}})
	// A private chat is not on the describe list: the picture is described
	// now, since nothing described it on the way in.
	inline := otogi.Media{Role: otogi.MediaImage, Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: gif}}
	wantPrefix(t, h.sticker(stickerCall{args: "add 生气", direct: true, media: []otogi.Media{inline}}), "Kept as sticker 1: 生气")
	if h.steps[0].calls() != 1 {
		t.Fatalf("model calls = %d, want 1", h.steps[0].calls())
	}
	sum := sha256.Sum256(gif)
	if d, ok, _ := h.m.store.get(context.Background(), key{"sha256", sum[:]}); !ok || d.Kind != contract.KindMeme {
		t.Fatalf("the description was not kept: %+v, %v", d, ok)
	}
	md, st, err := h.picture(1)
	if err != nil || md.Role != otogi.MediaAnimation || md.MediaType != "image/gif" || st.Text != "生气" || st.Kind != contract.KindMeme {
		t.Fatalf("Picture = %+v, %+v, %v", md, st, err)
	}
}

func TestStickerSeveralInOneMessage(t *testing.T) {
	h := newHarness(t, 1)
	h.steps[0].answers = []answer{says(picture), says(picture)}
	a, b := h.image("https://qq/a", pngOf(t, color.Black)), h.image("https://qq/b", pngOf(t, color.White))
	got := h.sticker(stickerCall{args: "add", media: []otogi.Media{a, b}})
	if lines := strings.Split(got, "\n"); len(lines) != 2 || !strings.HasPrefix(lines[0], "Kept as sticker 1") || !strings.HasPrefix(lines[1], "Kept as sticker 2") {
		t.Fatalf("reply = %q", got)
	}
}

func TestStickerRefused(t *testing.T) {
	h := newHarness(t, 1)
	png := h.image("https://qq/1", pngOf(t, color.Black))

	wantPrefix(t, h.sticker(stickerCall{args: "add", actor: "user:someone", media: []otogi.Media{png}}), "Only operators can manage stickers. Your ID is qq:user:someone")

	h.host.allowed[chat] = false
	wantPrefix(t, h.sticker(stickerCall{args: "add", media: []otogi.Media{png}}), "This chat is not on media's describe list.")
	h.host.allowed[chat] = true

	wantPrefix(t, h.sticker(stickerCall{args: "add"}), "Reply to a picture with /sticker add")
	wantPrefix(t, h.sticker(stickerCall{args: "nope"}), "Usage: /sticker")
	wantPrefix(t, h.sticker(stickerCall{args: "remove x"}), "Usage: /sticker")
	if len(h.list()) != 0 {
		t.Fatal("a refused /sticker kept something")
	}
}

func TestStickerOperatorsAreReadEachTime(t *testing.T) {
	h := newHarness(t, 1)
	h.steps[0].answers = []answer{says(picture)}
	png := otogi.Media{Role: otogi.MediaImage, Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: pngOf(t, color.Black)}}

	// The operators are edited in the control panel while the module
	// runs: the next command goes by the list as it is then.
	h.operators = []string{"qq:user:someone"}
	wantPrefix(t, h.sticker(stickerCall{args: "add", actor: "user:someone", direct: true, media: []otogi.Media{png}}), "Kept as sticker 1")
	wantPrefix(t, h.sticker(stickerCall{args: "list"}), "Only operators can manage stickers. Your ID is qq:"+operator)
}

func TestStickerOnlyFromRecentMessages(t *testing.T) {
	h := newHarness(t, 1)
	ctx := context.Background()
	now := time.Now()
	h.m.now = func() time.Time { return now }
	pic := []otogi.Media{h.image("https://qq/old", pngOf(t, color.Black))}
	if err := h.m.store.putRecent(ctx, from, "old", pic, now.Add(-recentMedia-time.Minute)); err != nil {
		t.Fatal(err)
	}
	wantPrefix(t, h.sticker(stickerCall{args: "add", replyTo: "old"}), "I can't find a picture in that message any more")
	wantPrefix(t, h.sticker(stickerCall{args: "add", replyTo: "never-seen"}), "I can't find a picture in that message any more")

	// The chat is on the list, but its media was not kept: describing is
	// what keeps it, and a message without media keeps nothing.
	if err := h.post("plain"); err != nil {
		t.Fatal(err)
	}
	wantPrefix(t, h.sticker(stickerCall{args: "add", replyTo: "plain"}), "I can't find a picture in that message any more")
}

func TestStickerPictureGone(t *testing.T) {
	h := newHarness(t, 1)
	md := h.image("https://qq/1", pngOf(t, color.Black))
	h.inv.fail = errors.New("expired")
	wantPrefix(t, h.sticker(stickerCall{args: "add", media: []otogi.Media{md}}), "Couldn't fetch that picture any more")
	if p := h.host.last("media.sticker"); p["reason"] != "gone" {
		t.Fatalf("media.sticker = %v", p)
	}
}

func TestStickerTooLarge(t *testing.T) {
	h := newHarness(t, 1)
	declared := h.image("https://qq/big", pngOf(t, color.Black))
	declared.ByteSize = maxStickerBytes + 1
	wantPrefix(t, h.sticker(stickerCall{args: "add", media: []otogi.Media{declared}}), "That picture is 9.0 MiB; a sticker can be at most 9 MiB.")
	if h.inv.fetches() != 0 {
		t.Fatal("a picture declared too large was fetched")
	}
	// A picture that turns out larger than it said.
	big := make([]byte, maxStickerBytes+1)
	copy(big, pngOf(t, color.Black))
	h.inv.files["https://qq/huge"] = big
	under := otogi.Media{Role: otogi.MediaImage, ByteSize: 10, Source: otogi.MediaSource{Kind: otogi.MediaSourceExternal, URL: "https://qq/huge"}}
	wantPrefix(t, h.sticker(stickerCall{args: "add", media: []otogi.Media{under}}), "That picture is 9.0 MiB")
	if p := h.host.last("media.sticker"); p["reason"] != "too large" {
		t.Fatalf("media.sticker = %v", p)
	}
}

// A picture is kept by its content: PNG, JPEG, GIF, and a WebP that is
// not animated. An animated WebP is refused with its own reason, and a
// WebP too short to tell is refused as not a picture kept, without reading
// past its end; so is anything else, an MP4 GIF from Telegram included.
func TestStickerTypes(t *testing.T) {
	h := newHarness(t, 1)
	mp4 := append([]byte{0, 0, 0, 0x20}, []byte("ftypisom\x00\x00\x02\x00isomiso2mp41")...)
	// An extended WebP header, its flags at data[20]: 0x10, alpha, or with
	// 0x02, animation.
	extended := func(flags byte) []byte {
		return append([]byte("RIFF\x24\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00"), append([]byte{flags}, make([]byte, 24)...)...)
	}
	for name, c := range map[string]struct {
		data   []byte
		reason string
		reply  string
	}{
		"mp4":       {mp4, "not png, jpeg, gif, or webp", "Only PNG, JPEG, GIF, and WebP pictures can be kept."},
		"animated":  {extended(0x12), "animated webp", "Animated WebP pictures cannot be kept: QQ refuses to send them."},
		"truncated": {[]byte("RIFF\x24\x00\x00\x00WEBPVP8X"), "not png, jpeg, gif, or webp", "Only PNG, JPEG, GIF, and WebP pictures can be kept."},
	} {
		md := otogi.Media{Role: otogi.MediaAnimation, Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: c.data}}
		wantPrefix(t, h.sticker(stickerCall{args: "add", media: []otogi.Media{md}}), c.reply)
		if p := h.host.last("media.sticker"); p["reason"] != c.reason || p["media_type"] == nil {
			t.Fatalf("%s: media.sticker = %v", name, p)
		}
	}
	if len(h.list()) != 0 {
		t.Fatal("a picture of the wrong type was kept")
	}
	// A WebP, simple or extended with no animation, is kept and sent as it
	// is: a picture, named for its type.
	h.steps[0].answers = []answer{says(picture), says(picture)}
	simple := append([]byte("RIFF\x24\x00\x00\x00WEBPVP8 "), make([]byte, 24)...)
	for i, data := range [][]byte{simple, extended(0x10)} {
		md := otogi.Media{Role: otogi.MediaImage, Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: data}}
		wantPrefix(t, h.sticker(stickerCall{args: "add", media: []otogi.Media{md}}), fmt.Sprintf("Kept as sticker %d", i+1))
		got, _, err := h.picture(i + 1)
		if err != nil || got.Role != otogi.MediaImage || got.MediaType != "image/webp" || got.Filename != fmt.Sprintf("sticker-%d.webp", i+1) ||
			!bytes.Equal(got.Source.Inline, data) {
			t.Fatalf("sticker %d: %+v, %v", i+1, got, err)
		}
	}
}

func TestStickerKeptOnce(t *testing.T) {
	h := newHarness(t, 1)
	h.steps[0].answers = []answer{says(picture)}
	png := pngOf(t, color.Black)
	wantPrefix(t, h.sticker(stickerCall{args: "add", media: []otogi.Media{h.image("https://qq/1", png)}}), "Kept as sticker 1")
	// The same picture by another way in, with no driver digest.
	inline := otogi.Media{Role: otogi.MediaImage, Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: png}}
	wantPrefix(t, h.sticker(stickerCall{args: "add", direct: true, media: []otogi.Media{inline}}), "Already kept as sticker 1.")
	if h.steps[0].calls() != 1 {
		t.Fatalf("model calls = %d, want 1", h.steps[0].calls())
	}
}

// Stickers are not limited in number or in bytes in all: what an agent is
// shown is chat's to bound. /sticker list shows 50 of them at most.
func TestStickersUnlimited(t *testing.T) {
	h := newHarness(t, 1)
	ctx := context.Background()
	for i := range listedStickers + 1 {
		pic := []byte(fmt.Sprintf("sticker %d", i))
		sum := sha256.Sum256(pic)
		if _, _, _, err := h.m.store.addSticker(ctx, sticker{SHA256: sum[:], MediaType: "image/png", Bytes: pic, Note: fmt.Sprint("n", i)}, "", "qq:U1", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	h.steps[0].answers = []answer{says(picture)}
	wantPrefix(t, h.sticker(stickerCall{args: "add", media: []otogi.Media{h.image("https://qq/1", pngOf(t, color.Black))}}), fmt.Sprintf("Kept as sticker %d", listedStickers+2))
	got := h.sticker(stickerCall{args: "list"})
	lines := strings.Split(got, "\n")
	if !strings.HasPrefix(lines[0], fmt.Sprintf("%d kept, ", listedStickers+2)) || len(lines) != listedStickers+2 ||
		lines[len(lines)-1] != "…and 2 more; see the control panel." {
		t.Fatalf("list: %d lines, first %q, last %q", len(lines), lines[0], lines[len(lines)-1])
	}
}

func TestStickerNoteAndRemove(t *testing.T) {
	h := newHarness(t, 1)
	h.steps[0].answers = []answer{says(picture), says(picture)}
	png := pngOf(t, color.Black)
	h.sticker(stickerCall{args: "add", media: []otogi.Media{h.image("https://qq/1", png)}})

	wantPrefix(t, h.sticker(stickerCall{args: "note 1 摸头 安慰"}), "Sticker 1 now shows: 摸头 安慰")
	if all := h.list(); len(all) != 1 || all[0].Text != "摸头 安慰" {
		t.Fatalf("List = %+v", all)
	}
	wantPrefix(t, h.sticker(stickerCall{args: "note 1"}), "Sticker 1 now shows: 一只猫")
	wantPrefix(t, h.sticker(stickerCall{args: "list"}), "1 kept, 0.0 MiB:")
	wantPrefix(t, h.sticker(stickerCall{args: "note 7 x"}), "There is no sticker 7.")

	wantPrefix(t, h.sticker(stickerCall{args: "remove 1"}), "Removed sticker 1.")
	if _, _, err := h.picture(1); !errors.Is(err, contract.ErrNoSticker) {
		t.Fatalf("Picture of a removed sticker = %v", err)
	}
	wantPrefix(t, h.sticker(stickerCall{args: "remove 1"}), "There is no sticker 1.")
	wantPrefix(t, h.sticker(stickerCall{args: "list"}), "No stickers yet.")
	// Numbers are not reused: a thread's old call must not point at another
	// picture.
	wantPrefix(t, h.sticker(stickerCall{args: "add", media: []otogi.Media{h.image("https://qq/1", png)}}), "Kept as sticker 2")
}

func TestStickerWithoutDescription(t *testing.T) {
	h := newHarness(t, 1)
	h.steps[0].answers = []answer{{err: errors.New("down")}}
	md := h.image("https://qq/1", pngOf(t, color.Black))
	wantPrefix(t, h.sticker(stickerCall{args: "add", media: []otogi.Media{md}}), "Kept as sticker 1, but it has no description")
	if all := h.list(); len(all) != 0 {
		t.Fatalf("List = %+v; a sticker with neither a note nor a description is left out", all)
	}
	if got := h.sticker(stickerCall{args: "list"}); !strings.Contains(got, "1. [-] (no description") {
		t.Fatalf("list = %q", got)
	}
	h.sticker(stickerCall{args: "note 1 摸头"})
	if all := h.list(); len(all) != 1 || all[0].Text != "摸头" || all[0].Kind != "" {
		t.Fatalf("List = %+v", all)
	}
}

// A kept sticker stands on its own: its description may be pruned.
func TestStickerOutlivesItsDescription(t *testing.T) {
	h := newHarness(t, 1)
	h.steps[0].answers = []answer{says(picture)}
	h.sticker(stickerCall{args: "add", media: []otogi.Media{h.image("https://qq/1", pngOf(t, color.Black))}})
	if _, err := h.m.store.db.Exec(`DELETE FROM descriptions`); err != nil {
		t.Fatal(err)
	}
	if all := h.list(); len(all) != 1 || all[0].Kind != contract.KindMeme || !strings.HasPrefix(all[0].Text, "一只猫") {
		t.Fatalf("List = %+v", all)
	}
}

func TestAfter(t *testing.T) {
	for _, c := range []struct {
		args string
		n    int
		want string
	}{
		{"add  摸头  安慰 ", 1, "摸头  安慰"},
		{"note 3 a b", 2, "a b"},
		{"note 3", 2, ""},
		{"add", 1, ""},
	} {
		if got := after(c.args, c.n); got != c.want {
			t.Errorf("after(%q, %d) = %q, want %q", c.args, c.n, got, c.want)
		}
	}
}

// lastRecord is the last record of kind.
func (h *host) lastRecord(kind string) otogi.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := len(h.records) - 1; i >= 0; i-- {
		if h.records[i].Kind == kind {
			return h.records[i]
		}
	}
	return otogi.Record{}
}

func (h *harness) admin() contract.StickerAdmin {
	return h.host.services[contract.StickerAdminService].(contract.StickerAdmin)
}

// The management plane keeps, notes, and removes stickers as /sticker
// does: the same checks, the description waited for, and a media.sticker
// record by its caller, on no event.
func TestStickerAdmin(t *testing.T) {
	h := newHarness(t, 1)
	h.steps[0].answers = []answer{says(picture), {err: errors.New("down")}}
	ctx, admin := context.Background(), h.admin()
	png := pngOf(t, color.Black)

	st, existing, err := admin.Keep(ctx, png, "", "panel:alice")
	if err != nil || existing {
		t.Fatal(existing, err)
	}
	sum := sha256.Sum256(png)
	if st.ID != 1 || st.Kind != contract.KindMeme || st.Note != "" || !strings.HasPrefix(st.Summary, "一只猫") || st.Text != st.Summary ||
		st.MediaType != "image/png" || st.Size != int64(len(png)) || string(st.SHA256) != string(sum[:]) || st.AddedBy != "panel:alice" || time.Since(st.AddedAt) > time.Minute ||
		len(st.Packs) != 0 {
		t.Fatalf("kept %+v", st)
	}
	if r := h.host.lastRecord("media.sticker"); r.Event != "" || r.Platform != "" || r.Conversation != "" || r.Outcome != "ok" ||
		r.Payload.(map[string]any)["by"] != "panel:alice" || r.Payload.(map[string]any)["action"] != "add" {
		t.Fatalf("record %+v", r)
	}

	var refusal *contract.StickerRefusal
	if _, _, err := admin.Keep(ctx, png, "", "panel:alice"); !errors.As(err, &refusal) || refusal.Reason != "already kept" || refusal.ID != 1 || refusal.Message != "Already kept as sticker 1." {
		t.Fatalf("again: %v", err)
	}
	if _, _, err := admin.Keep(ctx, []byte("not a picture"), "", "panel:alice"); !errors.As(err, &refusal) || refusal.Reason != "not png, jpeg, gif, or webp" {
		t.Fatalf("text: %v", err)
	}
	if _, _, err := admin.Keep(ctx, make([]byte, maxStickerBytes+1), "", "panel:alice"); !errors.As(err, &refusal) || refusal.Reason != "too large" {
		t.Fatalf("too large: %v", err)
	}
	if r := h.host.lastRecord("media.sticker"); r.Outcome != "skipped" || r.Payload.(map[string]any)["reason"] != "too large" || r.Event != "" {
		t.Fatalf("refusal record %+v", r)
	}

	// Not described: kept all the same, with no text.
	if st, _, err := admin.Keep(ctx, pngOf(t, color.White), "", "panel:bob"); err != nil || st.ID != 2 || st.Text != "" || st.Kind != "" {
		t.Fatalf("undescribed: %+v, %v", st, err)
	}

	all, packs, err := admin.All(ctx)
	if err != nil || len(all) != 2 || all[1].ID != 2 || all[1].Text != "" || all[1].AddedBy != "panel:bob" || len(packs) != 0 {
		t.Fatalf("All = %+v, %+v, %v", all, packs, err)
	}

	if st, err := admin.SetNote(ctx, 2, "摸头", "panel:alice"); err != nil || st.Text != "摸头" || st.Note != "摸头" || st.AddedBy != "panel:bob" {
		t.Fatalf("SetNote = %+v, %v", st, err)
	}
	if r := h.host.lastRecord("media.sticker"); r.Payload.(map[string]any)["action"] != "note" || r.Payload.(map[string]any)["by"] != "panel:alice" || r.Event != "" {
		t.Fatalf("note record %+v", r)
	}
	if _, err := admin.SetNote(ctx, 7, "x", "panel:alice"); !errors.Is(err, contract.ErrNoSticker) {
		t.Fatalf("SetNote of no sticker = %v", err)
	}
	if err := admin.Remove(ctx, 1, "panel:alice"); err != nil {
		t.Fatal(err)
	}
	if err := admin.Remove(ctx, 1, "panel:alice"); !errors.Is(err, contract.ErrNoSticker) {
		t.Fatalf("Remove again = %v", err)
	}
	if all, _, _ := admin.All(ctx); len(all) != 1 || all[0].ID != 2 {
		t.Fatalf("after Remove: %+v", all)
	}
	if h.steps[0].calls() != 2 {
		t.Fatalf("model calls = %d, want 2: refused pictures are not described", h.steps[0].calls())
	}
}
