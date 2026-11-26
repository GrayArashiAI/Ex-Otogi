package chat

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/decision"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// chimeProfile is an agent that speaks up unasked, waiting 20ms to 400ms for
// senders to finish; with wake, messages addressed to it are judged too.
func chimeProfile(name string, wake float64) ProfileConfig {
	p := waitingProfile(name, 20*time.Millisecond, 400*time.Millisecond)
	p.Judge.Chime = &ChimeConfig{}
	if wake > 0 {
		p.Judge.Wake = &WakeConfig{Threshold: wake}
	}
	return p
}

// newChiming starts agents with a timeout of a second: a wait to speak up
// lasts half of it at most, which it reaches, as rhythms are at least 3s.
func newChiming(t *testing.T, d *decider, profiles ...ProfileConfig) *harness {
	t.Helper()
	return newHarnessConfig(t, map[string]any{decision.Service: d}, Config{Profiles: profiles, Timeout: otogi.Duration(time.Second)})
}

// chiming answers chime questions with chime and still questions with
// still, and has every sender finished.
func chiming(chime, still float64) func(string, decision.Request) (float64, bool) {
	return func(id string, req decision.Request) (float64, bool) {
		switch {
		case strings.HasPrefix(id, "chime."):
			return chime, true
		case strings.HasPrefix(id, "still."):
			return still, true
		}
		return finished()(id, req)
	}
}

// asks reports whether req has a question whose ID starts with prefix.
func asks(req decision.Request, prefix string) bool {
	for id := range req.Questions {
		if strings.HasPrefix(id, prefix) {
			return true
		}
	}
	return false
}

// waitingToChime reports whether the harness's first agent waits to speak up
// in the group.
func waitingToChime(h *harness) bool {
	_, waiting, _ := h.m.chimeOf(h.m.profiles[0], group)
	return waiting
}

// judge.chime waits for the chat to pause, so it needs judge.wait.
func TestChimeNeedsWait(t *testing.T) {
	t.Parallel()
	p := ProfileConfig{Name: "luna", Keywords: []string{"luna"}, Provider: "luna", Model: "m", Judge: &JudgeConfig{Chime: &ChimeConfig{}}}
	_, err := New(Config{Profiles: []ProfileConfig{p}}, func(string) (llm.Provider, error) { return &provider{}, nil }, nil)
	var se *otogi.SettingError
	if !errors.As(err, &se) || se.Path != "profiles[0].judge.chime" {
		t.Fatalf("err %v, want a refusal at profiles[0].judge.chime", err)
	}
	p = chimeProfile("luna", 0)
	if _, err := New(Config{Profiles: []ProfileConfig{p}}, func(string) (llm.Provider, error) { return &provider{}, nil }, nil); err != nil {
		t.Fatalf("with judge.wait: %v", err)
	}
}

// An agent is asked whether it would like to chime in only while it is out
// of the conversation.
func TestChimeAskedOnlyOutOfTheConversation(t *testing.T) {
	t.Parallel()
	d := &decider{answer: chiming(0.1, 0)}
	h := newChiming(t, d, chimeProfile("luna", 0))
	h.mustHandle(h.message("Alice", "anyone tried the new Unity?"))
	if len(d.requests) != 1 || !asks(d.requests[0], "chime.a1.") {
		t.Fatalf("requests %+v", d.requests)
	}
	h.replayAgent(t, "luna", "decision", "I did", 1)
	h.mustHandle(h.message("Bob", "how was it"))
	if len(d.requests) != 1 {
		t.Fatalf("asked to chime in while in the conversation: %+v", d.requests[1].Questions)
	}
}

