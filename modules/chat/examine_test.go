package chat

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/decision"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	mediasvc "github.com/TundraWork/Ex-Otogi/pkg/media"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// tank is a photo's description with its ID.
var tank = mediasvc.Description{Kind: mediasvc.KindPhoto, Summary: "a tank", ID: "00a0a6"}

// examiner is an agent that reads descriptions and has examine_media.
func examiner(name string) ProfileConfig {
	return ProfileConfig{Name: name, Keywords: []string{name}, Provider: name, Model: "m", Media: &MediaConfig{AsText: true, Examine: &ExamineConfig{}}}
}

// newExamineHarness starts agents with a fake media service that describes
// "tank" and the stickers of newStickerBox.
func newExamineHarness(t *testing.T, profiles ...ProfileConfig) (*harness, *describer) {
	t.Helper()
	media := newDescriber()
	media.kept["tank"] = tank
	return newHarnessWith(t, map[string]any{mediasvc.Service: media, mediasvc.StickersService: newStickerBox()}, profiles...), media
}

// examining is the model calling examine_media with id and question.
func examining(id, question string) llm.Response {
	args, _ := json.Marshal(map[string]string{"id": id, "question": question})
	return llm.Response{Turn: llm.Turn{Role: llm.RoleModel, Calls: []llm.Call{{ID: "x", Name: examineTool, Args: args}}}, Finish: llm.FinishToolCalls}
}

// offers reports whether req offers the function name.
func offers(req llm.Request, name string) bool {
	for _, spec := range req.Tools {
		if spec.Function != nil && spec.Function.Name == name {
			return true
		}
	}
	return false
}

// toolRecords are the chat.tool records' payloads, in order.
func toolRecords(h *harness) []map[string]any {
	h.host.mu.Lock()
	defer h.host.mu.Unlock()
	var out []map[string]any
	for _, r := range h.host.records {
		if r.Kind == "chat.tool" {
			out = append(out, r.Payload.(map[string]any))
		}
	}
	return out
}

// An agent with examine_media is shown its media's IDs, in the recent chat
// (both of Alice's pictures), in the message it answers, and in what that
// quotes, and is offered the tool; another agent is shown neither.
func TestExamineShowsIDs(t *testing.T) {
	sol := examiner("sol")
	sol.Media.Examine = nil
	h, _ := newExamineHarness(t, examiner("otogi"), sol)
	h.mustHandle(h.message("Alice", "", withParts("", photo("tank"))))
	quoted := h.message("Alice", "", withParts("", photo("tank")))
	h.mustHandle(quoted)
	h.mustHandle(h.message("Bob", "otogi which one is it?", withParts("otogi which one is it?", photo("tank")), replyingTo(string(quoted.SourceID))))
	h.mustHandle(h.message("Bob", "sol which one is it?"))
	req := h.llm["otogi"].requests[0]
	turn := lastUserText(req)
	if strings.Count(turn, "[photo #00a0a6: a tank]") != 4 || !offers(req, examineTool) {
		t.Fatalf("otogi's turn %q, tools %+v", turn, req.Tools)
	}
	req = h.llm["sol"].requests[0]
	if turn := lastUserText(req); strings.Contains(turn, "#00a0a6") || !strings.Contains(turn, "[photo: a tank]") || offers(req, examineTool) {
		t.Fatalf("sol's turn %q, tools %+v", turn, req.Tools)
	}
}

// Judgments are shown no IDs: their input stays as it was.
func TestJudgmentsShowNoIDs(t *testing.T) {
	media := newDescriber()
	media.kept["tank"] = tank
	dec := &decider{scores: map[string]float64{"a1": 0.1}}
	luna := judgedProfile("luna", 0.7)
	luna.Media = &MediaConfig{AsText: true, Examine: &ExamineConfig{}}
	h := newHarnessWith(t, map[string]any{decision.Service: dec, mediasvc.Service: media, mediasvc.StickersService: newStickerBox()}, luna)
	h.mustHandle(h.message("Bob", "", withParts("", photo("tank"))))
	if got := dec.requests[0].State.(judgeState).History[0].Text; got != "[photo: a tank]" {
		t.Fatalf("the judgment saw %q", got)
	}
}

