package chat

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/pkg/decision"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	mediasvc "github.com/TundraWork/Ex-Otogi/pkg/media"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// staleProfile is an agent that checks a group reply before it goes out.
func staleProfile(name string) ProfileConfig {
	return ProfileConfig{Name: name, Label: strings.ToUpper(name[:1]) + name[1:], Keywords: []string{name}, Provider: name, Model: "m", Judge: &JudgeConfig{Stale: &StaleConfig{}}}
}

// staleAnswers answers the check's two questions with retracted and
// answered, and every other question with 0.95: a question the check should
// not ask would then hold the reply back, and a covered asked after it
// would leave the next batch out.
func staleAnswers(retracted, answered float64) func(string, decision.Request) (float64, bool) {
	return func(id string, _ decision.Request) (float64, bool) {
		switch id {
		case retractedID:
			return retracted, true
		case answeredID:
			return answered, true
		}
		return 0.95, true
	}
}

// whileReplying has agent's reply to first start, then makes and handles
// each of others while the model writes it, then lets the reply finish.
// None of others may wait for the reply.
func whileReplying(t *testing.T, h *harness, agent string, first otogi.Event, others ...func() otogi.Event) {
	t.Helper()
	release, before := make(chan struct{}), h.llm[agent].count()
	h.llm[agent].before = func(n int) {
		if n == before+1 {
			<-release
		}
	}
	errc := make(chan error, 1)
	go func() { errc <- h.handle(first) }()
	eventually(t, "the reply to start", func() bool { return h.llm[agent].count() == before+1 })
	for _, other := range others {
		h.mustHandle(other())
	}
	close(release)
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

// staleRequests are the requests of the check before a reply goes out.
func staleRequests(d *decider) []decision.Request {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []decision.Request
	for _, req := range d.requests {
		if s, ok := req.State.(briefState); ok && s.Draft != nil {
			out = append(out, req)
		}
	}
	return out
}

// shown is what the harness's reply streams showed, in order.
func shown(h *harness) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, s := range h.streams {
		if text := s.final.PlainText(); text != "" {
			out = append(out, text)
		}
	}
	return out
}

// With nobody else speaking after the reply started, nothing is asked: not
// for the account's own messages, nor for one as old as the message
// answered.
func TestStaleNotAskedWithoutNewMessages(t *testing.T) {
	t.Parallel()
	d := &decider{answer: staleAnswers(0.95, 0.95)}
	h := newJudged(t, d, staleProfile("luna"))
	h.mustHandle(h.message("Alice", "luna what is 2+2"))
	first := h.message("Alice", "luna what is 3+3")
	whileReplying(t, h, "luna", first,
		func() otogi.Event {
			return h.message("Me", "a card", func(e *otogi.Event) { e.Direction = otogi.DirectionOutgoing })
		},
		func() otogi.Event {
			return h.message("Bob", "same second", func(e *otogi.Event) { e.OccurredAt = first.OccurredAt })
		})
	if n := len(staleRequests(d)); n != 0 || len(shown(h)) != 2 {
		t.Fatalf("%d checks, shown %q", n, shown(h))
	}
}

// Someone else speaking while the reply is written has the reply checked
// before it goes out: it goes out unless whoever asked took it back or
// someone else answered it, or the check failed.
func TestStaleCheck(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name                string
		retracted, answered float64
		err                 error
		dropped             bool
	}{
		{name: "still to go out", retracted: 0.1, answered: 0.2},
		{name: "taken back", retracted: 0.9, answered: 0.2, dropped: true},
		{name: "answered by someone else", retracted: 0.1, answered: 0.8, dropped: true},
		{name: "check failed", err: errors.New("jev is down")},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d := &decider{answer: staleAnswers(c.retracted, c.answered), err: c.err}
			h := newJudged(t, d, staleProfile("luna"))
			whileReplying(t, h, "luna", h.message("Alice", "luna what is 2+2"), func() otogi.Event { return h.message("Bob", "it's 4, Alice") })

			reqs := staleRequests(d)
			if len(reqs) != 1 {
				t.Fatalf("%d checks", len(reqs))
			}
			s := reqs[0].State.(briefState)
			if got := slices.Sorted(maps.Keys(reqs[0].Questions)); !slices.Equal(got, []string{answeredID, retractedID}) ||
				!slices.Equal(s.Batch, []string{"m1"}) || s.Draft.Text != "hello" || !slices.Equal(s.Draft.During, []string{"m2"}) || s.Reply != nil {
				t.Fatalf("questions %v, state %+v", got, s)
			}
			rec := lastReply(h)
			check, _ := rec["stale"].(*staleCheck)
			if check == nil || !slices.Equal(check.Messages, []string{"m2"}) {
				t.Fatalf("record %+v", rec)
			}
			if c.dropped {
				if len(shown(h)) != 0 || rec["skipped"] != "stale" || check.Draft != "hello" || len(rec["turns"].([]int64)) != 0 {
					t.Fatalf("shown %q, record %+v, check %+v", shown(h), rec, check)
				}
				return
			}
			if got := shown(h); len(got) != 1 || !strings.HasSuffix(got[0], "hello") || rec["skipped"] != nil || check.Draft != "" {
				t.Fatalf("shown %q, record %+v", got, rec)
			}
			if c.err != nil && check.Error == "" {
				t.Fatalf("no error in %+v", check)
			}
		})
	}
}