// Past the threshold, the agent waits for the chat to pause, at most half
// of timeout here, and speaks up told why.
func TestChimeWaitsForAPause(t *testing.T) {
	t.Parallel()
	h := newChiming(t, &decider{answer: chiming(0.9, 0)}, chimeProfile("luna", 0))
	start := time.Now()
	h.mustHandle(h.message("Alice", "anyone tried the new Unity?"))
	if took := time.Since(start); took < 450*time.Millisecond || took > time.Second {
		t.Fatalf("spoke up after %v, want the half second timeout allows", took)
	}
	reqs := h.llm["luna"].requests
	if len(reqs) != 1 || !strings.HasSuffix(lastUserText(reqs[0]), chimeNote) {
		t.Fatalf("requests %d, last text %q", len(reqs), lastUserText(reqs[0]))
	}
	rec := lastReply(h)
	j, _ := rec["judged"].(*judgment)
	if rec["trigger"] != "chime" || j == nil || j.Score != 0.9 || j.Threshold != 0.55 || !slices.Equal(batchOf(t, rec)["messages"].([]string), []string{"m1"}) {
		t.Fatalf("record %+v, judged %+v", rec, j)
	}
	if cause := turnCause(t, h, "agent-1"); cause != "chime" {
		t.Fatalf("the reply's turn says %q, want chime", cause)
	}
}

// A message that comes during the wait asks whether the agent still means
// to speak up: below judge.chime.still (0.5 unset), it gives up; at or
// above, it speaks up.
func TestChimeStill(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		setting, still float64
		spoke          bool
	}{{0, 0.3, false}, {0, 0.8, true}, {0, 0.45, false}, {0.4, 0.45, true}, {0.4, 0.3, false}} {
		d := &decider{answer: chiming(0.9, c.still)}
		pc := chimeProfile("luna", 0)
		pc.Judge.Chime.Still = c.setting
		h := newChiming(t, d, pc)
		first := h.message("Alice", "anyone tried the new Unity?")
		errc := make(chan error, 1)
		go func() { errc <- h.handle(first) }()
		eventually(t, "the wait to start", func() bool { return waitingToChime(h) })
		h.mustHandle(h.message("Bob", "anyway, dinner?"))
		if err := <-errc; err != nil {
			t.Fatal(err)
		}
		if req := d.requests[len(d.requests)-1]; !asks(req, "still.a1") || asks(req, "chime.") {
			t.Fatalf("still %v of %v: the second message asked %+v", c.still, c.setting, req.Questions)
		}
		if n := h.llm["luna"].count(); (n == 1) != c.spoke {
			t.Fatalf("still %v of %v: %d replies", c.still, c.setting, n)
		}
		r := h.host.records[len(h.host.records)-1]
		p, _ := r.Payload.(map[string]any)
		if c.spoke {
			if r.Kind != "chat.reply" || r.Outcome != "ok" || p["trigger"] != "chime" {
				t.Fatalf("still %v of %v: record %s %s %+v", c.still, c.setting, r.Kind, r.Outcome, p)
			}
			continue
		}
		if r.Kind != "chat.reply" || r.Outcome != "skipped" || p["skipped"] != "still" || batchOf(t, p)["still"] != c.still {
			t.Fatalf("still %v of %v: record %s %s %+v", c.still, c.setting, r.Kind, r.Outcome, p)
		}
		if waitingToChime(h) {
			t.Fatalf("still %v of %v: still waiting after giving up", c.still, c.setting)
		}
	}
}

