package media

import (
	"context"
	"crypto/sha256"
	"errors"
	"image/color"
	"slices"
	"strings"
	"testing"
	"time"

	contract "github.com/TundraWork/Ex-Otogi/pkg/media"
)

func TestStickerPackIDs(t *testing.T) {
	h := newHarness(t, 1)
	ctx, admin := context.Background(), h.admin()
	for _, id := range []string{"eku", "a", "0", "pack-1", "eku--memes", strings.Repeat("a", 32)} {
		if _, err := admin.MakePack(ctx, contract.StickerPack{ID: id, Title: "T"}, "panel:alice"); err != nil {
			t.Errorf("MakePack(%q) = %v", id, err)
		}
	}
	for _, id := range []string{"", "Eku", "eku pack", "-eku", "eku_memes", "表情", " eku", "eku ", strings.Repeat("a", 33)} {
		if _, err := admin.MakePack(ctx, contract.StickerPack{ID: id, Title: "T"}, "panel:alice"); !errors.Is(err, contract.ErrInvalidPack) {
			t.Errorf("MakePack(%q) = %v, want ErrInvalidPack", id, err)
		}
	}
	for _, title := range []string{"", "  "} {
		if _, err := admin.MakePack(ctx, contract.StickerPack{ID: "titled", Title: title}, "panel:alice"); !errors.Is(err, contract.ErrInvalidPack) {
			t.Errorf("MakePack with title %q = %v, want ErrInvalidPack", title, err)
		}
	}
	if _, err := admin.MakePack(ctx, contract.StickerPack{ID: "eku", Title: "Again"}, "panel:alice"); !errors.Is(err, contract.ErrPackExists) {
		t.Fatalf("MakePack of a taken ID = %v", err)
	}
	if r := h.host.lastRecord("media.sticker_pack"); r.Event != "" || r.Outcome != "ok" {
		t.Fatalf("record %+v", r)
	} else if p := r.Payload.(map[string]any); p["action"] != "create" || p["by"] != "panel:alice" || p["title"] != "T" {
		t.Fatalf("record %v", p)
	}
}

