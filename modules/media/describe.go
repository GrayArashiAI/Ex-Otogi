package media

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	contract "github.com/TundraWork/Ex-Otogi/pkg/media"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
	"github.com/TundraWork/Ex-Otogi/pkg/webpanim"
)

// The answer is asked for in labeled sections, one line each but the text,
// not JSON: on eku's pictures with Gemini 3.5 Flash-Lite, JSON came back in
// code fences and with Chinese mangled in \u escapes, and a describe
// function got answers in English whatever the language asked for, while
// labeled sections all parsed, all in the language asked for.
const (
	imagePrompt = `You describe a picture sent in a group chat for someone who cannot see it. Reply in exactly these three sections and nothing else:
kind: one of screenshot, photo, sticker, meme, other
description: what it shows and, for a sticker or meme, the feeling it conveys; one or two sentences on one line, written in %[1]s
text: the text on it, copied exactly as it is, not translated, which may run over several lines; leave it empty when there is none`
	animationNote = "This is an animated sticker or GIF: its %d frames in time order, %.1f seconds in all. Describe what happens and the feeling it conveys as one whole, not frame by frame."
	videoPrompt   = `You describe a video sent in a group chat for someone who cannot watch it. Reply in exactly these two sections and nothing else:
description: what happens in it; one or two sentences on one line, written in %[1]s
text: what is said in it and its captions, copied exactly as they are, not translated, which may run over several lines; leave it empty when there is none`
)

var (
	section = regexp.MustCompile(`(?m)^[ \t]*(kind|description|text):`)
	kinds   = map[string]contract.Kind{
		"screenshot": contract.KindScreenshot, "photo": contract.KindPhoto, "sticker": contract.KindSticker,
		"meme": contract.KindMeme, "other": contract.KindOther,
	}
)

// parse reads an answer in the sections asked for: kind, description, and
// text for a picture; description and text for a video. Sections out of
// order or missing, an unknown kind, or an empty description fail. The text
// runs to the end of the answer, so text that holds a line like "kind:" is
// copied, not read as a section.
func parse(answer string, video bool) (contract.Description, error) {
	answer = strings.TrimSpace(strings.ReplaceAll(answer, "\r\n", "\n"))
	want := []string{"kind", "description", "text"}
	if video {
		want = want[1:]
	}
	var values []string
	rest := answer
	for i, name := range want {
		loc := section.FindStringSubmatchIndex(rest)
		if loc == nil || rest[loc[2]:loc[3]] != name || (i == 0 && strings.TrimSpace(rest[:loc[0]]) != "") {
			return contract.Description{}, fmt.Errorf("the answer is not in the sections asked for (%s)", strings.Join(want, ", "))
		}
		if i > 0 {
			values = append(values, strings.TrimSpace(rest[:loc[0]]))
		}
		rest = rest[loc[1]:]
		if name == "text" {
			values = append(values, strings.TrimSpace(rest))
			break
		}
	}
	d := contract.Description{Kind: contract.KindVideo}
	if !video {
		k, ok := kinds[strings.ToLower(values[0])]
		if !ok {
			return contract.Description{}, fmt.Errorf("unknown kind %q", values[0])
		}
		d.Kind, values = k, values[1:]
	}
	d.Summary, d.Text = strings.Join(strings.Fields(values[0]), " "), values[1]
	if d.Summary == "" {
		return contract.Description{}, fmt.Errorf("the description is empty")
	}
	return d, nil
}

// prepared is media made ready for a model: the parts to send and how to
// read the answer.
type prepared struct {
	system string
	parts  []otogi.Part
	video  bool
	frames int // the animation's frame count, or 0
}

