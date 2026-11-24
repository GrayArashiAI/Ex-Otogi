package chat

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/decision"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// waitingProfile is an agent that waits in groups between least and most, triggered
// by its name (no judgment of whether a message addresses it).
func waitingProfile(name string, least, most time.Duration) ProfileConfig {
	return ProfileConfig{Name: name, Keywords: []string{name}, Provider: name, Model: "m", Judge: &JudgeConfig{Wait: &WaitConfig{Min: otogi.Duration(least), Max: otogi.Duration(most)}}}
}

// finished answers done questions by the text of the message they ask
// about: unfinished for the texts listed, finished for the rest.
func finished(unfinished ...string) func(string, decision.Request) (float64, bool) {
	return func(id string, req decision.Request) (float64, bool) {
		if !strings.HasPrefix(id, "done.") {
			return 0, false
		}
		if slices.Contains(unfinished, asked(id, req)) {
			return 0.05, true
		}
		return 0.95, true
	}
}

func batchOf(t *testing.T, rec map[string]any) map[string]any {
	t.Helper()
	b, ok := rec["batch"].(map[string]any)
	if !ok {
		t.Fatalf("no batch in %+v", rec)
	}
	return b
}

func replies(h *harness) []map[string]any {
	h.host.mu.Lock()
	defer h.host.mu.Unlock()
	var out []map[string]any
	for _, r := range h.host.records {
		if r.Kind == "chat.reply" {
			out = append(out, r.Payload.(map[string]any))
		}
	}
	return out
}

// requests is how many times the provider was asked, safely.
func (p *provider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.requests)
}