// The agent names media by its ID, and gets the answer back to reply with:
// from the original the media module keeps, or else fetched again.
func TestExamineAnswers(t *testing.T) {
	for _, kept := range []bool{false, true} {
		h, media := newExamineHarness(t, examiner("otogi"))
		media.originals = map[string]bool{"tank": kept}
		h.llm["otogi"].responses = []llm.Response{examining("#00a0a6", "which model?"), answer("A T-72.")}
		h.mustHandle(h.message("Alice", "", withParts("", photo("tank"))))
		h.mustHandle(h.message("Bob", "otogi what tank is that?"))
		want := []examineCall{{name: "tank", question: "which model?"}}
		if !kept {
			want = append(want, examineCall{name: "tank", inline: true, question: "which model?"})
		}
		if got := media.examined; len(got) != len(want) || got[0] != want[0] || got[len(got)-1] != want[len(want)-1] {
			t.Fatalf("kept %v: Examine calls %+v, want %+v", kept, got, want)
		}
		rec := toolRecords(h)
		if len(rec) != 1 || string(rec[0]["result"].(json.RawMessage)) != `{"answer":"tank: which model?"}` {
			t.Fatalf("kept %v: chat.tool %+v", kept, rec)
		}
		if got := h.streams[len(h.streams)-1].final.PlainText(); got != "A T-72." {
			t.Fatalf("kept %v: reply %q", kept, got)
		}
	}
}

// Media in the thread's earlier turns is found by its ID, though the recent
// chat leaves those turns out and it has long left the recent chat.
func TestExamineFindsTheThreadsMedia(t *testing.T) {
	p := examiner("otogi")
	p.Context = ptr(2)
	h, media := newExamineHarness(t, p)
	h.llm["otogi"].responses = []llm.Response{answer("Nice tank!"), examining("#00a0a6", "which model?"), answer("A T-72.")}
	h.mustHandle(h.message("Alice", "otogi look", withParts("otogi look", photo("tank"))))
	first := h.streams[0]
	h.echo(first)
	for range 6 {
		h.mustHandle(h.message("Carol", "lunch?"))
	}
	h.mustHandle(h.message("Alice", "which model is it?", replyingTo(string(first.ref.SourceEvent))))
	if len(media.examined) == 0 || media.examined[0].name != "tank" {
		t.Fatalf("Examine calls %+v", media.examined)
	}
	if rec := toolRecords(h); len(rec) != 1 || rec[0]["error"] != nil {
		t.Fatalf("chat.tool %+v", rec)
	}
}

// A kept sticker is named by its number, and looked at from its picture.
func TestExamineSticker(t *testing.T) {
	h, media := newExamineHarness(t, examiner("otogi"))
	h.llm["otogi"].responses = []llm.Response{examining("sticker 3", "what breed?"), examining("sticker 9", "what breed?"), answer("A shiba.")}
	h.mustHandle(h.message("Bob", "otogi what dog is on sticker 3?"))
	if len(media.examined) != 1 || !media.examined[0].inline || media.examined[0].question != "what breed?" {
		t.Fatalf("Examine calls %+v", media.examined)
	}
	if rec := toolRecords(h); len(rec) != 2 || rec[1]["error"] != "there is no sticker 9" {
		t.Fatalf("chat.tool %+v", rec)
	}
}

// When the agent cannot look, it is told why and what to do instead, and
// the reply goes on.
func TestExamineFailures(t *testing.T) {
	for _, c := range []struct {
		name  string
		id    string
		setup func(*harness, *describer)
		want  string
	}{
		{"not an ID", "00a0a6", nil, `"00a0a6": want the ID as the chat shows it, as "#2ce7ad", or "sticker 3"`},
		{"not shown", "#ffffff", nil, "no media #ffffff in this chat's recent messages"},
		{"not fetched", "#00a0a6", func(h *harness, _ *describer) { h.fetchErr = errors.New("404") }, "its original can no longer be fetched; answer from its description"},
		{"unavailable", "#00a0a6", func(_ *harness, d *describer) { d.examineErr = otogi.ErrUnavailable }, "the media model is unavailable now; answer from its description"},
		{"too long", "#00a0a6", func(_ *harness, d *describer) { d.examineBlock = true }, "looking took too long; answer from its description"},
	} {
		p := examiner("otogi")
		if c.name == "too long" {
			// Short, for the test: the other cases keep the default, which
			// a slow run under the race detector stays well within.
			p.Media.Examine.Timeout = otogi.Duration(50 * time.Millisecond)
		}
		h, media := newExamineHarness(t, p)
		if c.setup != nil {
			c.setup(h, media)
		}
		h.llm["otogi"].responses = []llm.Response{examining(c.id, "which model?"), answer("Some tank.")}
		h.mustHandle(h.message("Alice", "", withParts("", photo("tank"))))
		h.mustHandle(h.message("Bob", "otogi what tank is that?"))
		if rec := toolRecords(h); len(rec) != 1 || rec[0]["error"] != c.want {
			t.Fatalf("%s: chat.tool %+v", c.name, rec)
		}
		if got := h.streams[len(h.streams)-1].final.PlainText(); got != "Some tank." {
			t.Fatalf("%s: reply %q", c.name, got)
		}
	}
}