// prepare turns fetched media into what a model is given, by its content:
// QQ's file names and types often disagree with it. It fails with
// contract.ErrNotDescribed for content that is not an image or a video.
func (m *Module) prepare(md otogi.Media, data []byte) (prepared, error) {
	detected := http.DetectContentType(data)
	part := func(i int, mime string, b []byte) otogi.Part {
		return otogi.Part{ID: otogi.PartID(fmt.Sprintf("m%d", i)), Kind: otogi.PartMedia,
			Media: &otogi.Media{Role: otogi.MediaImage, MediaType: mime, Source: otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: b}}}
	}
	text := func(s string) otogi.Part { return otogi.Part{ID: "t", Kind: otogi.PartText, Text: s} }
	animated, _ := webpanim.Animated(data)
	switch {
	case detected == "image/gif" || detected == "image/webp" && animated:
		decode := gifFrames
		if detected == "image/webp" {
			decode = webpFrames
		}
		frames, count, total, err := decode(data, animationFrames)
		if err != nil {
			return prepared{}, fmt.Errorf("%w: %w", contract.ErrNotDescribed, err)
		}
		p := prepared{system: fmt.Sprintf(imagePrompt, m.language)}
		if count < minAnimationFrames {
			p.parts = []otogi.Part{text("Describe this picture."), part(0, "image/png", frames[0])}
			return p, nil
		}
		p.parts, p.frames = []otogi.Part{text(fmt.Sprintf(animationNote, len(frames), total.Seconds()))}, count
		for i, f := range frames {
			p.parts = append(p.parts, part(i, "image/png", f))
		}
		return p, nil
	case detected == "image/png" || detected == "image/jpeg" || detected == "image/webp":
		return prepared{system: fmt.Sprintf(imagePrompt, m.language), parts: []otogi.Part{text("Describe this picture."), part(0, detected, data)}}, nil
	case strings.HasPrefix(detected, "video/") || md.Role == otogi.MediaVideo && strings.HasPrefix(md.MediaType, "video/"):
		mime := detected
		if !strings.HasPrefix(mime, "video/") {
			mime = md.MediaType
		}
		v := part(0, mime, data)
		v.Media.Role = otogi.MediaVideo
		return prepared{system: fmt.Sprintf(videoPrompt, m.language), parts: []otogi.Part{text("Describe this video."), v}, video: true}, nil
	}
	return prepared{}, fmt.Errorf("%w: content is %s", contract.ErrNotDescribed, detected)
}

// describe tries the steps in order until one describes p, recording each
// failure as media.failed and the success as media.described. It returns
// otogi.ErrUnavailable when every step failed, and ctx's error once ctx
// ends.
func (m *Module) describe(ctx context.Context, from string, k key, p prepared, size int) (contract.Description, string, error) {
	var ids []otogi.PartID
	for _, part := range p.parts {
		ids = append(ids, part.ID)
	}
	turn := llm.Turn{Role: llm.RoleUser, Content: otogi.Message{Roots: ids, Parts: p.parts}}
	for i, s := range m.steps {
		if err := ctx.Err(); err != nil {
			return contract.Description{}, "", err
		}
		base := map[string]any{"from": from, "key": k.String(), "provider": s.Provider, "model": s.Model, "step": i + 1}
		started := m.now()
		resp, err := s.provider.Generate(ctx, llm.Request{Model: s.Model, System: p.system, Thinking: s.Thinking, Turns: []llm.Turn{turn}}, nil)
		took := m.now().Sub(started)
		var d contract.Description
		switch {
		case err != nil:
			base["error"] = err.Error()
		case resp.Finish != llm.FinishStop:
			base["finish"], base["reason"] = resp.Finish, resp.Reason
		case len(resp.Withheld) > 0:
			base["error"], base["withheld"] = "the model did not get the media", resp.Withheld
		default:
			if d, err = parse(resp.Markdown, p.video); err != nil {
				base["error"], base["answer"] = err.Error(), resp.Markdown
			}
		}
		if _, failed := base["error"]; failed || base["finish"] != nil {
			m.host.Record(ctx, otogi.Record{Kind: "media.failed", Outcome: "error", Duration: took, Payload: base})
			m.host.Logger().WarnContext(ctx, "media: a step failed", "key", k.String(), "step", i+1, "model", s.Model, "error", base["error"], "finish", base["finish"])
			continue
		}
		base["kind"], base["summary"], base["text"] = d.Kind, d.Summary, d.Text
		base["input_tokens"], base["output_tokens"], base["bytes"] = resp.Usage.Input, resp.Usage.Output, size
		if p.frames > 0 {
			base["frames"] = p.frames
		}
		m.host.Record(ctx, otogi.Record{Kind: "media.described", Outcome: "ok", Duration: took, Payload: base})
		return d, s.Provider + "/" + s.Model, nil
	}
	if err := ctx.Err(); err != nil {
		return contract.Description{}, "", err
	}
	return contract.Description{}, "", fmt.Errorf("media: every step failed: %w", otogi.ErrUnavailable)
}

// skip records media that is not described.
func (m *Module) skip(ctx context.Context, from string, md otogi.Media, size int64, reason string) {
	m.host.Record(ctx, otogi.Record{Kind: "media.skipped", Outcome: "skipped", Payload: map[string]any{
		"from": from, "role": md.Role, "media_type": md.MediaType, "bytes": size, "reason": reason}})
}