func TestStickerPacks(t *testing.T) {
	h := newHarness(t, 1)
	h.steps[0].answers = []answer{says(picture), says(picture), says(picture)}
	ctx, admin := context.Background(), h.admin()
	for _, p := range []contract.StickerPack{{ID: "eku", Title: "Eku", Description: "艾可"}, {ID: "cats", Title: "Cats"}} {
		if _, err := admin.MakePack(ctx, p, "panel:alice"); err != nil {
			t.Fatal(err)
		}
	}
	black, white, red := pngOf(t, color.Black), pngOf(t, color.White), pngOf(t, color.RGBA{R: 255, A: 255})

	// Kept into a pack.
	st, existing, err := admin.Keep(ctx, black, "eku", "panel:alice")
	if err != nil || existing || st.ID != 1 || !slices.Equal(st.Packs, []string{"eku"}) {
		t.Fatalf("Keep into eku = %+v, %v, %v", st, existing, err)
	}
	if p := h.host.last("media.sticker"); p["action"] != "add" || p["pack"] != "eku" {
		t.Fatalf("record %v", p)
	}
	// Kept already: put in the pack asked for, not described again.
	st, existing, err = admin.Keep(ctx, black, "cats", "panel:bob")
	if err != nil || !existing || st.ID != 1 || !slices.Equal(st.Packs, []string{"cats", "eku"}) {
		t.Fatalf("Keep again into cats = %+v, %v, %v", st, existing, err)
	}
	if p := h.host.last("media.sticker"); p["action"] != "pack" || p["pack"] != "cats" || p["by"] != "panel:bob" || p["id"] != 1 {
		t.Fatalf("record %v", p)
	}
	// A pack that is not there is found before the picture is described.
	if _, _, err := admin.Keep(ctx, white, "nope", "panel:alice"); !errors.Is(err, contract.ErrNoPack) {
		t.Fatalf("Keep into nope = %v", err)
	}
	if h.steps[0].calls() != 1 {
		t.Fatalf("model calls = %d, want 1", h.steps[0].calls())
	}
	if _, _, err := admin.Keep(ctx, white, "", "panel:alice"); err != nil { // 2, in no pack
		t.Fatal(err)
	}
	if _, _, err := admin.Keep(ctx, red, "eku", "panel:alice"); err != nil { // 3, in eku
		t.Fatal(err)
	}

	// Agents see each sticker's packs.
	listed := map[int][]string{}
	for _, st := range h.list() {
		listed[st.ID] = st.Packs
	}
	if !slices.Equal(listed[1], []string{"cats", "eku"}) || len(listed[2]) != 0 || !slices.Equal(listed[3], []string{"eku"}) {
		t.Fatalf("List packs = %v", listed)
	}
	_, packs, err := admin.All(ctx)
	if err != nil || len(packs) != 2 || packs[0].ID != "cats" || !slices.Equal(packs[0].Stickers, []int{1}) ||
		packs[1].ID != "eku" || packs[1].Description != "艾可" || !slices.Equal(packs[1].Stickers, []int{1, 3}) {
		t.Fatalf("All packs = %+v, %v", packs, err)
	}

	// Pack and Unpack do nothing, and record nothing, when it is so already.
	records := func() int { return len(h.host.kinds()) }
	if err := admin.Pack(ctx, "cats", 2, "panel:alice"); err != nil {
		t.Fatal(err)
	}
	before := records()
	if err := admin.Pack(ctx, "cats", 2, "panel:alice"); err != nil || records() != before {
		t.Fatalf("Pack again = %v, %d records after %d", err, records(), before)
	}
	if err := admin.Unpack(ctx, "cats", 2, "panel:alice"); err != nil {
		t.Fatal(err)
	}
	if p := h.host.last("media.sticker"); p["action"] != "unpack" || p["pack"] != "cats" || p["id"] != 2 {
		t.Fatalf("record %v", p)
	}
	before = records()
	if err := admin.Unpack(ctx, "cats", 2, "panel:alice"); err != nil || records() != before {
		t.Fatalf("Unpack again = %v", err)
	}
	if err := admin.Pack(ctx, "nope", 2, "panel:alice"); !errors.Is(err, contract.ErrNoPack) {
		t.Fatalf("Pack into nope = %v", err)
	}
	if err := admin.Pack(ctx, "cats", 9, "panel:alice"); !errors.Is(err, contract.ErrNoSticker) {
		t.Fatalf("Pack of no sticker = %v", err)
	}

	if p, err := admin.EditPack(ctx, "cats", "Cats!", "猫", "panel:alice"); err != nil || p.Title != "Cats!" || p.Description != "猫" || !slices.Equal(p.Stickers, []int{1}) {
		t.Fatalf("EditPack = %+v, %v", p, err)
	}
	if _, err := admin.EditPack(ctx, "cats", " ", "", "panel:alice"); !errors.Is(err, contract.ErrInvalidPack) {
		t.Fatalf("EditPack with no title = %v", err)
	}
	if _, err := admin.EditPack(ctx, "nope", "x", "", "panel:alice"); !errors.Is(err, contract.ErrNoPack) {
		t.Fatalf("EditPack of nope = %v", err)
	}

	// A sticker removed leaves its packs.
	if err := admin.Remove(ctx, 3, "panel:alice"); err != nil {
		t.Fatal(err)
	}
	if _, packs, _ := admin.All(ctx); !slices.Equal(packs[1].Stickers, []int{1}) {
		t.Fatalf("eku after removing 3 = %+v", packs[1])
	}

	if _, err := admin.RemovePack(ctx, "cats", "all", "panel:alice"); !errors.Is(err, contract.ErrInvalidPack) {
		t.Fatalf("RemovePack with stickers all = %v", err)
	}
	if _, err := admin.RemovePack(ctx, "nope", contract.KeepStickers, "panel:alice"); !errors.Is(err, contract.ErrNoPack) {
		t.Fatalf("RemovePack of nope = %v", err)
	}
}

