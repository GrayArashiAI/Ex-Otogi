package management

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/media"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// fakeStickers keeps stickers and packs in memory and notes who changed
// them.
type fakeStickers struct {
	kept  []media.KeptSticker
	packs []media.StickerPack
	by    []string
}

func (f *fakeStickers) All(context.Context) ([]media.KeptSticker, []media.StickerPack, error) {
	return f.kept, f.packs, nil
}

func (f *fakeStickers) Picture(_ context.Context, id int) (otogi.Media, media.Sticker, error) {
	for _, st := range f.kept {
		if st.ID == id {
			return otogi.Media{MediaType: st.MediaType, Hashes: []otogi.MediaHash{{Algorithm: "sha256", Value: st.SHA256}},
				Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: []byte("GIF89a")}}, st.Sticker, nil
		}
	}
	return otogi.Media{}, media.Sticker{}, media.ErrNoSticker
}

func (f *fakeStickers) pack(id string) int {
	return slices.IndexFunc(f.packs, func(p media.StickerPack) bool { return p.ID == id })
}

func (f *fakeStickers) Keep(_ context.Context, data []byte, pack, by string) (media.KeptSticker, bool, error) {
	f.by = append(f.by, "keep "+pack+" "+by)
	if pack != "" && f.pack(pack) < 0 {
		return media.KeptSticker{}, false, media.ErrNoPack
	}
	switch string(data) {
	case "again":
		if pack != "" {
			return media.KeptSticker{Sticker: media.Sticker{ID: 3, Packs: []string{pack}}, MediaType: "image/gif"}, true, nil
		}
		return media.KeptSticker{}, false, &media.StickerRefusal{Reason: "already kept", Message: "Already kept as sticker 3.", ID: 3}
	case "text":
		return media.KeptSticker{}, false, &media.StickerRefusal{Reason: "not png, jpeg, or gif", Message: "Only PNG, JPEG, and GIF pictures can be kept."}
	}
	st := media.KeptSticker{Sticker: media.Sticker{ID: 4}, MediaType: "image/png", Size: int64(len(data)), AddedBy: by}
	f.kept = append(f.kept, st)
	return st, false, nil
}

func (f *fakeStickers) SetNote(_ context.Context, id int, note, by string) (media.KeptSticker, error) {
	f.by = append(f.by, "note "+by)
	for i, st := range f.kept {
		if st.ID == id {
			f.kept[i].Note, f.kept[i].Text = note, note
			return f.kept[i], nil
		}
	}
	return media.KeptSticker{}, media.ErrNoSticker
}

func (f *fakeStickers) Remove(_ context.Context, id int, by string) error {
	f.by = append(f.by, "remove "+by)
	for i, st := range f.kept {
		if st.ID == id {
			f.kept = append(f.kept[:i], f.kept[i+1:]...)
			return nil
		}
	}
	return media.ErrNoSticker
}

func (f *fakeStickers) MakePack(_ context.Context, p media.StickerPack, by string) (media.StickerPack, error) {
	f.by = append(f.by, "make "+by)
	switch {
	case strings.TrimSpace(p.Title) == "":
		return media.StickerPack{}, media.ErrInvalidPack
	case f.pack(p.ID) >= 0:
		return media.StickerPack{}, media.ErrPackExists
	}
	f.packs = append(f.packs, p)
	return p, nil
}

func (f *fakeStickers) EditPack(_ context.Context, id, title, description, by string) (media.StickerPack, error) {
	f.by = append(f.by, "edit "+by)
	i := f.pack(id)
	if i < 0 {
		return media.StickerPack{}, media.ErrNoPack
	}
	f.packs[i].Title, f.packs[i].Description = title, description
	return f.packs[i], nil
}

func (f *fakeStickers) RemovePack(_ context.Context, id string, stickers media.RemovePack, by string) ([]int, error) {
	f.by = append(f.by, "remove pack "+string(stickers)+" "+by)
	i := f.pack(id)
	if i < 0 {
		return nil, media.ErrNoPack
	}
	f.packs = append(f.packs[:i], f.packs[i+1:]...)
	if stickers == media.RemoveStickers {
		return []int{3}, nil
	}
	return nil, nil
}