// Called during the wait, the agent answers both messages as called, once
// the caller has finished, without the chime's note.
func TestChimeCalledDuringTheWait(t *testing.T) {
	t.Parallel()
	d := &decider{answer: func(id string, req decision.Request) (float64, bool) {
		if strings.HasPrefix(id, "wake.") {
			if strings.Contains(asked(id, req), "what do you think") {
				return 0.9, true
			}
			return 0.1, true
		}
		return chiming(0.9, 0.8)(id, req)
	}}
	// wait.max of 100ms: called, the agent answers well before the half
	// second the chime would wait.
	p := chimeProfile("luna", 0.7)
	p.Judge.Wait.Max = otogi.Duration(100 * time.Millisecond)
	h := newChiming(t, d, p)
	start := time.Now()
	first := h.message("Alice", "anyone tried the new Unity?")
	errc := make(chan error, 1)
	go func() { errc <- h.handle(first) }()
	eventually(t, "the wait to start", func() bool { return waitingToChime(h) })
	h.mustHandle(h.message("Bob", "luna what do you think"))
	if waitingToChime(h) {
		t.Fatal("still waiting to speak up after the call: a message that moves on would drop the batch")
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 350*time.Millisecond {
		t.Fatalf("answered after %v: the call should end the wait for a pause", took)
	}
	reqs := h.llm["luna"].requests
	if len(reqs) != 1 || strings.Contains(lastUserText(reqs[0]), chimeNote) {
		t.Fatalf("requests %d, last text %q", len(reqs), lastUserText(reqs[0]))
	}
	rec := lastReply(h)
	if rec["trigger"] != "decision" || !slices.Equal(batchOf(t, rec)["messages"].([]string), []string{"m1", "m2"}) {
		t.Fatalf("record %+v", rec)
	}
	if cause := turnCause(t, h, "agent-1"); cause != "decision" {
		t.Fatalf("the reply's turn says %q, want decision", cause)
	}
}

// The pause is 2 to 6 rhythms for scores from 1 to 0, the surer the chime
// the shorter: 2 to 3.8 past the default threshold of 0.55.
func TestChimeHold(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		score float64
		r     time.Duration
		want  time.Duration
	}{
		{1, 3 * time.Second, 6 * time.Second},
		{0.55, 10 * time.Second, 38 * time.Second},
		{0, time.Second, 6 * time.Second},
		{1.2, time.Second, 2 * time.Second},
	} {
		if got := chimeHold(c.score, c.r); math.Abs(float64(got-c.want)) > float64(time.Microsecond) {
			t.Errorf("chimeHold(%v, %v) = %v, want %v", c.score, c.r, got, c.want)
		}
	}
}

