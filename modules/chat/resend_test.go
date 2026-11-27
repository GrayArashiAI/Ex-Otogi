package chat

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	mediasvc "github.com/TundraWork/Ex-Otogi/pkg/media"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// resenderProfile is an agent that reads descriptions and has resend_media, not
// examine_media.
func resenderProfile(name string) ProfileConfig {
	p := examiner(name)
	p.Media.Examine, p.Media.Resend = nil, true
	return p
}

// video is a video message part named name, which the fake describer keys
// on.
func video(name string) otogi.Part {
	return otogi.Part{ID: otogi.PartID(name), Kind: otogi.PartMedia, Media: &otogi.Media{Role: otogi.MediaVideo,
		Source: otogi.MediaSource{Kind: otogi.MediaSourceOpaque, Locator: otogi.ProviderLocator{Data: []byte(name)}}}}
}

// resends is the model saying text, if any, and calling resend_media with
// each id.
func resends(text string, ids ...string) llm.Response {
	r := llm.Response{Turn: llm.Turn{Role: llm.RoleModel}, Finish: llm.FinishToolCalls}
	if text != "" {
		r.Turn.Content = otogi.TextMessage(text)
	}
	for i, id := range ids {
		args, _ := json.Marshal(map[string]string{"id": id})
		r.Turn.Calls = append(r.Turn.Calls, llm.Call{ID: fmt.Sprintf("r%d", i), Name: resendTool, Args: args})
	}
	return r
}

// An agent with resend_media sees IDs and is offered the tool, without
// examine_media. The picture it names follows its words as a message of
// its own, quoting as the reply does, from the media module's copy or else
// fetched again, and chat.reply records it.
func TestResend(t *testing.T) {
	for _, kept := range []bool{true, false} {
		h, media := newExamineHarness(t, resenderProfile("otogi"))
		media.originals = map[string]bool{"tank": kept}
		p := h.llm["otogi"]
		p.responses = []llm.Response{resends("Here it is.", "#00a0a6")}
		h.mustHandle(h.message("Alice", "", withParts("", photo("tank"))))
		ask := h.message("Bob", "otogi send that tank again")
		h.mustHandle(ask)
		req := p.requests[0]
		if !strings.Contains(lastUserText(req), "[photo #00a0a6: a tank]") || !offers(req, resendTool) || offers(req, examineTool) {
			t.Fatalf("kept %v: turn %q, tools %+v", kept, lastUserText(req), req.Tools)
		}
		if len(p.requests) != 1 || h.streams[0].final.PlainText() != "Here it is." {
			t.Fatalf("kept %v: requests %d, reply %q", kept, len(p.requests), h.streams[0].final.PlainText())
		}
		want := "kept bytes of tank"
		if !kept {
			want = "bytes of tank"
		}
		if len(h.sends) != 1 {
			t.Fatalf("kept %v: sends %+v", kept, h.sends)
		}
		s := h.sends[0]
		if s.slot != pictureSlot || len(s.msg.Parts) != 1 || s.msg.Parts[0].Media == nil || s.msg.Parts[0].Media.Role != otogi.MediaImage ||
			string(s.msg.Parts[0].Media.Source.Inline) != want || len(s.msg.Relations) != 1 || s.msg.Relations[0].Target.SourceEvent != ask.SourceID {
			t.Fatalf("kept %v: picture message %+v", kept, s)
		}
		r, payload := lastReplyRecord(t, h)
		resent, ok := payload["resent"].(*sentPicture)
		if r.Outcome != "ok" || !ok || resent.ID != "00a0a6" || resent.Ref == "" {
			t.Fatalf("kept %v: record %v %+v", kept, r.Outcome, payload)
		}

		// The picture comes back as QQ gives it, as the text [image] with no
		// attachment; the recent messages read it as the picture it was,
		// not as the bytes sent.
		h.mustHandle(echoed(h, resent.Ref, "[image]"))
		p.responses = []llm.Response{answer("A tank.")}
		h.mustHandle(h.message("Carol", "otogi what did you just send?"))
		if got := lastUserText(p.requests[1]); !strings.Contains(got, "otogi (this account): [photo #00a0a6: a tank]") || strings.Contains(got, "[image]") {
			t.Fatalf("kept %v: recent messages %q", kept, got)
		}
	}
}

