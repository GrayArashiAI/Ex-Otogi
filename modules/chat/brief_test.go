package chat

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/decision"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

func lookingProfile(name string) ProfileConfig {
	return ProfileConfig{Name: name, Keywords: []string{name}, Provider: name, Model: "m", Tools: []string{llm.BuiltinWebSearch}, Judge: &JudgeConfig{Brief: &BriefConfig{}}}
}

func lookupScore(score float64) func(string, decision.Request) (float64, bool) {
	return func(id string, _ decision.Request) (float64, bool) {
		if id == lookupID {
			return score, true
		}
		return 0, false
	}
}

// An agent asked whether to look things up is told to, in the turn, when the
// judgment says it should, and not otherwise.
func TestLookupNote(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		score float64
		note  bool
	}{{0.9, true}, {0.3, false}} {
		d := &decider{answer: lookupScore(c.score)}
		h := newJudged(t, d, lookingProfile("luna"))
		h.mustHandle(h.message("Alice", "luna which Unity version does VRChat use now"))
		req := h.llm["luna"].requests[0]
		if got := strings.HasSuffix(lastUserText(req), lookupNote); got != c.note {
			t.Errorf("score %v: note %v, want %v: %q", c.score, got, c.note, lastUserText(req))
		}
		if strings.Contains(req.System, lookupNote) {
			t.Errorf("score %v: the note is in the system prompt", c.score)
		}
		if len(d.requests) != 1 || d.requests[0].Questions[lookupID].Instructions == "" {
			t.Fatalf("score %v: decision requests %+v", c.score, d.requests)
		}
		if _, ok := d.requests[0].State.(briefState); !ok {
			t.Errorf("score %v: state %T", c.score, d.requests[0].State)
		}
		got, ok := lastReply(h)["lookup"].(*briefed)
		if !ok || got.Score == nil || *got.Score != c.score || got.Threshold != 0.7 {
			t.Errorf("score %v: record %+v", c.score, lastReply(h)["lookup"])
		}
		// The note went with that request only: the thread keeps the turn without it.
		h.mustHandle(h.message("Bob", "and you?", replyingTo("agent-1")))
		next := h.llm["luna"].requests[1]
		if len(next.Turns) < 2 || strings.Contains(next.Turns[0].Content.PlainText(), lookupNote) {
			t.Errorf("score %v: the thread kept the note: %+v", c.score, next.Turns)
		}
	}
}

// Private chats are asked too.
func TestLookupInPrivate(t *testing.T) {
	t.Parallel()
	d := &decider{answer: lookupScore(0.9)}
	h := newJudged(t, d, lookingProfile("luna"))
	h.host.lists[[2]string{"luna", "telegram:dm:1"}] = "test"
	h.mustHandle(h.message("Alice", "luna which Unity version does VRChat use now", func(e *otogi.Event) {
		e.Conversation = otogi.Conversation{ID: "dm:1", Kind: otogi.ConversationDirect}
	}))
	if len(d.requests) != 1 || d.requests[0].Questions[lookupID].Instructions == "" {
		t.Fatalf("decision requests %+v", d.requests)
	}
	if got := lastUserText(h.llm["luna"].requests[0]); !strings.HasSuffix(got, lookupNote) {
		t.Fatalf("not told to look things up: %q", got)
	}
}

// Without judge.brief nothing is asked before a reply.
func TestNoLookupWithoutBrief(t *testing.T) {
	t.Parallel()
	d := &decider{answer: finished()}
	p := waitingProfile("luna", 20*time.Millisecond, 2*time.Second)
	p.Tools = []string{llm.BuiltinWebSearch}
	h := newJudged(t, d, p)
	h.mustHandle(h.message("Alice", "luna which Unity version does VRChat use now"))
	for _, req := range d.requests {
		if _, ok := req.Questions[lookupID]; ok {
			t.Fatal("asked whether to look things up")
		}
	}
	if strings.Contains(lastUserText(h.llm["luna"].requests[0]), lookupNote) {
		t.Error("told to look things up")
	}
	if _, ok := lastReply(h)["lookup"]; ok {
		t.Error("the record says something was asked")
	}
}

