package management

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/TundraWork/Ex-Otogi/pkg/media"
)

// Sticker is a kept sticker, as the panel manages it.
type Sticker struct {
	ID        int       `json:"id"`
	Kind      string    `json:"kind,omitempty" doc:"What its description said it is; empty when it was not described."`
	Text      string    `json:"text" doc:"What agents see of it: the note, else the summary. Empty: agents do not see it."`
	Summary   string    `json:"summary" doc:"What its description said when it was kept."`
	Note      string    `json:"note"`
	MediaType string    `json:"media_type" enum:"image/png,image/jpeg,image/gif"`
	Bytes     int64     `json:"bytes"`
	SHA256    string    `json:"sha256" doc:"The picture's SHA-256, in hex."`
	Packs     []string  `json:"packs" nullable:"false" doc:"The IDs of the packs it is in, sorted; empty when it is in none."`
	AddedBy   string    `json:"added_by" doc:"Who kept it: platform:actor, or the control panel's user."`
	AddedAt   time.Time `json:"added_at"`
}

func stickerOf(s media.KeptSticker) Sticker {
	return Sticker{ID: s.ID, Kind: string(s.Kind), Text: s.Text, Summary: s.Summary, Note: s.Note, MediaType: s.MediaType,
		Bytes: s.Size, SHA256: hex.EncodeToString(s.SHA256), Packs: append([]string{}, s.Packs...), AddedBy: s.AddedBy, AddedAt: s.AddedAt}
}

// StickerPack is a sticker pack, as the panel manages it.
type StickerPack struct {
	ID          string `json:"id" pattern:"^[a-z0-9][a-z0-9-]{0,31}$" doc:"How chat's settings name it; fixed once it is made."`
	Title       string `json:"title"`
	Description string `json:"description"`
	Stickers    []int  `json:"stickers" nullable:"false" doc:"Its stickers' numbers, in order."`
}

func packOf(p media.StickerPack) StickerPack {
	return StickerPack{ID: p.ID, Title: p.Title, Description: p.Description, Stickers: append([]int{}, p.Stickers...)}
}

// StickerList is every kept sticker and every pack.
type StickerList struct {
	Stickers []Sticker     `json:"stickers" nullable:"false"`
	Packs    []StickerPack `json:"packs" nullable:"false"`
}

// KeptStickerBody is a sticker kept or, when Existing, kept already and now
// in the pack asked for.
type KeptStickerBody struct {
	Sticker
	Existing bool `json:"existing,omitempty" doc:"The picture was kept already: this is that sticker, now in the pack."`
}

// StickerPath names a sticker in a route.
type StickerPath struct {
	ID int `path:"id" minimum:"1" doc:"The sticker's number."`
}

// PackPath names a sticker pack in a route.
type PackPath struct {
	Pack string `path:"pack" doc:"The pack's ID."`
}

// PackBody is a pack's title and description.
type PackBody struct {
	Title       string `json:"title" minLength:"1"`
	Description string `json:"description,omitempty"`
}

// maxStickerUpload bounds an uploaded picture's request: a sticker is at
// most 9 MiB, and the media module says so for anything up to this.
const maxStickerUpload = 10 << 20