// One picture or sticker per reply: a second, a picture or a sticker, in
// the same round or later, answers sent false, no error, so the reply ends
// without asking the model again, and only the first is sent.
func TestOnePictureOrStickerPerReply(t *testing.T) {
	p := resenderProfile("otogi")
	p.Media.Stickers = []int{1, 3}
	for _, c := range []struct {
		name  string
		resp  llm.Response
		slot  string
		calls int
	}{
		{"two pictures", resends("", "#00a0a6", "#00a0a6"), pictureSlot, 2},
		{"a picture, then a sticker", func() llm.Response {
			r := resends("", "#00a0a6")
			r.Turn.Calls = append(r.Turn.Calls, picks("", 1).Turn.Calls...)
			return r
		}(), pictureSlot, 2},
		{"a sticker, then a picture", func() llm.Response {
			r := picks("", 1)
			r.Turn.Calls = append(r.Turn.Calls, resends("", "#00a0a6").Turn.Calls...)
			return r
		}(), stickerSlot, 2},
	} {
		h, _ := newExamineHarness(t, p)
		h.llm["otogi"].responses = []llm.Response{c.resp}
		h.mustHandle(h.message("Alice", "", withParts("", photo("tank"))))
		h.mustHandle(h.message("Bob", "otogi both please"))
		if n := len(h.llm["otogi"].requests); n != 1 {
			t.Fatalf("%s: the model was asked %d times", c.name, n)
		}
		if len(h.sends) != 1 || h.sends[0].slot != c.slot {
			t.Fatalf("%s: sends %+v", c.name, h.sends)
		}
		rec := toolRecords(h)
		if len(rec) != c.calls || rec[1]["error"] != nil || string(rec[1]["result"].(json.RawMessage)) != `{"reason":"one picture or sticker a reply","sent":false}` {
			t.Fatalf("%s: chat.tool %+v", c.name, rec)
		}
	}
}

// When a picture cannot be sent again, the agent is told why and what to
// do instead, and the reply goes on in words, with nothing sent.
func TestResendFailures(t *testing.T) {
	for _, c := range []struct {
		name  string
		id    string
		setup func(*harness, *describer)
		want  string
	}{
		{"not an ID", "00a0a6", nil, `"00a0a6": want the ID as the chat shows it, as "#2ce7ad"`},
		{"a kept sticker", "sticker 3", nil, "that is a kept sticker: send it with send_sticker"},
		{"not shown", "#ffffff", nil, "no picture #ffffff in this chat's recent messages"},
		{"a video", "#0c1d00", nil, "videos cannot be sent again"},
		{"not fetched", "#00a0a6", func(h *harness, _ *describer) { h.fetchErr = errors.New("404") }, "its original can no longer be fetched; say so in words"},
	} {
		h, media := newExamineHarness(t, resenderProfile("otogi"))
		media.kept["clip"] = mediasvc.Description{Kind: mediasvc.KindVideo, Summary: "a tank driving", ID: "0c1d00"}
		if c.setup != nil {
			c.setup(h, media)
		}
		h.llm["otogi"].responses = []llm.Response{resends("", c.id), answer("I can't send it.")}
		h.mustHandle(h.message("Alice", "", withParts("", photo("tank"), video("clip"))))
		h.mustHandle(h.message("Bob", "otogi send it again"))
		if rec := toolRecords(h); len(rec) != 1 || rec[0]["error"] != c.want {
			t.Fatalf("%s: chat.tool %+v", c.name, rec)
		}
		if len(h.sends) != 0 || h.streams[len(h.streams)-1].final.PlainText() != "I can't send it." {
			t.Fatalf("%s: sends %+v, reply %q", c.name, h.sends, h.streams[len(h.streams)-1].final.PlainText())
		}
		if _, payload := lastReplyRecord(t, h); payload["resent"] != nil {
			t.Fatalf("%s: a reply that sent nothing recorded %+v", c.name, payload["resent"])
		}
	}
}

