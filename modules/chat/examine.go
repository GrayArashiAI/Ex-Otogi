package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	mediasvc "github.com/TundraWork/Ex-Otogi/pkg/media"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// examineTool is the function an agent with media.examine asks a question
// of its own about a piece of media with: the media module's models answer
// it from the media itself, which the descriptions the agent is shown name
// by ID. It does not end the reply: the answer comes back for the agent to
// write its reply with.
const examineTool = "examine_media"

var (
	mediaID       = regexp.MustCompile(`^#([0-9a-f]{6})$`)
	stickerNumber = regexp.MustCompile(`^sticker ([1-9][0-9]{0,8})$`)
)

// looker is examine_media in one reply: what it needs to find the media an
// ID names among what the reply was shown.
type looker struct {
	m     *Module
	a     ask
	inv   otogi.Invocation
	path  []storedTurn // the thread's earlier turns
	until time.Time    // when the reply started, which the recent chat it was shown runs to
}

func (l *looker) tool() llm.Tool {
	return llm.NewTool(llm.Function{
		Name: examineTool,
		Description: `Look again at a picture, animation, or video in this chat, to answer what its description leaves out: what exactly something is, a detail, a size, small text. ` +
			`Name it as the chat shows it: the ID after # in its brackets, as "#2ce7ad", or a kept sticker as "sticker 3". You get back what the media model sees; then write your reply.`,
		Parameters: json.RawMessage(`{"type":"object","properties":{` +
			`"id":{"type":"string","description":"the media's ID as the chat shows it, \"#\" and six letters and digits, or \"sticker N\""},` +
			`"question":{"type":"string","description":"what to look for, in full"}},"required":["id","question"]}`),
	}, func(ctx context.Context, _ otogi.Event, args json.RawMessage) (any, error) {
		var in struct {
			ID       string `json:"id"`
			Question string `json:"question"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf(`want {"id": ..., "question": ...}: %w`, err)
		}
		if in.Question == "" {
			return nil, errors.New("the question is empty")
		}
		ctx, cancel := context.WithTimeout(ctx, l.a.p.examine)
		defer cancel()
		answer, err := l.examine(ctx, in.ID, in.Question)
		if err != nil {
			return nil, err
		}
		return map[string]string{"answer": answer}, nil
	})
}

// examine answers question about the media id names, telling the model,
// when it cannot, what to do instead.
func (l *looker) examine(ctx context.Context, id, question string) (string, error) {
	to := l.a.msgs[0].Destination()
	var md otogi.Media
	if n := stickerNumber.FindStringSubmatch(id); n != nil {
		number, _ := strconv.Atoi(n[1])
		var err error
		if md, _, err = l.m.stickers.Picture(ctx, number); errors.Is(err, mediasvc.ErrNoSticker) {
			return "", fmt.Errorf("there is no sticker %d", number)
		} else if err != nil {
			return "", lookFailed(err)
		}
	} else if h := mediaID.FindStringSubmatch(id); h != nil {
		found, ok, err := l.find(ctx, h[1])
		if err != nil {
			return "", lookFailed(err)
		}
		if !ok {
			return "", fmt.Errorf("no media %s in this chat's recent messages", id)
		}
		md = found
	} else {
		return "", fmt.Errorf(`%q: want the ID as the chat shows it, as "#2ce7ad", or "sticker 3"`, id)
	}
	answer, err := l.m.describer.Examine(ctx, to, md, question)
	if errors.Is(err, mediasvc.ErrNotKept) {
		// Not kept by the media module: fetched again, while the platform's
		// link to it lasts.
		data, ferr := l.inv.FetchMedia(ctx, to, md.Source)
		if ferr != nil {
			l.m.host.Logger().WarnContext(ctx, "chat: fetching media to examine", "id", id, "error", ferr)
			if ctx.Err() != nil {
				return "", lookFailed(ctx.Err())
			}
			return "", errors.New("its original can no longer be fetched; answer from its description")
		}
		md.Source, md.ByteSize = otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: data}, int64(len(data))
		answer, err = l.m.describer.Examine(ctx, to, md, question)
	}
	if err != nil {
		return "", lookFailed(err)
	}
	return answer, nil
}

// lookFailed is what the model is told of a look that failed.
func lookFailed(err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return errors.New("looking took too long; answer from its description")
	case errors.Is(err, otogi.ErrUnavailable):
		return errors.New("the media model is unavailable now; answer from its description")
	case errors.Is(err, mediasvc.ErrNotDescribed), errors.Is(err, mediasvc.ErrNotAllowed):
		return errors.New("this media cannot be looked at; answer from its description")
	}
	return err
}

// find returns the media the reply was shown as id: in the messages it
// answers and what they quote, the recent chat, and the thread's earlier
// turns, which the recent chat leaves out. When two share the ID, the
// latest is taken.
func (l *looker) find(ctx context.Context, id string) (otogi.Media, bool, error) {
	to := l.a.msgs[0].Destination()
	type seen struct {
		at time.Time
		md otogi.Media
	}
	var all []seen
	add := func(ln line) {
		for _, md := range ln.Media {
			all = append(all, seen{ln.At, md})
		}
	}
	message := func(source string) error {
		ln, found, err := l.m.store.message(ctx, to, source)
		if found {
			add(ln)
		}
		return err
	}
	until := l.until
	for _, e := range l.a.msgs {
		add(transcriptLine(e))
		if target, ok := replyTarget(e.Message); ok {
			if err := message(string(target.SourceEvent)); err != nil {
				return otogi.Media{}, false, err
			}
		}
		if e.OccurredAt.After(until) {
			until = e.OccurredAt
		}
	}
	lines, err := l.m.store.recent(ctx, to, until.Add(time.Millisecond), l.a.p.context+len(l.path)+len(l.a.msgs))
	if err != nil {
		return otogi.Media{}, false, err
	}
	for _, ln := range lines {
		add(ln)
	}
	for _, t := range l.path {
		if t.SourceID != "" {
			if err := message(t.SourceID); err != nil {
				return otogi.Media{}, false, err
			}
		}
	}
	slices.SortStableFunc(all, func(a, b seen) int { return b.at.Compare(a.at) })
	for _, s := range all {
		d, ok, err := l.m.describer.Lookup(ctx, to, s.md)
		if err != nil {
			return otogi.Media{}, false, err
		}
		if ok && d.Sticker == 0 && d.ID == id {
			return s.md, true, nil
		}
	}
	return otogi.Media{}, false, nil
}