// registerStickers serves the media module's stickers and their packs.
// stickers returns the running media module's service, or false when it is
// not running.
func registerStickers(api huma.API, stickers func() (media.StickerAdmin, bool)) {
	running := func() (media.StickerAdmin, error) {
		if s, ok := stickers(); ok {
			return s, nil
		}
		return nil, huma.Error503ServiceUnavailable("the media module is not running")
	}
	// found maps the service's errors to responses: a sticker or pack that
	// is not there is 404, a pack's ID taken 409, an invalid one 422.
	found := func(err error) error {
		switch {
		case errors.Is(err, media.ErrNoSticker), errors.Is(err, media.ErrNoPack):
			return huma.Error404NotFound(err.Error())
		case errors.Is(err, media.ErrPackExists):
			return huma.Error409Conflict(err.Error())
		case errors.Is(err, media.ErrInvalidPack):
			return huma.Error422UnprocessableEntity(err.Error())
		}
		return problem(err)
	}
	noSticker := func(id int, err error) error {
		if errors.Is(err, media.ErrNoSticker) {
			return huma.Error404NotFound("there is no sticker " + strconv.Itoa(id))
		}
		return found(err)
	}
	tags := []string{"Stickers"}
	huma.Register(api, huma.Operation{OperationID: "list-stickers", Method: http.MethodGet, Path: "/api/modules/media/stickers", Summary: "The stickers the media module keeps, and their packs",
		Description: "Every kept sticker by number, those agents do not see (no note and no description) included, and every pack by ID. Answers 503 while the media module is not running.", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body StickerList }, error) {
			s, err := running()
			if err != nil {
				return nil, err
			}
			all, packs, err := s.All(ctx)
			if err != nil {
				return nil, problem(err)
			}
			out := StickerList{Stickers: make([]Sticker, len(all)), Packs: make([]StickerPack, len(packs))}
			for i, st := range all {
				out.Stickers[i] = stickerOf(st)
			}
			for i, p := range packs {
				out.Packs[i] = packOf(p)
			}
			return &struct{ Body StickerList }{out}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "get-sticker-picture", Method: http.MethodGet, Path: "/api/modules/media/stickers/{id}/picture", Summary: "A sticker's picture",
		Description: "The picture itself, with its type. A sticker's picture never changes, so it may be cached for good; its ETag is its SHA-256.", Tags: tags},
		func(ctx context.Context, in *StickerPath) (*huma.StreamResponse, error) {
			s, err := running()
			if err != nil {
				return nil, err
			}
			md, _, err := s.Picture(ctx, in.ID)
			if err != nil {
				return nil, noSticker(in.ID, err)
			}
			etag := ""
			for _, h := range md.Hashes {
				if h.Algorithm == "sha256" {
					etag = `"` + hex.EncodeToString(h.Value) + `"`
				}
			}
			return &huma.StreamResponse{Body: func(hc huma.Context) {
				hc.SetHeader("Content-Type", md.MediaType)
				hc.SetHeader("Cache-Control", "private, max-age=31536000, immutable")
				if etag != "" {
					hc.SetHeader("ETag", etag)
				}
				_, _ = hc.BodyWriter().Write(md.Source.Inline) // the client is gone if this fails
			}}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "add-sticker", Method: http.MethodPost, Path: "/api/modules/media/stickers", Summary: "Keep a picture as a sticker",
		Description: "The body is the picture: PNG, JPEG, or GIF, at most 9 MiB. It is kept as /sticker add keeps one, with no note: the same checks, and it is described first, waiting at most 30 seconds (kept all the same when it cannot be). With pack, it is put in that pack, which must be there (404, answered before the picture is described). Answers 201 with the sticker. A picture kept already is 409 without pack; with pack, that sticker is put in the pack and answered with 200 and existing. Any other refusal is 422, the detail being what /sticker add answers and the errors naming the reason (and the sticker already kept). Recorded as media.sticker by the caller.", Tags: tags,
		DefaultStatus: http.StatusCreated, MaxBodyBytes: maxStickerUpload},
		func(ctx context.Context, in *struct {
			Pack    string `query:"pack" doc:"The ID of the pack to put it in."`
			RawBody []byte `contentType:"application/octet-stream"`
		},
		) (*struct {
			Status int
			Body   KeptStickerBody
		}, error,
		) {
			s, err := running()
			if err != nil {
				return nil, err
			}
			st, existing, err := s.Keep(ctx, in.RawBody, in.Pack, caller(ctx))
			var refusal *media.StickerRefusal
			switch {
			case errors.As(err, &refusal):
				detail := &huma.ErrorDetail{Message: refusal.Reason, Location: "body"}
				if refusal.ID != 0 {
					detail.Value = refusal.ID
				}
				if refusal.Reason == "already kept" {
					return nil, huma.Error409Conflict(refusal.Message, detail)
				}
				return nil, huma.Error422UnprocessableEntity(refusal.Message, detail)
			case err != nil:
				return nil, found(err)
			}
			status := http.StatusCreated
			if existing {
				status = http.StatusOK
			}
			return &struct {
				Status int
				Body   KeptStickerBody
			}{status, KeptStickerBody{Sticker: stickerOf(st), Existing: existing}}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "note-sticker", Method: http.MethodPatch, Path: "/api/modules/media/stickers/{id}", Summary: "Set a sticker's note",
		Description: "An empty note clears it, and agents see the description's summary again. Recorded as media.sticker by the caller.", Tags: tags},
		func(ctx context.Context, in *struct {
			StickerPath
			Body struct {
				Note string `json:"note" doc:"What agents see of the sticker; empty for its description's summary."`
			}
		},
		) (*struct{ Body Sticker }, error) {
			s, err := running()
			if err != nil {
				return nil, err
			}
			st, err := s.SetNote(ctx, in.ID, in.Body.Note, caller(ctx))
			if err != nil {
				return nil, noSticker(in.ID, err)
			}
			return &struct{ Body Sticker }{stickerOf(st)}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "remove-sticker", Method: http.MethodDelete, Path: "/api/modules/media/stickers/{id}", Summary: "Remove a sticker",
		Description: "It leaves its packs too, and its number is not used again. Recorded as media.sticker by the caller.", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *StickerPath) (*struct{}, error) {
			s, err := running()
			if err != nil {
				return nil, err
			}
			if err := s.Remove(ctx, in.ID, caller(ctx)); err != nil {
				return nil, noSticker(in.ID, err)
			}
			return nil, nil
		})

	huma.Register(api, huma.Operation{OperationID: "make-sticker-pack", Method: http.MethodPost, Path: "/api/modules/media/sticker-packs", Summary: "Make a sticker pack",
		Description: "An empty pack. Its ID is how chat's settings name it, and cannot change; 409 when a pack has it already. Recorded as media.sticker_pack by the caller.", Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *struct {
			Body struct {
				ID string `json:"id" pattern:"^[a-z0-9][a-z0-9-]{0,31}$" doc:"Lowercase letters, digits, and hyphens, starting with a letter or digit, at most 32."`
				PackBody
			}
		},
		) (*struct{ Body StickerPack }, error) {
			s, err := running()
			if err != nil {
				return nil, err
			}
			p, err := s.MakePack(ctx, media.StickerPack{ID: in.Body.ID, Title: in.Body.Title, Description: in.Body.Description}, caller(ctx))
			if err != nil {
				return nil, found(err)
			}
			return &struct{ Body StickerPack }{packOf(p)}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "edit-sticker-pack", Method: http.MethodPatch, Path: "/api/modules/media/sticker-packs/{pack}", Summary: "Set a sticker pack's title and description",
		Description: "Recorded as media.sticker_pack by the caller.", Tags: tags},
		func(ctx context.Context, in *struct {
			PackPath
			Body PackBody
		},
		) (*struct{ Body StickerPack }, error) {
			s, err := running()
			if err != nil {
				return nil, err
			}
			p, err := s.EditPack(ctx, in.Pack, in.Body.Title, in.Body.Description, caller(ctx))
			if err != nil {
				return nil, found(err)
			}
			return &struct{ Body StickerPack }{packOf(p)}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "remove-sticker-pack", Method: http.MethodDelete, Path: "/api/modules/media/sticker-packs/{pack}", Summary: "Remove a sticker pack",
		Description: "stickers says what becomes of its stickers: keep keeps them all, those in no other pack then in none; remove removes those in no other pack too, their numbers not used again. Neither can be undone. Agents' settings still naming the pack skip it. Recorded as media.sticker_pack by the caller, and each sticker removed as media.sticker.", Tags: tags},
		func(ctx context.Context, in *struct {
			PackPath
			Stickers string `query:"stickers" required:"true" enum:"keep,remove" doc:"What becomes of its stickers."`
		},
		) (*struct {
			Body struct {
				Removed []int `json:"removed" nullable:"false" doc:"The stickers removed with it."`
			}
		}, error,
		) {
			s, err := running()
			if err != nil {
				return nil, err
			}
			removed, err := s.RemovePack(ctx, in.Pack, media.RemovePack(in.Stickers), caller(ctx))
			if err != nil {
				return nil, found(err)
			}
			out := &struct {
				Body struct {
					Removed []int `json:"removed" nullable:"false" doc:"The stickers removed with it."`
				}
			}{}
			out.Body.Removed = append([]int{}, removed...)
			return out, nil
		})
	huma.Register(api, huma.Operation{OperationID: "pack-sticker", Method: http.MethodPut, Path: "/api/modules/media/sticker-packs/{pack}/stickers/{id}", Summary: "Put a sticker in a pack",
		Description: "204 when it is there already too. Recorded as media.sticker by the caller when it changes anything.", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			PackPath
			StickerPath
		},
		) (*struct{}, error) {
			s, err := running()
			if err != nil {
				return nil, err
			}
			if err := s.Pack(ctx, in.Pack, in.ID, caller(ctx)); err != nil {
				return nil, noSticker(in.ID, err)
			}
			return nil, nil
		})
	huma.Register(api, huma.Operation{OperationID: "unpack-sticker", Method: http.MethodDelete, Path: "/api/modules/media/sticker-packs/{pack}/stickers/{id}", Summary: "Take a sticker out of a pack",
		Description: "204 when it was not there too. Recorded as media.sticker by the caller when it changes anything.", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			PackPath
			StickerPath
		},
		) (*struct{}, error) {
			s, err := running()
			if err != nil {
				return nil, err
			}
			if err := s.Unpack(ctx, in.Pack, in.ID, caller(ctx)); err != nil {
				return nil, noSticker(in.ID, err)
			}
			return nil, nil
		})
}