func (f *fakeStickers) Pack(_ context.Context, pack string, id int, by string) error {
	f.by = append(f.by, "pack "+by)
	return f.member(pack, id)
}

func (f *fakeStickers) Unpack(_ context.Context, pack string, id int, by string) error {
	f.by = append(f.by, "unpack "+by)
	return f.member(pack, id)
}

func (f *fakeStickers) member(pack string, id int) error {
	if f.pack(pack) < 0 {
		return media.ErrNoPack
	}
	if !slices.ContainsFunc(f.kept, func(st media.KeptSticker) bool { return st.ID == id }) {
		return media.ErrNoSticker
	}
	return nil
}

// The sticker routes answer for the running media module, and 503 while it
// is not running; refusals keep /sticker add's words and name the reason.
func TestStickerRoutes(t *testing.T) {
	fake := &fakeStickers{kept: []media.KeptSticker{{Sticker: media.Sticker{ID: 3, Kind: media.KindMeme, Text: "摸头", Packs: []string{"eku"}}, Summary: "一只猫", Note: "摸头",
		MediaType: "image/gif", Size: 6, SHA256: []byte{0xab, 0xcd}, AddedBy: "qq:user:U1", AddedAt: time.Unix(1000, 0).UTC()}},
		packs: []media.StickerPack{{ID: "eku", Title: "Eku", Stickers: []int{3}}}}
	running := true
	handler, err := newHandler(ServerConfig{Token: "secret", Events: openLog(t), Status: func(context.Context) Status { return Status{} },
		Ready: func() bool { return true }, Shutdown: func() {},
		Stickers: func() (media.StickerAdmin, bool) { return fake, running }})
	if err != nil {
		t.Fatal(err)
	}
	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer secret")
		if method == http.MethodPost && strings.Contains(path, "/stickers") {
			req.Header.Set("Content-Type", "application/octet-stream")
		} else if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	for _, c := range []struct {
		method, path, body string
		code               int
		contains           string
	}{
		{"GET", "/api/modules/media/stickers", "", 200, `{"stickers":[{"id":3,"kind":"meme","text":"摸头","summary":"一只猫","note":"摸头","media_type":"image/gif","bytes":6,"sha256":"abcd","packs":["eku"],"added_by":"qq:user:U1","added_at":"1970-01-01T00:16:40Z"}],"packs":[{"id":"eku","title":"Eku","description":"","stickers":[3]}]}`},
		{"GET", "/api/modules/media/stickers/9/picture", "", 404, "there is no sticker 9"},
		{"POST", "/api/modules/media/stickers", "again", 409, `"detail":"Already kept as sticker 3."`},
		{"POST", "/api/modules/media/stickers", "again", 409, `"message":"already kept","location":"body","value":3`},
		{"POST", "/api/modules/media/stickers?pack=eku", "again", 200, `"id":3`},
		{"POST", "/api/modules/media/stickers?pack=eku", "again", 200, `"existing":true`},
		{"POST", "/api/modules/media/stickers?pack=nope", "\x89PNG", 404, "no such sticker pack"},
		{"POST", "/api/modules/media/stickers", "text", 422, `"message":"not png, jpeg, or gif"`},
		{"POST", "/api/modules/media/stickers", "\x89PNG", 201, `"id":4`},
		{"PATCH", "/api/modules/media/stickers/3", `{"note":"安慰"}`, 200, `"note":"安慰"`},
		{"PATCH", "/api/modules/media/stickers/9", `{"note":"x"}`, 404, "there is no sticker 9"},
		{"DELETE", "/api/modules/media/stickers/4", "", 204, ""},
		{"DELETE", "/api/modules/media/stickers/4", "", 404, "there is no sticker 4"},

		{"POST", "/api/modules/media/sticker-packs", `{"id":"cats-2","title":"Cats","description":"猫"}`, 201, `{"id":"cats-2","title":"Cats","description":"猫","stickers":[]}`},
		{"POST", "/api/modules/media/sticker-packs", `{"id":"cats-2","title":"Cats"}`, 409, "sticker pack exists"},
		{"POST", "/api/modules/media/sticker-packs", `{"id":"Cats","title":"Cats"}`, 422, "id"},
		{"POST", "/api/modules/media/sticker-packs", `{"id":"-cats","title":"Cats"}`, 422, "id"},
		{"POST", "/api/modules/media/sticker-packs", `{"id":"cats","title":""}`, 422, "title"},
		{"POST", "/api/modules/media/sticker-packs", `{"id":"cats","title":" "}`, 422, "invalid sticker pack"},
		{"PATCH", "/api/modules/media/sticker-packs/cats-2", `{"title":"Cats!"}`, 200, `"title":"Cats!"`},
		{"PATCH", "/api/modules/media/sticker-packs/nope", `{"title":"x"}`, 404, "no such sticker pack"},
		{"PUT", "/api/modules/media/sticker-packs/cats-2/stickers/3", "", 204, ""},
		{"PUT", "/api/modules/media/sticker-packs/nope/stickers/3", "", 404, "no such sticker pack"},
		{"PUT", "/api/modules/media/sticker-packs/cats-2/stickers/9", "", 404, "there is no sticker 9"},
		{"DELETE", "/api/modules/media/sticker-packs/cats-2/stickers/3", "", 204, ""},
		{"DELETE", "/api/modules/media/sticker-packs/cats-2", "", 422, "stickers"},
		{"DELETE", "/api/modules/media/sticker-packs/cats-2?stickers=all", "", 422, "stickers"},
		{"DELETE", "/api/modules/media/sticker-packs/cats-2?stickers=remove", "", 200, `{"removed":[3]}`},
		{"DELETE", "/api/modules/media/sticker-packs/eku?stickers=keep", "", 200, `{"removed":[]}`},
		{"DELETE", "/api/modules/media/sticker-packs/eku?stickers=keep", "", 404, "no such sticker pack"},
	} {
		rec := do(c.method, c.path, c.body)
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.contains) {
			t.Errorf("%s %s: %d %s, want %d with %s", c.method, c.path, rec.Code, rec.Body, c.code, c.contains)
		}
	}
	want := "keep  management API, keep  management API, keep eku management API, keep eku management API, keep nope management API, keep  management API, keep  management API, " +
		"note management API, note management API, remove management API, remove management API, " +
		"make management API, make management API, make management API, edit management API, edit management API, " +
		"pack management API, pack management API, pack management API, unpack management API, " +
		"remove pack remove management API, remove pack keep management API, remove pack keep management API"
	if got := strings.Join(fake.by, ", "); got != want {
		t.Errorf("by:\n%s\nwant\n%s", got, want)
	}

	rec := do("GET", "/api/modules/media/stickers/3/picture", "")
	if rec.Code != 200 || rec.Body.String() != "GIF89a" || rec.Header().Get("Content-Type") != "image/gif" || rec.Header().Get("ETag") != `"abcd"` ||
		rec.Header().Get("Cache-Control") != "private, max-age=31536000, immutable" {
		t.Errorf("picture: %d %q %v", rec.Code, rec.Body, rec.Header())
	}
	// A sticker may be 9 MiB, past huma's 1 MiB default; the route takes up
	// to 10 MiB and leaves what is too large to the module to say.
	if rec := do("POST", "/api/modules/media/stickers", string(bytes.Repeat([]byte{0}, 9<<20))); rec.Code != http.StatusCreated {
		t.Errorf("an upload of 9 MiB: %d %s", rec.Code, rec.Body)
	}
	if rec := do("POST", "/api/modules/media/stickers", string(bytes.Repeat([]byte{0}, maxStickerUpload+1))); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("an upload over 10 MiB: %d %s", rec.Code, rec.Body)
	}

	running = false
	for _, path := range []string{"/api/modules/media/stickers", "/api/modules/media/stickers/3/picture"} {
		if rec := do("GET", path, ""); rec.Code != 503 || !strings.Contains(rec.Body.String(), "the media module is not running") {
			t.Errorf("%s while media is not running: %d %s", path, rec.Code, rec.Body)
		}
	}
	if rec := do("POST", "/api/modules/media/sticker-packs", `{"id":"dogs","title":"Dogs"}`); rec.Code != 503 {
		t.Errorf("make a pack while media is not running: %d %s", rec.Code, rec.Body)
	}
}