// A message the agent still means to speak up after moves the pause to
// after it, by its sender's rhythm, up to the longest wait; a failed
// judgment changes nothing; the agent gives up once the message it meant to
// add to has left the recent chat.
func TestHeardMovesThePause(t *testing.T) {
	t.Parallel()
	h := newChiming(t, &decider{}, chimeProfile("luna", 0))
	p := h.m.profiles[0]
	t0 := time.Now()
	b := &batch{key: p.Name + "\x00" + group, p: p, entries: []entry{{e: h.message("Alice", "x"), at: t0}}, first: t0, deadline: t0.Add(time.Second),
		changed: make(chan struct{}, 1), chime: true, score: 0.9, latest: t0.Add(time.Minute)}
	h.m.mu.Lock()
	h.m.waits[b.key] = &waiting{open: b}
	h.m.mu.Unlock()
	deadline := func() time.Time {
		h.m.mu.Lock()
		defer h.m.mu.Unlock()
		return b.deadline
	}
	h.m.heard(p, group, t0.Add(5*time.Second), stillAnswer{score: 0.8, known: true}, 2*time.Second)
	if want := t0.Add(5*time.Second + chimeHold(0.9, 2*time.Second)); !deadline().Equal(want) {
		t.Fatalf("deadline %v after t0, want %v", deadline().Sub(t0), want.Sub(t0))
	}
	before := deadline()
	h.m.heard(p, group, t0.Add(9*time.Second), stillAnswer{}, time.Second)
	if !deadline().Equal(before) {
		t.Fatal("a failed judgment moved the pause")
	}
	h.m.heard(p, group, t0.Add(59*time.Second), stillAnswer{score: 0.8, known: true}, 10*time.Second)
	if !deadline().Equal(b.latest) {
		t.Fatalf("deadline %v after t0, want the longest wait", deadline().Sub(t0))
	}
	h.m.heard(p, group, t0.Add(60*time.Second), stillAnswer{gone: true}, time.Second)
	h.m.mu.Lock()
	gaveUp, still := b.gaveUp, b.still
	h.m.mu.Unlock()
	if !gaveUp || still != nil {
		t.Fatalf("gave up %v, still %v: want given up with no score, the message gone from the recent chat", gaveUp, still)
	}
	// Its record has no still: no judgment gave a score.
	if err := h.m.giveUp(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	if rec := lastReply(h); rec["skipped"] != "still" || batchOf(t, rec)["still"] != nil {
		t.Fatalf("record %+v", rec)
	}
}

// A call that comes just as a message judged with it has the agent give up
// is answered: the batch becomes one the agent was called for.
func TestChimeCalledAsItGivesUp(t *testing.T) {
	t.Parallel()
	h := newChiming(t, &decider{answer: chiming(0.9, 0.2)}, chimeProfile("luna", 0.7))
	p := h.m.profiles[0]
	now := time.Now()
	b := h.m.enter(p, entry{e: h.message("Alice", "anyone tried the new Unity?"), at: now, trigger: "chime", j: &judgment{Score: 0.9, Threshold: 0.55}}, 1, true, 3*time.Second, false)
	if b == nil {
		t.Fatal("the chime opened no batch")
	}
	h.m.heard(p, group, now, stillAnswer{score: 0.2, known: true}, 3*time.Second)
	if joined := h.m.enter(p, entry{e: h.message("Bob", "luna what do you think"), at: now, trigger: "decision", j: &judgment{Score: 0.9, Threshold: 0.7}}, 1, true, 3*time.Second, false); joined != nil {
		t.Fatal("the call opened a batch of its own")
	}
	if err := h.m.run(t.Context(), invocation{h: h}, b); err != nil {
		t.Fatal(err)
	}
	rec := lastReply(h)
	if n := h.llm["luna"].count(); n != 1 || rec["skipped"] != nil || rec["trigger"] != "decision" || !slices.Equal(batchOf(t, rec)["messages"].([]string), []string{"m1", "m2"}) {
		t.Fatalf("%d replies, record %+v", n, rec)
	}
}

// Chimes count in the agent's share, and one the share alone holds back is
// recorded.
func TestChimeHeldByTheShare(t *testing.T) {
	t.Parallel()
	d := &decider{answer: chiming(0.6, 0)}
	h := newChiming(t, d, chimeProfile("luna", 0))
	for i := 1; i <= 4; i++ {
		h.replayAgent(t, "luna", "chime", fmt.Sprint(i), i)
	}
	// Ten lines after them, luna is out of the conversation; each of the
	// five members has two.
	for _, who := range []string{"Alice", "Bob", "Carol", "Dave", "Erin"} {
		for i := 1; i <= 2; i++ {
			h.mustHandle(h.message(who, fmt.Sprintf("%s %d", who, i)))
		}
	}
	if n := h.llm["luna"].count(); n != 0 {
		t.Fatalf("%d replies before the candidate", n)
	}
	// luna 4 chimes, the middle member 2: share 2, threshold
	// 0.55 + 0.1 × 0.7 = 0.62, past the 0.6 of this one.
	held := h.message("Alice", "Alice 3")
	h.mustHandle(held)
	if n := h.llm["luna"].count(); n != 0 || waitingToChime(h) {
		t.Fatalf("%d replies, waiting %v", n, waitingToChime(h))
	}
	r := h.host.records[len(h.host.records)-1]
	if p, _ := r.Payload.(map[string]any); r.Kind != "chat.held" || r.Event != held.ID || p["trigger"] != "chime" || p["score"] != 0.6 || p["base"] != 0.55 ||
		math.Abs(p["threshold"].(float64)-0.62) > 1e-9 || p["share"] != 2.0 {
		t.Fatalf("held record %s %+v", r.Kind, r.Payload)
	}
}