// Removing a pack keeps its stickers, or removes those in no other pack.
func TestRemoveStickerPack(t *testing.T) {
	h := newHarness(t, 1)
	ctx, admin := context.Background(), h.admin()
	for _, id := range []string{"a", "b", "c"} {
		if _, err := admin.MakePack(ctx, contract.StickerPack{ID: id, Title: id}, "panel:alice"); err != nil {
			t.Fatal(err)
		}
	}
	// 1 in a, 2 in a and b, 3 in b, 4 in c.
	for i, packs := range [][]string{{"a"}, {"a", "b"}, {"b"}, {"c"}} {
		pic := []byte{byte(i)}
		sum := sha256.Sum256(pic)
		id, _, _, err := h.m.store.addSticker(ctx, sticker{SHA256: sum[:], MediaType: "image/png", Bytes: pic, Note: "n"}, packs[0], "qq:U1", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range packs[1:] {
			if err := admin.Pack(ctx, p, id, "panel:alice"); err != nil {
				t.Fatal(err)
			}
		}
	}
	removed, err := admin.RemovePack(ctx, "a", contract.KeepStickers, "panel:alice")
	if err != nil || len(removed) != 0 {
		t.Fatalf("RemovePack a, keep = %v, %v", removed, err)
	}
	if p := h.host.last("media.sticker_pack"); p["action"] != "remove" || p["pack"] != "a" || p["stickers"] != "keep" || p["by"] != "panel:alice" {
		t.Fatalf("record %v", p)
	}
	packsOf := func() map[int][]string {
		out := map[int][]string{}
		all, _, err := admin.All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, st := range all {
			out[st.ID] = st.Packs
		}
		return out
	}
	if got := packsOf(); len(got) != 4 || len(got[1]) != 0 || !slices.Equal(got[2], []string{"b"}) {
		t.Fatalf("after removing a = %v", got)
	}

	if err := admin.Pack(ctx, "c", 2, "panel:alice"); err != nil {
		t.Fatal(err)
	}
	removed, err = admin.RemovePack(ctx, "b", contract.RemoveStickers, "panel:alice")
	if err != nil || !slices.Equal(removed, []int{3}) {
		t.Fatalf("RemovePack b, remove = %v, %v; want only 3, the sticker in b alone", removed, err)
	}
	if p := h.host.last("media.sticker"); p["action"] != "remove" || p["id"] != 3 || p["pack"] != "b" {
		t.Fatalf("sticker record %v", p)
	}
	if p := h.host.last("media.sticker_pack"); p["stickers"] != "remove" || !slices.Equal(p["removed"].([]int), []int{3}) {
		t.Fatalf("pack record %v", p)
	}
	if got := packsOf(); len(got) != 3 || !slices.Equal(got[2], []string{"c"}) || got[3] != nil {
		t.Fatalf("after removing b = %v", got)
	}
}

// A pack removed while a picture for it is described: the picture is not
// kept, so none is left in no pack.
func TestStickerPackRemovedWhileDescribing(t *testing.T) {
	h := newHarness(t, 1)
	release := make(chan struct{})
	a := says(picture)
	a.hold = release
	h.steps[0].answers = []answer{a}
	ctx, admin := context.Background(), h.admin()
	if _, err := admin.MakePack(ctx, contract.StickerPack{ID: "eku", Title: "Eku"}, "panel:alice"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := admin.Keep(ctx, pngOf(t, color.Black), "eku", "panel:alice")
		done <- err
	}()
	for h.steps[0].calls() == 0 {
		time.Sleep(time.Millisecond)
	}
	if _, err := admin.RemovePack(ctx, "eku", contract.KeepStickers, "panel:bob"); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; !errors.Is(err, contract.ErrNoPack) {
		t.Fatalf("Keep = %v, want ErrNoPack", err)
	}
	if all, _, _ := admin.All(ctx); len(all) != 0 {
		t.Fatalf("kept %+v", all)
	}
}

// The same picture kept at once twice, the other way in first: the second
// finds it as it writes, and puts it in its pack, or is refused with no
// pack. Into the pack the first put it in, it changes nothing, and records
// nothing.
func TestSameStickerKeptAtOnce(t *testing.T) {
	for _, c := range []struct{ pack, first string }{{"eku", ""}, {"", ""}, {"eku", "eku"}} {
		pack := c.pack
		h := newHarness(t, 1)
		release := make(chan struct{})
		a := says(picture)
		a.hold = release
		h.steps[0].answers = []answer{a}
		ctx, admin := context.Background(), h.admin()
		if _, err := admin.MakePack(ctx, contract.StickerPack{ID: "eku", Title: "Eku"}, "panel:alice"); err != nil {
			t.Fatal(err)
		}
		png := pngOf(t, color.Black)
		type kept struct {
			st       contract.KeptSticker
			existing bool
			err      error
		}
		done := make(chan kept, 1)
		go func() {
			st, existing, err := admin.Keep(ctx, png, pack, "panel:alice")
			done <- kept{st, existing, err}
		}()
		for h.steps[0].calls() == 0 {
			time.Sleep(time.Millisecond)
		}
		sum := sha256.Sum256(png)
		first, _, _, err := h.m.store.addSticker(ctx, sticker{SHA256: sum[:], MediaType: "image/png", Bytes: png}, c.first, "qq:U1", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		close(release)
		got := <-done
		var refusal *contract.StickerRefusal
		switch {
		case pack == "" && (!errors.As(got.err, &refusal) || refusal.Reason != "already kept" || refusal.ID != first):
			t.Errorf("no pack: %+v", got)
		case pack != "" && (got.err != nil || !got.existing || got.st.ID != first || !slices.Equal(got.st.Packs, []string{"eku"})):
			t.Errorf("into eku: %+v", got)
		}
		if all, _, _ := admin.All(ctx); len(all) != 1 {
			t.Errorf("pack %q: kept %d stickers", pack, len(all))
		}
		if p := h.host.last("media.sticker"); pack != "" && (c.first == "") != (p["action"] == "pack") {
			t.Errorf("pack %q after %q: last media.sticker %v", pack, c.first, p)
		}
	}
}