// eventually polls until ok or a second passes.
func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(time.Second); !ok(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// A sender who has finished is answered after the least wait, with only
// whether they finished asked: the trigger left nothing else to judge.
func TestWaitAnswersOnceTheSenderHasFinished(t *testing.T) {
	t.Parallel()
	d := &decider{answer: finished()}
	h := newJudged(t, d, waitingProfile("luna", 50*time.Millisecond, 3*time.Second))
	start := time.Now()
	h.mustHandle(h.message("Alice", "luna what is 2+2"))
	if took := time.Since(start); took < 50*time.Millisecond || took > time.Second {
		t.Fatalf("answered after %v", took)
	}
	if len(d.requests) != 1 || len(d.requests[0].Questions) != 1 || d.requests[0].Questions["done.m1"].Instructions == "" {
		t.Fatalf("requests %+v", d.requests)
	}
	b := batchOf(t, lastReply(h))
	if !slices.Equal(b["messages"].([]string), []string{"m1"}) || b["waited_ms"].(int64) < 50 {
		t.Fatalf("batch %+v", b)
	}
}

// A sender who has not finished is waited for: what they send next joins the
// batch, its own Handle returning at once, and the agent answers both in one
// reply, told who said what. The second is finished, sent 2s after the
// first: the wait ends a little after min (by the 3s floor of the rhythm),
// well before max.
func TestWaitGathersWhatTheSenderSendsNext(t *testing.T) {
	t.Parallel()
	d := &decider{answer: finished("luna")}
	h := newJudged(t, d, waitingProfile("luna", 50*time.Millisecond, 3*time.Second))
	first := h.message("Alice", "luna")
	second := h.message("Alice", "luna how do I fix this error", func(e *otogi.Event) { e.OccurredAt = first.OccurredAt.Add(2 * time.Second) })
	errc := make(chan error, 1)
	start := time.Now()
	go func() { errc <- h.handle(first) }()
	time.Sleep(150 * time.Millisecond)
	if n := h.llm["luna"].count(); n != 0 {
		t.Fatalf("answered %d times before the sender finished", n)
	}
	h.mustHandle(second)
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took < 300*time.Millisecond || took > 1500*time.Millisecond {
		t.Fatalf("answered %v after the first message, want about 0.4s", took)
	}
	if n := h.llm["luna"].count(); n != 1 {
		t.Fatalf("%d replies, want 1", n)
	}
	text := lastUserText(h.llm["luna"].requests[0])
	if !strings.Contains(text, "Answer: Alice: luna / Alice: luna how do I fix this error") || !strings.Contains(text, "Alice: luna how do I fix this error") {
		t.Fatalf("user turn %q", text)
	}
	if b := batchOf(t, lastReply(h)); !slices.Equal(b["messages"].([]string), []string{"m1", "m2"}) {
		t.Fatalf("batch %+v", b)
	}
}

// However unfinished the sender seems, the wait ends max after the first
// message.
func TestWaitEndsAtMax(t *testing.T) {
	t.Parallel()
	d := &decider{answer: finished("luna, so")}
	h := newJudged(t, d, waitingProfile("luna", 20*time.Millisecond, 300*time.Millisecond))
	start := time.Now()
	h.mustHandle(h.message("Alice", "luna, so"))
	if took := time.Since(start); took < 300*time.Millisecond || took > 1500*time.Millisecond {
		t.Fatalf("answered after %v", took)
	}
}

// Messages arriving within the least wait of each other are judged in one
// request, each asked whether it addresses the agent and whether its sender
// has finished, and answered in one batch.
func TestMessagesArrivingTogetherAreJudgedTogether(t *testing.T) {
	t.Parallel()
	d := &decider{scores: map[string]float64{"a1": 0.9}, answer: finished()}
	luna := waitingProfile("luna", 150*time.Millisecond, 2*time.Second)
	luna.Judge.Wake = &WakeConfig{Threshold: 0.7}
	h := newJudged(t, d, luna)
	first, second := h.message("Alice", "what do you think"), h.message("Bob", "yes, tell us")
	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Go(func() { errs[0] = h.handle(first) })
	time.Sleep(30 * time.Millisecond)
	wg.Go(func() { errs[1] = h.handle(second) })
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
	if len(d.requests) != 1 {
		t.Fatalf("%d decision requests, want 1", len(d.requests))
	}
	state := d.requests[0].State.(judgeState)
	var ids []string
	for id := range d.requests[0].Questions {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	if !slices.Equal(state.Candidates, []string{"m1", "m2"}) || !slices.Equal(ids, []string{"done.m1", "done.m2", "wake.a1.m1", "wake.a1.m2"}) {
		t.Fatalf("candidates %v, questions %v", state.Candidates, ids)
	}
	if n := h.llm["luna"].count(); n != 1 {
		t.Fatalf("%d replies, want 1", n)
	}
	if b := batchOf(t, lastReply(h)); !slices.Equal(b["messages"].([]string), []string{"m1", "m2"}) {
		t.Fatalf("batch %+v", b)
	}
}

// A batch that falls due while a message that arrived before then is still
// being judged waits for it: the message joins and is answered with the rest,
// not on its own after.
func TestDueBatchWaitsForMessagesStillBeingJudged(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	d := &decider{scores: map[string]float64{"a1": 0.9}, answer: finished(), before: func(req decision.Request) {
		for id := range req.Questions {
			if strings.HasPrefix(id, "wake.") {
				<-release
				return
			}
		}
	}}
	luna := waitingProfile("luna", 50*time.Millisecond, 3*time.Second)
	luna.Judge.Wake, luna.Mention = &WakeConfig{Threshold: 0.7}, true
	h := newJudged(t, d, luna)
	first := h.message("Alice", "are you there", func(e *otogi.Event) { e.Mentioned = true })
	second := h.message("Bob", "and tell us about the weather")
	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Go(func() { errs[0] = h.handle(first) })
	time.Sleep(20 * time.Millisecond)
	wg.Go(func() { errs[1] = h.handle(second) })
	time.Sleep(300 * time.Millisecond) // the batch is long due
	if n := h.llm["luna"].count(); n != 0 {
		t.Fatalf("answered %d times while a message was being judged", n)
	}
	close(release)
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
	if n := h.llm["luna"].count(); n != 1 {
		t.Fatalf("%d replies, want 1", n)
	}
	if b := batchOf(t, lastReply(h)); !slices.Equal(b["messages"].([]string), []string{"m1", "m2"}) {
		t.Fatalf("batch %+v", b)
	}
}

// Messages to the agent while it replies wait in the next batch, opened by
// the first of them, until the reply ends. When the reply already did for
// them, they are not answered; otherwise they are.
func TestMessagesDuringAReplyWaitForIt(t *testing.T) {
	t.Parallel()
	for _, covered := range []bool{true, false} {
		score := 0.1
		if covered {
			score = 0.9
		}
		d := &decider{answer: func(id string, req decision.Request) (float64, bool) {
			if id == coveredID {
				return score, true
			}
			return finished()(id, req)
		}}
		h := newJudged(t, d, waitingProfile("luna", 20*time.Millisecond, 2*time.Second))
		release := make(chan struct{})
		h.llm["luna"].before = func(n int) {
			if n == 1 {
				<-release
			}
		}
		first, second := h.message("Alice", "luna what is 2+2"), h.message("Alice", "luna?? are you there")
		errc := make(chan error, 1)
		go func() { errc <- h.handle(first) }()
		eventually(t, "the first reply to start", func() bool { return h.llm["luna"].count() == 1 })
		errc2 := make(chan error, 1)
		go func() { errc2 <- h.handle(second) }()
		time.Sleep(100 * time.Millisecond)
		if n := h.llm["luna"].count(); n != 1 {
			t.Fatalf("covered %v: %d requests while the first reply runs", covered, n)
		}
		close(release)
		if err := errors.Join(<-errc, <-errc2); err != nil {
			t.Fatal(err)
		}
		recs := replies(h)
		if covered {
			last := recs[len(recs)-1]
			if h.llm["luna"].count() != 1 || last["skipped"] != "covered" || !slices.Equal(batchOf(t, last)["covered"].([]string), []string{"m2"}) {
				t.Fatalf("covered: %d requests, record %+v", h.llm["luna"].count(), last)
			}
		} else if h.llm["luna"].count() != 2 || len(recs) != 2 {
			t.Fatalf("not covered: %d requests, %d records", h.llm["luna"].count(), len(recs))
		}
		var briefs int
		for _, req := range d.requests {
			if s, ok := req.State.(briefState); ok {
				briefs++
				if s.Reply.Text != "hello" || !slices.Equal(s.Reply.During, []string{"m2"}) {
					t.Fatalf("covered %v: state %+v", covered, s)
				}
			}
		}
		if briefs != 1 {
			t.Fatalf("covered %v: %d covered requests, want 1", covered, briefs)
		}
	}
}

// A message that arrives while the agent replies came during the reply even
// when its judgment is back only after the reply ended: it is asked whether
// the reply already did for it. On the test group (2026-11-25), "ds
// 今天星期几" arrived while the answer to "Go 1.27有什么新东西" was written,
// was judged after it went out, and was answered on its own unasked.
func TestAMessageJudgedAfterTheReplyItArrivedDuring(t *testing.T) {
	t.Parallel()
	reached, judged := make(chan struct{}), make(chan struct{})
	d := &decider{answer: func(id string, req decision.Request) (float64, bool) {
		if id == coveredID {
			return 0.9, true
		}
		return finished()(id, req)
	}}
	d.before = func(req decision.Request) {
		for id := range req.Questions {
			if strings.HasPrefix(id, "done.") && asked(id, req) == "ds 今天星期几" {
				close(reached)
				<-judged
				return
			}
		}
	}
	h := newJudged(t, d, waitingProfile("ds", 20*time.Millisecond, 2*time.Second))
	replying := make(chan struct{})
	h.llm["ds"].before = func(n int) {
		if n == 1 {
			<-replying
		}
	}
	first, second := h.message("Alice", "ds Go 1.27有什么新东西"), h.message("Alice", "ds 今天星期几")
	errc := make(chan error, 2)
	go func() { errc <- h.handle(first) }()
	eventually(t, "the first reply to start", func() bool { return h.llm["ds"].count() == 1 })
	go func() { errc <- h.handle(second) }()
	<-reached // the second arrived during the reply; its judgment is held
	close(replying)
	if err := <-errc; err != nil { // the first's Handle, done with its reply
		t.Fatal(err)
	}
	close(judged)
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	last := lastReply(h)
	if n := h.llm["ds"].count(); n != 1 || last["skipped"] != "covered" || !slices.Equal(batchOf(t, last)["covered"].([]string), []string{"m2"}) {
		t.Fatalf("%d replies, record %+v", n, last)
	}
	var briefs int
	for _, req := range d.requests {
		if s, ok := req.State.(briefState); ok {
			briefs++
			if s.Reply.Text != "hello" || !slices.Equal(s.Reply.During, []string{"m2"}) {
				t.Fatalf("state %+v", s)
			}
		}
	}
	if briefs != 1 {
		t.Fatalf("%d covered requests, want 1", briefs)
	}
}

// A wait ends with its Handle: the message is not answered then, and handling
// it again (as after a restart) answers it once.
func TestWaitEndsWithItsHandle(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	calls := 0
	d := &decider{answer: func(id string, req decision.Request) (float64, bool) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return 0.05, true // not finished: the first Handle waits
		}
		return 0.95, true
	}}
	h := newJudged(t, d, waitingProfile("luna", 20*time.Millisecond, 5*time.Second))
	e := h.message("Alice", "luna")
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- h.m.Handle(ctx, e, invocation{h: h}) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	h.mustHandle(e)
	if n := h.llm["luna"].count(); n != 1 {
		t.Fatalf("%d replies, want 1", n)
	}
}