// Adding to a question is no taking it back: the reply still goes out,
// though the addition did not call the agent. The check asks only its two
// questions: any other would hold the reply back here.
func TestStaleKeepsASupplementedQuestion(t *testing.T) {
	t.Parallel()
	d := &decider{answer: staleAnswers(0.1, 0.1)}
	h := newJudged(t, d, staleProfile("luna"))
	whileReplying(t, h, "luna", h.message("Alice", "luna what should I cook tonight"), func() otogi.Event { return h.message("Alice", "it's for my kid, she is five") })
	if len(staleRequests(d)) != 1 || len(shown(h)) != 1 {
		t.Fatalf("%d checks, shown %q", len(staleRequests(d)), shown(h))
	}
}

// The check shows the chat as every judgment does: the agent's own lines
// without the label its reply showed, and the operators marked.
func TestStaleShowsTheChatAsJudgmentsDo(t *testing.T) {
	t.Parallel()
	d := &decider{answer: staleAnswers(0.1, 0.1)}
	h := newJudged(t, d, staleProfile("luna"))
	h.m.operators = func() []string { return []string{"telegram:user:Alice"} }
	h.mustHandle(h.message("Alice", "luna hi"))
	h.echo(h.streams[0])
	whileReplying(t, h, "luna", h.message("Alice", "luna what is 2+2"), func() otogi.Event { return h.message("Bob", "4") })
	s := staleRequests(d)[0].State.(briefState)
	var agent, alice bool
	for _, l := range s.History {
		agent = agent || strings.HasSuffix(l.From, "Luna") && l.Text == "hello"
		alice = alice || strings.Contains(l.From, "Alice ⟦operator⟧")
	}
	if !agent || !alice {
		t.Fatalf("history %+v", s.History)
	}
}

// A reply checked before it goes out does not stream in a group, so nothing
// shows before the check; in a private chat it streams, and is not checked.
func TestStaleHoldsTheStreamInGroups(t *testing.T) {
	t.Parallel()
	d := &decider{answer: staleAnswers(0.1, 0.1)}
	h := newJudged(t, d, staleProfile("luna"))
	h.host.lists[[2]string{"luna", "telegram:user:7"}] = "test"
	h.mustHandle(h.message("Alice", "luna hi"))
	private := func(e *otogi.Event) {
		e.Conversation = otogi.Conversation{ID: "user:7", Kind: otogi.ConversationDirect}
	}
	h.mustHandle(h.message("Alice", "luna hi", private))
	if len(h.streams) != 2 || len(h.streams[0].updates) != 0 || h.streams[0].final.PlainText() == "" || len(h.streams[1].updates) == 0 {
		t.Fatalf("streams %d, group updates %q, private updates %q", len(h.streams), h.streams[0].updates, h.streams[1].updates)
	}
	if n := len(staleRequests(d)); n != 0 {
		t.Fatalf("%d checks", n)
	}
}