// A failed request is no decision: the agent answers, told nothing, and the
// record says why.
func TestLookupFailureAnswersAnyway(t *testing.T) {
	t.Parallel()
	d := &decider{err: errors.New("decision: unavailable")}
	h := newJudged(t, d, lookingProfile("luna"))
	h.mustHandle(h.message("Alice", "luna which Unity version does VRChat use now"))
	if n := h.llm["luna"].count(); n != 1 {
		t.Fatalf("%d replies", n)
	}
	if strings.Contains(lastUserText(h.llm["luna"].requests[0]), lookupNote) {
		t.Error("told to look things up")
	}
	got, ok := lastReply(h)["lookup"].(*briefed)
	if !ok || got.Score != nil || !strings.Contains(got.Error, "unavailable") {
		t.Errorf("record %+v", lastReply(h)["lookup"])
	}
}

// judge.brief needs a tool to look things up with.
func TestBriefNeedsAWebTool(t *testing.T) {
	t.Parallel()
	p := lookingProfile("luna")
	p.Tools = nil
	_, err := New(Config{Profiles: []ProfileConfig{p}}, func(string) (llm.Provider, error) { return &provider{}, nil }, nil)
	var se *otogi.SettingError
	if !errors.As(err, &se) || se.Path != "profiles[0].judge.brief" {
		t.Fatalf("err %v, want a refusal at profiles[0].judge.brief", err)
	}
	p.Tools = []string{llm.BuiltinURLContext}
	if _, err := New(Config{Profiles: []ProfileConfig{p}}, func(string) (llm.Provider, error) { return &provider{}, nil }, nil); err != nil {
		t.Fatalf("url_context: %v", err)
	}
}

// For a waiting agent, whether to look things up is asked in the same
// request as whether the previous reply did for the messages that came
// during it.
func TestLookupIsAskedWithCovered(t *testing.T) {
	t.Parallel()
	d := &decider{answer: func(id string, req decision.Request) (float64, bool) {
		switch id {
		case coveredID:
			return 0.1, true
		case lookupID:
			return 0.9, true
		}
		return finished()(id, req)
	}}
	p := waitingProfile("luna", 20*time.Millisecond, 2*time.Second)
	p.Tools = []string{llm.BuiltinWebSearch}
	p.Judge.Brief = &BriefConfig{}
	h := newJudged(t, d, p)
	release := make(chan struct{})
	h.llm["luna"].before = func(n int) {
		if n == 1 {
			<-release
		}
	}
	first, second := h.message("Alice", "luna which Unity version"), h.message("Alice", "luna and which SDK")
	errc := make(chan error, 1)
	go func() { errc <- h.handle(first) }()
	eventually(t, "the first reply to start", func() bool { return h.llm["luna"].count() == 1 })
	errc2 := make(chan error, 1)
	go func() { errc2 <- h.handle(second) }()
	time.Sleep(100 * time.Millisecond)
	close(release)
	if err := errors.Join(<-errc, <-errc2); err != nil {
		t.Fatal(err)
	}
	var briefs [][]string
	for _, req := range d.requests {
		if _, ok := req.State.(briefState); ok {
			var ids []string
			for id := range req.Questions {
				ids = append(ids, id)
			}
			slices.Sort(ids)
			briefs = append(briefs, ids)
		}
	}
	if len(briefs) != 2 || !slices.Equal(briefs[0], []string{lookupID}) || !slices.Equal(briefs[1], []string{coveredID, lookupID}) {
		t.Fatalf("requests before replies %v, want lookup alone, then covered and lookup together", briefs)
	}
	for i, req := range h.llm["luna"].requests {
		if !strings.HasSuffix(lastUserText(req), lookupNote) {
			t.Errorf("reply %d not told to look things up", i)
		}
	}
}