// panics runs f as the kernel runs a Handle, recovering a panic, and reports
// whether there was one.
func panics(f func()) (p bool) {
	defer func() { p = recover() != nil }()
	f()
	return false
}

// A reply that panics, which the kernel recovers from, still ends: the
// agent's next message is answered, not held behind it.
func TestAReplyThatPanicsEnds(t *testing.T) {
	t.Parallel()
	d := &decider{answer: finished()}
	h := newJudged(t, d, waitingProfile("luna", 20*time.Millisecond, 300*time.Millisecond))
	h.llm["luna"].before = func(n int) {
		if n == 1 {
			panic("the model blew up")
		}
	}
	if !panics(func() { _ = h.handle(h.message("Alice", "luna what is 2+2")) }) {
		t.Fatal("the reply did not panic")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.m.Handle(ctx, h.message("Alice", "luna are you there"), invocation{h: h}); err != nil {
		t.Fatal(err)
	}
	if n := h.llm["luna"].count(); n != 2 {
		t.Fatalf("%d model requests, want 2", n)
	}
}

// A judgment that panics leaves nothing waiting on it: the message judged
// with it in one request gets an error at once, and the chat's next message
// is judged and answered.
func TestAJudgmentThatPanicsEnds(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	calls := 0
	d := &decider{scores: map[string]float64{"a1": 0.9}, answer: finished(), before: func(decision.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			panic("the decision service blew up")
		}
	}}
	luna := waitingProfile("luna", 100*time.Millisecond, 500*time.Millisecond)
	luna.Judge.Wake = &WakeConfig{Threshold: 0.7}
	h := newJudged(t, d, luna)
	first, second := h.message("Alice", "what do you think"), h.message("Bob", "yes, tell us")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	var panicked bool
	var err error
	wg.Go(func() { panicked = panics(func() { _ = h.m.Handle(ctx, first, invocation{h: h}) }) })
	time.Sleep(20 * time.Millisecond)
	wg.Go(func() { err = h.m.Handle(ctx, second, invocation{h: h}) })
	wg.Wait()
	if !panicked || !errors.Is(err, errUnjudged) {
		t.Fatalf("panicked %v; the other message: %v", panicked, err)
	}
	if err := h.m.Handle(ctx, h.message("Alice", "what do you think now"), invocation{h: h}); err != nil {
		t.Fatal(err)
	}
	if n := h.llm["luna"].count(); n != 1 {
		t.Fatalf("%d replies, want 1", n)
	}
}

