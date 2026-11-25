package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/pkg/decision"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	mediasvc "github.com/TundraWork/Ex-Otogi/pkg/media"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// silentRound is a model round saying text, then calling stay_silent and the
// tools named.
func silentRound(text string, also ...string) llm.Response {
	r := llm.Response{Turn: llm.Turn{Role: llm.RoleModel, Calls: []llm.Call{{ID: "q", Name: silentTool, Args: json.RawMessage(`{}`)}}}, Finish: llm.FinishToolCalls}
	if text != "" {
		r.Turn.Content = otogi.TextMessage(text)
	}
	for i, name := range also {
		r.Turn.Calls = append(r.Turn.Calls, llm.Call{ID: "a" + string(rune('0'+i)), Name: name, Args: json.RawMessage(`"hi"`)})
	}
	return r
}

func mentioned(e *otogi.Event) {
	e.Mentioned = true
	e.Message.Parts[0].Annotations = []otogi.Annotation{{Offset: 0, Length: 5, Kind: otogi.AnnotationMention, Target: "user:B1"}}
}

// Each turn ends with the note set for what started it, in that request
// only; follow-ups and chimes left unset have the built-in ones, and other
// turns none.
func TestNotesByTrigger(t *testing.T) {
	t.Parallel()
	notes := &NotesConfig{Mention: "(mentioned)", Reply: "(replied to)", Keyword: "(named)", Decision: "(judged)", Follow: ptr("(following)"), Chime: ptr("(chiming)")}
	with := &profile{ProfileConfig: ProfileConfig{Notes: notes}}
	without := &profile{}
	for trigger, want := range map[string][2]string{
		"mention": {"(mentioned)", ""}, "reply": {"(replied to)", ""}, "keyword": {"(named)", ""}, "decision": {"(judged)", ""},
		"follow": {"(following)", followNote}, "chime": {"(chiming)", chimeNote},
	} {
		if got := with.note(trigger); got != want[0] {
			t.Errorf("%s with notes: %q", trigger, got)
		}
		if got := without.note(trigger); got != want[1] {
			t.Errorf("%s without notes: %q", trigger, got)
		}
	}
	// Setting one note leaves the built-in ones; setting one empty removes it.
	mention := &profile{ProfileConfig: ProfileConfig{Notes: &NotesConfig{Mention: "(mentioned)"}}}
	if got := [2]string{mention.note("follow"), mention.note("chime")}; got != [2]string{followNote, chimeNote} {
		t.Errorf("with only mention set: %q", got)
	}
	none := &profile{ProfileConfig: ProfileConfig{Notes: &NotesConfig{Follow: ptr(""), Chime: ptr("")}}}
	if got := none.note("follow") + none.note("chime"); got != "" {
		t.Errorf("with follow and chime set empty: %q", got)
	}

	h := newHarness(t, ProfileConfig{Name: "luna", Keywords: []string{"luna"}, Mention: true, Provider: "luna", Model: "m", Notes: notes})
	p := h.llm["luna"]
	h.mustHandle(h.message("Alice", "luna hi"))
	h.mustHandle(h.message("Alice", "@Luna hi", mentioned))
	h.mustHandle(h.message("Alice", "and?", replyingTo("agent-2")))
	for i, want := range []string{"(named)", "(mentioned)", "(replied to)"} {
		if got := lastUserText(p.requests[i]); !strings.HasSuffix(got, want) {
			t.Fatalf("request %d ends %q", i, got)
		}
	}
	// The thread keeps the message alone: the reply continuing it does not
	// see the earlier turn's note.
	for _, turn := range p.requests[2].Turns[:len(p.requests[2].Turns)-1] {
		if strings.Contains(turn.Content.PlainText(), "(mentioned)") {
			t.Fatalf("a note stayed in the thread: %q", turn.Content.PlainText())
		}
	}
}

// An agent with silent may end any turn without a reply: nothing goes out,
// the turn is not kept, and the model is asked once. Words said first go out;
// a sticker goes out alone.
func TestSilent(t *testing.T) {
	t.Parallel()
	off := newHarness(t, ProfileConfig{Name: "luna", Keywords: []string{"luna"}, Provider: "luna", Model: "m"})
	off.mustHandle(off.message("Alice", "luna hi"))
	if hasTool(off.llm["luna"].requests[0], silentTool) {
		t.Fatal("stay_silent offered without silent")
	}

	h := newHarness(t, ProfileConfig{Name: "luna", Keywords: []string{"luna"}, Mention: true, Provider: "luna", Model: "m", Silent: true})
	p := h.llm["luna"]
	p.responses = []llm.Response{silentRound("")}
	h.mustHandle(h.message("Alice", "@Luna good night", mentioned))
	rec := lastReply(h)
	if !hasTool(p.requests[0], silentTool) || p.count() != 1 || len(shown(h)) != 0 || rec["skipped"] != "silent" || rec["trigger"] != "mention" || len(rec["turns"].([]int64)) != 0 {
		t.Fatalf("%d requests, shown %q, record %+v", p.count(), shown(h), rec)
	}

	p.responses, p.requests = []llm.Response{silentRound("Night!")}, nil
	h.mustHandle(h.message("Alice", "luna good night"))
	if got := shown(h); len(got) != 1 || got[0] != "Night!" || lastReply(h)["skipped"] != nil {
		t.Fatalf("shown %q, record %+v", got, lastReply(h))
	}
}