// A picture the platform refuses: the agent is asked again, its call's
// result now saying the picture did not go and why, and what it writes
// then goes out after its reply, as a message of its own. The reply is
// ok, the failure recorded with the picture; the thread keeps the failed
// result and what the agent added.
func TestResendRefused(t *testing.T) {
	h, media := newExamineHarness(t, resenderProfile("otogi"))
	media.originals = map[string]bool{"tank": true}
	h.sendErr, h.failSlot = errors.New("upload refused"), pictureSlot
	p := h.llm["otogi"]
	p.responses = []llm.Response{resends("Here.", "#00a0a6"), answer("It won't go through, sorry.")}
	h.mustHandle(h.message("Alice", "", withParts("", photo("tank"))))
	h.mustHandle(h.message("Bob", "otogi again"))
	if len(p.requests) != 2 {
		t.Fatalf("the model was asked %d times", len(p.requests))
	}
	turns := p.requests[1].Turns
	last := turns[len(turns)-1]
	if last.Role != llm.RoleTool || len(last.Results) != 1 || last.Results[0].Output != nil ||
		!strings.HasPrefix(last.Results[0].Error, "not sent: send the picture again: upload refused.") {
		t.Fatalf("asked again after %+v", last)
	}
	if h.streams[0].final.PlainText() != "Here." || len(h.sends) != 1 || h.sends[0].slot != afterSlot ||
		h.sends[0].msg.PlainText() != "It won't go through, sorry." || len(h.sends[0].msg.Relations) != 1 {
		t.Fatalf("reply %q, sends %+v", h.streams[0].final.PlainText(), h.sends)
	}
	r, payload := lastReplyRecord(t, h)
	resent, _ := payload["resent"].(*sentPicture)
	after, _ := payload["after_failure"].(*afterFailed)
	if r.Outcome != "ok" || resent == nil || resent.Ref != "" || !strings.Contains(resent.Error, "upload refused") ||
		after == nil || after.Ref != "agent-2" || after.Error != "" {
		t.Fatalf("record %v %+v", r.Outcome, payload)
	}
	// The next reply in the thread reads the failed result and what the
	// agent added.
	p.responses = []llm.Response{answer("ok")}
	h.mustHandle(h.message("Bob", "otogi why?", replyingTo("agent-2")))
	var failed, added bool
	for _, turn := range p.requests[2].Turns {
		for _, res := range turn.Results {
			failed = failed || strings.HasPrefix(res.Error, "not sent: ")
		}
		added = added || turn.Role == llm.RoleModel && turn.Content.PlainText() == "It won't go through, sorry."
	}
	if !failed || !added {
		t.Fatalf("thread continued with %+v", p.requests[2].Turns)
	}
}

// The agent's words keep their order when its picture fails in a later
// round: what it said with a first call, what it wrote with the picture,
// then what it adds once the picture did not go.
func TestFailedPictureKeepsOrder(t *testing.T) {
	prof := resenderProfile("otogi")
	prof.Media.Examine = &ExamineConfig{}
	h, media := newExamineHarness(t, prof)
	media.originals = map[string]bool{"tank": true}
	h.sendErr, h.failSlot = errors.New("upload refused"), pictureSlot
	look := examining("#00a0a6", "what is it?")
	look.Turn.Content = otogi.TextMessage("Let me look.")
	p := h.llm["otogi"]
	p.responses = []llm.Response{look, resends("It's a tank.", "#00a0a6"), answer("The picture won't go, though.")}
	h.mustHandle(h.message("Alice", "", withParts("", photo("tank"))))
	h.mustHandle(h.message("Bob", "otogi what is that? send it again"))
	if len(h.streams) != 2 || h.streams[0].final.PlainText() != "Let me look." || h.streams[0].ref.SourceEvent != "agent-1" ||
		h.streams[1].final.PlainText() != "It's a tank." || h.streams[1].ref.SourceEvent != "agent-2" {
		t.Fatalf("streams %+v", h.streams)
	}
	if _, payload := lastReplyRecord(t, h); len(h.sends) != 1 || payload["after_failure"].(*afterFailed).Ref != "agent-3" {
		t.Fatalf("sends %+v, record %+v", h.sends, payload)
	}
}