// Agents do not wait in private chats.
func TestNoWaitInPrivate(t *testing.T) {
	t.Parallel()
	d := &decider{answer: finished()}
	h := newJudged(t, d, waitingProfile("luna", time.Second, 3*time.Second))
	h.host.lists[[2]string{"luna", "telegram:dm:1"}] = "test"
	start := time.Now()
	h.mustHandle(h.message("Alice", "luna hi", func(e *otogi.Event) {
		e.Conversation = otogi.Conversation{ID: "dm:1", Kind: otogi.ConversationDirect}
	}))
	if took := time.Since(start); took > 500*time.Millisecond || len(d.requests) != 0 || h.llm["luna"].count() != 1 {
		t.Fatalf("took %v, %d decision requests, %d replies", took, len(d.requests), h.llm["luna"].count())
	}
	if _, ok := lastReply(h)["batch"]; ok {
		t.Fatal("a batch in a private chat")
	}
}

// wait.max may take at most half of timeout, which also bounds the reply,
// and min may not exceed it.
func TestWaitConfig(t *testing.T) {
	t.Parallel()
	providers := func(string) (llm.Provider, error) { return &provider{}, nil }
	for name, c := range map[string]struct {
		timeout, least, most time.Duration
		path, want           string
	}{
		"max past half of timeout": {time.Minute, time.Second, 40 * time.Second, "profiles[0].judge.wait.max", "at most half of timeout"},
		"min past max":             {0, 30 * time.Second, 10 * time.Second, "profiles[0].judge.wait.min", "is longer than max"},
		"negative":                 {0, -time.Second, 0, "profiles[0].judge.wait.min", "must not be negative"},
	} {
		p := waitingProfile("luna", c.least, c.most)
		_, err := New(Config{Timeout: otogi.Duration(c.timeout), Profiles: []ProfileConfig{p}}, providers)
		var se *otogi.SettingError
		if !errors.As(err, &se) || se.Path != c.path || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err %v, want %q at %s", name, err, c.want, c.path)
		}
	}
	if _, err := New(Config{Profiles: []ProfileConfig{waitingProfile("luna", 0, 0)}}, providers); err != nil {
		t.Fatalf("defaults: %v", err)
	}
}