// A sticker with stay_silent goes out alone.
func TestSilentWithASticker(t *testing.T) {
	t.Parallel()
	profile := stickerProfile()
	profile.Silent = true
	h := newHarnessWith(t, map[string]any{mediasvc.Service: newDescriber(), mediasvc.StickersService: newStickerBox()}, profile)
	r := picks("", 3)
	r.Turn.Calls = append(r.Turn.Calls, llm.Call{ID: "q", Name: silentTool, Args: json.RawMessage(`{}`)})
	h.llm["otogi"].responses = []llm.Response{r}
	h.mustHandle(h.message("Alice", "otogi good night"))
	if len(shown(h)) != 0 || len(h.sends) != 1 || lastReply(h)["sticker"] == nil {
		t.Fatalf("shown %q, sent %d, record %+v", shown(h), len(h.sends), lastReply(h))
	}
}

// stay_silent with a tool that does not end the reply ends nothing: the model
// is asked again, and what it then says goes out.
func TestSilentWithAnotherTool(t *testing.T) {
	t.Parallel()
	shout := llm.NewTool(llm.Function{Name: "shout", Description: "shouts"}, func(context.Context, otogi.Event, json.RawMessage) (any, error) { return "HI", nil })
	storage, err := modulestore.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	h := &harness{t: t, host: &host{storage: storage, services: map[string]any{"llm.tools/fun": []llm.Tool{shout}}, lists: map[[2]string]string{{"luna", group}: "test", {"tools:fun", group}: "test"}}, llm: map[string]*provider{}, now: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)}
	p := &provider{responses: []llm.Response{silentRound("", "shout"), answer("Shouted.")}}
	h.llm["luna"] = p
	if h.m, err = New(Config{Tools: []string{"fun"}, Profiles: []ProfileConfig{{Name: "luna", Keywords: []string{"luna"}, Provider: "luna", Model: "m", Silent: true}}}, func(string) (llm.Provider, error) { return p, nil }, nil); err != nil {
		t.Fatal(err)
	}
	h.m.now = h.clock
	if err := h.m.Start(context.Background(), h.host); err != nil {
		t.Fatal(err)
	}
	h.mustHandle(h.message("Alice", "luna shout"))
	if got := shown(h); p.count() != 2 || len(got) != 1 || got[0] != "Shouted." {
		t.Fatalf("%d requests, shown %q", p.count(), got)
	}
}

// When the agent stays silent, the messages that came meanwhile are answered
// as the next batch, without asking whether the reply did for them: none
// went out.
func TestSilentLeavesTheNextBatchAnswered(t *testing.T) {
	t.Parallel()
	d := &decider{answer: func(id string, req decision.Request) (float64, bool) {
		if id == coveredID {
			return 0.95, true
		}
		return finished()(id, req)
	}}
	profile := waitingProfile("luna", 20*time.Millisecond, 2*time.Second)
	profile.Silent = true
	h := newJudged(t, d, profile)
	h.llm["luna"].responses = []llm.Response{silentRound(""), answer("6")}
	release := make(chan struct{})
	h.llm["luna"].before = func(n int) {
		if n == 1 {
			<-release
		}
	}
	errc := make(chan error, 2)
	go func() { errc <- h.handle(h.message("Alice", "luna good night")) }()
	eventually(t, "the first reply to start", func() bool { return h.llm["luna"].count() == 1 })
	go func() { errc <- h.handle(h.message("Alice", "luna wait, what is 3+3")) }()
	time.Sleep(100 * time.Millisecond)
	close(release)
	if err := errors.Join(<-errc, <-errc); err != nil {
		t.Fatal(err)
	}
	recs := replies(h)
	if h.llm["luna"].count() != 2 || len(recs) != 2 || recs[0]["skipped"] != "silent" || recs[1]["skipped"] != nil || len(shown(h)) != 1 {
		t.Fatalf("%d requests, records %+v, shown %q", h.llm["luna"].count(), recs, shown(h))
	}
}
