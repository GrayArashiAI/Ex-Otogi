package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	mediasvc "github.com/TundraWork/Ex-Otogi/pkg/media"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// resendTool is the function an agent with media.resend sends a picture or
// animation of the chat again with, named by the ID its description shows.
// It ends the reply, as send_sticker does: the picture follows the reply's
// words as a message of its own.
const resendTool = "resend_media"

// pictureSlot is the effect slot a reply's picture sent again goes in.
const pictureSlot = "picture"

// onePerReply is what a second picture or sticker picked in a reply
// answers. It is no error, so the reply still ends; the first one picked is
// sent.
var onePerReply = map[string]any{"sent": false, "reason": "one picture or sticker a reply"}

// resender is resend_media in one reply: the picture picked, with its
// bytes, its ID, and its media as its original message has it, which the
// agent's line reads as. taken is shared with the reply's sticker pick:
// whether the reply sends a picture or sticker already.
type resender struct {
	look   *looker
	taken  *bool
	picked *otogi.Media
	id     string
	shown  otogi.Media
}

func (r *resender) tool() llm.Tool {
	return llm.NewEndingTool(llm.Function{
		Name: resendTool,
		Description: `Send a picture or animation of this chat again when someone asks for it, named as the chat shows it: the ID after # in its brackets, as "#2ce7ad". ` +
			"It goes as a message of its own after what you wrote. This ends your reply: write anything you want to say before calling it. One picture or sticker per reply.",
		Parameters: json.RawMessage(`{"type":"object","properties":{` +
			`"id":{"type":"string","description":"the picture's ID as the chat shows it, \"#\" and six letters and digits"}},"required":["id"]}`),
	}, func(ctx context.Context, _ otogi.Event, args json.RawMessage) (any, error) {
		var in struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf(`want {"id": ...}: %w`, err)
		}
		if *r.taken {
			return onePerReply, nil
		}
		md, shown, id, err := r.original(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		r.picked, r.shown, r.id, *r.taken = &md, shown, id, true
		return map[string]bool{"sent": true}, nil
	})
}

// original finds the picture id names among what the reply was shown and
// gets its bytes: the media module's copy, else fetched again. shown is the
// picture as the chat has it, before its source became bytes. It tells the
// model, when it cannot, what to do instead.
func (r *resender) original(ctx context.Context, id string) (sent, shown otogi.Media, key string, err error) {
	if stickerNumber.MatchString(id) {
		return sent, shown, "", errors.New("that is a kept sticker: send it with " + stickerTool)
	}
	h := mediaID.FindStringSubmatch(id)
	if h == nil {
		return sent, shown, "", fmt.Errorf(`%q: want the ID as the chat shows it, as "#2ce7ad"`, id)
	}
	shown, ok, err := r.look.find(ctx, h[1])
	if err != nil {
		return sent, shown, "", err
	}
	if !ok {
		return sent, shown, "", fmt.Errorf("no picture %s in this chat's recent messages", id)
	}
	switch shown.Role {
	case otogi.MediaImage, otogi.MediaAnimation, otogi.MediaSticker:
	case otogi.MediaVideo:
		return sent, shown, "", errors.New("videos cannot be sent again")
	default:
		return sent, shown, "", errors.New("only pictures and animations can be sent again")
	}
	to := r.look.a.msgs[0].Destination()
	sent, err = r.look.m.describer.Original(ctx, to, shown)
	switch {
	case err == nil:
		return sent, shown, h[1], nil
	case errors.Is(err, mediasvc.ErrNotKept):
		// Not kept by the media module: fetched again, while the platform's
		// link to it lasts.
		data, ferr := r.look.inv.FetchMedia(ctx, to, shown.Source)
		if ferr != nil {
			r.look.m.host.Logger().WarnContext(ctx, "chat: fetching media to send again", "id", id, "error", ferr)
			return sent, shown, "", errors.New("its original can no longer be fetched; say so in words")
		}
		sent = shown
		sent.Source, sent.ByteSize = otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: data}, int64(len(data))
		return sent, shown, h[1], nil
	case errors.Is(err, mediasvc.ErrNotAllowed):
		return sent, shown, "", errors.New("this chat's pictures cannot be sent again")
	}
	return sent, shown, "", err
}

// sentPicture is how chat.reply records a picture sent again: its ID, and
// the sent message's source ID, empty when it was not sent.
type sentPicture struct {
	ID    string `json:"id"`
	Ref   string `json:"ref,omitempty"`
	Error string `json:"error,omitempty"` // why it did not go
}

// sendPicture sends md as a message of its own, without the agent's label,
// replying to e when the reply quotes.
func (m *Module) sendPicture(ctx context.Context, e otogi.Event, inv otogi.Invocation, md otogi.Media, quote bool) (otogi.EventRef, error) {
	msg := otogi.Message{Roots: []otogi.PartID{"p"}, Parts: []otogi.Part{{ID: "p", Kind: otogi.PartMedia, Media: &md}}}
	if quote {
		msg.Relations = []otogi.Relation{{Kind: otogi.RelationReply, Target: e.Ref()}}
	}
	ref, err := inv.Send(ctx, pictureSlot, msg)
	if err != nil {
		return ref, fmt.Errorf("send the picture again: %w", err)
	}
	return ref, nil
}