// Asked again, the agent may call no function: a call is answered by
// asking it to answer with what it has, and only its words go out; when
// it writes nothing, nothing more goes out.
func TestAfterFailureOnlyWords(t *testing.T) {
	for _, c := range []struct {
		name  string
		after []llm.Response
		want  string
	}{
		{"calls again", []llm.Response{resends("", "#00a0a6"), answer("Sorry, it won't go.")}, "Sorry, it won't go."},
		{"writes nothing", []llm.Response{answer("")}, ""},
	} {
		h, media := newExamineHarness(t, resenderProfile("otogi"))
		media.originals = map[string]bool{"tank": true}
		h.sendErr, h.failSlot = errors.New("upload refused"), pictureSlot
		p := h.llm["otogi"]
		p.responses = append([]llm.Response{resends("", "#00a0a6")}, c.after...)
		h.mustHandle(h.message("Alice", "", withParts("", photo("tank"))))
		h.mustHandle(h.message("Bob", "otogi again"))
		if c.want == "" {
			if len(h.sends) != 0 {
				t.Fatalf("%s: sends %+v", c.name, h.sends)
			}
			continue
		}
		if len(h.sends) != 1 || h.sends[0].msg.PlainText() != c.want {
			t.Fatalf("%s: sends %+v", c.name, h.sends)
		}
		turns := p.requests[2].Turns
		if last := turns[len(turns)-1]; len(toolRecords(h)) != 1 || len(last.Results) != 1 || last.Results[0].Error != "too many function calls in this reply; answer with what you have" {
			t.Fatalf("%s: the second call ran, or was answered %+v", c.name, last)
		}
	}
}

// A picture that goes asks the model nothing more.
func TestSentPictureAsksNothingMore(t *testing.T) {
	h, media := newExamineHarness(t, resenderProfile("otogi"))
	media.originals = map[string]bool{"tank": true}
	h.llm["otogi"].responses = []llm.Response{resends("", "#00a0a6")}
	h.mustHandle(h.message("Alice", "", withParts("", photo("tank"))))
	h.mustHandle(h.message("Bob", "otogi again"))
	if _, payload := lastReplyRecord(t, h); len(h.llm["otogi"].requests) != 1 || payload["after_failure"] != nil {
		t.Fatalf("requests %d, record %+v", len(h.llm["otogi"].requests), payload)
	}
}

// echoed is the platform giving back the account's own message source, as
// text and no media, as QQ gives back a picture or sticker the bot sent.
func echoed(h *harness, source, text string) otogi.Event {
	return h.message("Otogi", text, func(e *otogi.Event) {
		e.SourceID, e.Direction = otogi.SourceEventID(source), otogi.DirectionOutgoing
	})
}

// A picture sent again reads as its original does for each agent: as a
// photo to one that reads no descriptions, and as its description in a
// quote to one that reads them as text.
func TestResentPictureReadsAsItsOriginal(t *testing.T) {
	plain := resenderProfile("luna")
	plain.Media = nil
	h, _ := newExamineHarness(t, resenderProfile("otogi"), plain)
	p := h.llm["otogi"]
	p.responses = []llm.Response{resends("", "#00a0a6")}
	h.mustHandle(h.message("Alice", "", withParts("", photo("tank"))))
	h.mustHandle(h.message("Bob", "otogi send that tank again"))
	_, payload := lastReplyRecord(t, h)
	ref := payload["resent"].(*sentPicture).Ref
	h.mustHandle(echoed(h, ref, "[image]"))

	h.llm["luna"].responses = []llm.Response{answer("A picture.")}
	h.mustHandle(h.message("Carol", "luna what did otogi send?"))
	if got := lastUserText(h.llm["luna"].requests[0]); !strings.Contains(got, "otogi (this account): [photo]") || strings.Contains(got, "[image]") {
		t.Fatalf("recent messages without descriptions %q", got)
	}

	p.responses = []llm.Response{answer("Yes.")}
	h.mustHandle(h.message("Dave", "otogi is this a tank?", replyingTo(ref)))
	if got := lastUserText(p.requests[len(p.requests)-1]); !strings.Contains(got, "[photo #00a0a6: a tank]") {
		t.Fatalf("quote %q", got)
	}
}