// A reply that is held back where the model first says something before its
// tools ends there: the words do not go out, and the tools do not run.
func TestStaleBeforeTools(t *testing.T) {
	t.Parallel()
	ran := false
	shout := llm.NewTool(llm.Function{Name: "shout", Description: "shouts"}, func(context.Context, otogi.Event, json.RawMessage) (any, error) {
		ran = true
		return "HI", nil
	})
	storage, err := modulestore.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	d := &decider{answer: staleAnswers(0.9, 0.1)}
	h := &harness{t: t, host: &host{storage: storage, services: map[string]any{"llm.tools/fun": []llm.Tool{shout}, decision.Service: d}, lists: map[[2]string]string{{"luna", group}: "test", {"tools:fun", group}: "test"}}, llm: map[string]*provider{}, now: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)}
	call := llm.Response{Turn: llm.Turn{Role: llm.RoleModel, Content: otogi.TextMessage("Let me shout."), Calls: []llm.Call{{ID: "c1", Name: "shout", Args: json.RawMessage(`"hi"`)}}}, Finish: llm.FinishToolCalls}
	p := &provider{responses: []llm.Response{call, answer("Shouted.")}}
	h.llm["luna"] = p
	profile := staleProfile("luna")
	if h.m, err = New(Config{Tools: []string{"fun"}, Profiles: []ProfileConfig{profile}}, func(string) (llm.Provider, error) { return p, nil }, nil); err != nil {
		t.Fatal(err)
	}
	h.m.now = h.clock
	if err := h.m.Start(context.Background(), h.host); err != nil {
		t.Fatal(err)
	}
	whileReplying(t, h, "luna", h.message("Alice", "luna shout hi"), func() otogi.Event { return h.message("Alice", "never mind") })
	rec := lastReply(h)
	if len(shown(h)) != 0 || ran || p.count() != 1 || rec["skipped"] != "stale" || rec["stale"].(*staleCheck).Draft != "Let me shout." {
		t.Fatalf("shown %q, tool ran %v, %d requests, record %+v", shown(h), ran, p.count(), rec)
	}
}

// A sticker held back does not go out either.
func TestStaleHoldsTheSticker(t *testing.T) {
	t.Parallel()
	d := &decider{answer: staleAnswers(0.1, 0.9)}
	box := newStickerBox()
	profile := stickerProfile()
	profile.Judge = &JudgeConfig{Stale: &StaleConfig{}}
	h := newHarnessWith(t, map[string]any{mediasvc.Service: newDescriber(), mediasvc.StickersService: box, decision.Service: d}, profile)
	h.llm["otogi"].responses = []llm.Response{picks("", 3)}
	whileReplying(t, h, "otogi", h.message("Alice", "otogi show me a dog"), func() otogi.Event { return h.message("Bob", "here's a dog") })
	if len(shown(h)) != 0 || len(h.sends) != 0 || lastReply(h)["skipped"] != "stale" {
		t.Fatalf("shown %q, sent %d, record %+v", shown(h), len(h.sends), lastReply(h))
	}
}

// When a reply is held back, the messages that came during it are answered
// as the next batch, without asking whether the reply did for them: none
// went out.
func TestStaleLeavesTheNextBatchAnswered(t *testing.T) {
	t.Parallel()
	d := &decider{answer: staleAnswers(0.9, 0.1)}
	profile := waitingProfile("luna", 20*time.Millisecond, 2*time.Second)
	profile.Judge.Stale = &StaleConfig{}
	h := newJudged(t, d, profile)
	release := make(chan struct{})
	h.llm["luna"].before = func(n int) {
		if n == 1 {
			<-release
		}
	}
	errc := make(chan error, 2)
	go func() { errc <- h.handle(h.message("Alice", "luna what is 2+2")) }()
	eventually(t, "the first reply to start", func() bool { return h.llm["luna"].count() == 1 })
	go func() { errc <- h.handle(h.message("Alice", "luna never mind, what is 3+3")) }()
	time.Sleep(100 * time.Millisecond)
	close(release)
	if err := errors.Join(<-errc, <-errc); err != nil {
		t.Fatal(err)
	}
	recs := replies(h)
	if h.llm["luna"].count() != 2 || len(recs) != 2 || recs[0]["skipped"] != "stale" || recs[1]["skipped"] != nil || len(shown(h)) != 1 {
		t.Fatalf("%d requests, records %+v, shown %q", h.llm["luna"].count(), recs, shown(h))
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, req := range d.requests {
		if s, ok := req.State.(briefState); ok && s.Reply != nil {
			t.Fatalf("covered asked: %+v", s)
		}
	}
}

// judge.stale has the module require the decision service, with its
// threshold by default 0.8.
func TestStaleSettings(t *testing.T) {
	t.Parallel()
	h := newJudged(t, &decider{}, staleProfile("luna"))
	if !slices.Contains(h.m.Descriptor().Requires, decision.Service) || h.m.profiles[0].stale != defaultStale || defaultStale != 0.8 {
		t.Fatalf("requires %v, threshold %v", h.m.Descriptor().Requires, h.m.profiles[0].stale)
	}
}
