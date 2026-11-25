package chat

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/gemini"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/openai"
	decisionmodule "github.com/TundraWork/Ex-Otogi/modules/decision"
	"github.com/TundraWork/Ex-Otogi/pkg/decision"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// A timed replay sends a chat's messages at the times they were sent, to an
// agent that waits, follows up and speaks up unasked as it would live. It
// runs on virtual time (testing/synctest): in the bubble, time.Now, timers
// and sleeps read a clock that jumps ahead whenever every goroutine there
// waits, so the module's waits take no real time and a half-hour chat takes
// as long as its requests. With OTOGI_CHAT_REPLAY_REALTIME=1 it runs on the
// real clock instead, the same way otherwise, to compare the two.

// offBubble runs requests to real services outside the synctest bubble: a
// goroutine blocked on the network does not count as waiting, so an idle
// connection in the bubble would keep its clock from ever moving. Nil runs
// them where they are called, for a replay on the real clock.
type offBubble struct{ calls chan func() }

// newOffBubble starts the goroutine that runs requests; it is to be called
// outside the bubble, and stops when the test ends.
func newOffBubble(t *testing.T) *offBubble {
	o := &offBubble{calls: make(chan func())}
	go func() {
		for f := range o.calls {
			go f()
		}
	}()
	t.Cleanup(func() { close(o.calls) })
	return o
}

// do runs f, a request, under ctx's deadline. Outside the bubble, f gets a
// context with what is left of that deadline on the real clock, without
// ctx's cancellation, whose channels belong to the bubble. Meanwhile the
// bubble's clock stands still, as a goroutine waiting on a mutex is not
// durably blocked. Then the clock moves on by the time f took; ctx ending
// first ends do with its error, as it would have ended the request.
func (o *offBubble) do(ctx context.Context, f func(context.Context)) error {
	if o == nil {
		f(ctx)
		return nil
	}
	left, limited := time.Duration(0), false
	if deadline, ok := ctx.Deadline(); ok {
		left, limited = time.Until(deadline), true
	}
	var took time.Duration
	var done sync.Mutex
	done.Lock()
	o.calls <- func() {
		defer done.Unlock()
		c, cancel := context.WithoutCancel(ctx), context.CancelFunc(func() {})
		if limited {
			c, cancel = context.WithTimeout(c, left)
		}
		defer cancel()
		start := time.Now()
		f(c)
		took = time.Since(start)
	}
	done.Lock()
	timer := time.NewTimer(took)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// offDecider is a decision service whose requests run off the bubble.
type offDecider struct {
	o *offBubble
	d decision.Decider
}

func (w offDecider) Decide(ctx context.Context, req decision.Request) (decision.Answers, error) {
	var a decision.Answers
	var err error
	if late := w.o.do(ctx, func(ctx context.Context) { a, err = w.d.Decide(ctx, req) }); late != nil {
		return nil, late
	}
	return a, err
}

// offProvider is a model whose requests run off the bubble. Its answer is
// shown at once when it is in, not streamed: a replay is about timing.
type offProvider struct {
	o *offBubble
	p llm.Provider
}

func (w offProvider) Generate(ctx context.Context, req llm.Request, partial func(otogi.Message) error) (llm.Response, error) {
	var resp llm.Response
	var err error
	if late := w.o.do(ctx, func(ctx context.Context) { resp, err = w.p.Generate(ctx, req, nil) }); late != nil {
		return llm.Response{}, late
	}
	if err == nil && partial != nil && len(resp.Turn.Content.Parts) > 0 {
		if err := partial(resp.Turn.Content); err != nil {
			return llm.Response{}, err
		}
	}
	return resp, err
}

// timedRecord is a record of a timed replay, and when it was written, from
// the first message replayed.
type timedRecord struct {
	at time.Duration
	r  otogi.Record
}

// echoing hands each reply back to the module as the platform does: the
// account's message, as it was sent, when it was sent.
type echoing struct {
	invocation
}

func (i echoing) Stream(ctx context.Context, slot string, to otogi.Destination) otogi.MessageStream {
	return echoStream{i.invocation.Stream(ctx, slot, to).(*stream), i}
}

type echoStream struct {
	*stream
	inv echoing
}

func (s echoStream) Close(ctx context.Context, msg otogi.Message) ([]otogi.EventRef, error) {
	refs, err := s.stream.Close(ctx, msg)
	if err != nil {
		return refs, err
	}
	h := s.h
	e := otogi.Event{
		ID: otogi.EventID("echo-" + string(s.ref.SourceEvent)), Platform: "telegram", Account: "main",
		Conversation: otogi.Conversation{ID: "chat:1", Kind: otogi.ConversationGroup, Title: "Friends"},
		SourceID:     s.ref.SourceEvent, Actor: otogi.Actor{ID: "user:Me", Name: "Me"},
		Action: otogi.ActionMessageCreated, Direction: otogi.DirectionOutgoing, OccurredAt: h.m.now(), Message: msg,
	}
	return refs, h.m.Handle(ctx, e, s.inv)
}

// replayTimed replays lines to the agents of c, which ask decider and
// provider, and returns the records written. Lines marked history are in
// the transcript before it starts; the others are sent at their times, each
// handled on its own as the kernel does, and the agent's own among them are
// left out, as the agent replayed speaks for itself. The module reads the
// time as the chat's, from the first line replayed on: the date the model
// is told and the times in the transcript are the chat's.
func replayTimed(t *testing.T, realtime bool, lines []replayLine, c Config, decider decision.Decider, provider llm.Provider) []timedRecord {
	t.Helper()
	for i, l := range lines {
		if l.At == 0 {
			t.Fatalf("line %d has no time", i+1)
		}
	}
	first := slices.IndexFunc(lines, func(l replayLine) bool { return !l.History })
	if first < 0 {
		t.Fatal("nothing to replay")
	}
	var o *offBubble
	if !realtime {
		o = newOffBubble(t)
	}
	var out []timedRecord
	run := func(t *testing.T) {
		h := newHarnessConfig(t, map[string]any{decision.Service: offDecider{o, decider}}, c)
		for _, p := range h.m.profiles {
			p.provider = offProvider{o, provider}
		}
		base, start := time.UnixMilli(lines[first].At), time.Now()
		h.m.now = func() time.Time { return base.Add(time.Since(start)) }
		h.host.recorded = func(r otogi.Record) {
			at := h.m.now().Sub(base)
			out = append(out, timedRecord{at: at, r: r})
			t.Logf("%s %s", offset(at), describeRecord(h, r))
		}
		ctx := context.Background()
		for i, l := range lines[:first] {
			h.historyLine(t, c.Profiles[0].Name, l, i)
		}
		inv := echoing{invocation{h: h}}
		timeout := h.m.Descriptor().Timeout
		var wg sync.WaitGroup
		for _, l := range lines[first:] {
			if l.Agent {
				continue
			}
			time.Sleep(time.UnixMilli(l.At).Sub(h.m.now()))
			e := h.message(l.From, l.Text, func(e *otogi.Event) { e.OccurredAt = h.m.now() })
			t.Logf("%s %s: %s", offset(e.OccurredAt.Sub(base)), l.From, oneLine(l.Text))
			wg.Go(func() {
				ctx, cancel := context.WithTimeout(ctx, timeout)
				defer cancel()
				if err := h.m.Handle(ctx, e, inv); err != nil {
					t.Logf("%s handling %s: %v", offset(h.m.now().Sub(base)), e.SourceID, err)
				}
			})
		}
		wg.Wait()
	}
	if realtime {
		run(t)
	} else {
		synctest.Test(t, run)
	}
	return out
}

// historyLine puts l in the transcript at its time, without handling it:
// the agent's own lines as replies it was called for.
func (h *harness) historyLine(t *testing.T, agent string, l replayLine, i int) {
	t.Helper()
	ctx, at := context.Background(), time.UnixMilli(l.At)
	to := otogi.Destination{Platform: "telegram", Account: "main", Conversation: "chat:1"}
	e := h.message(l.From, l.Text, func(e *otogi.Event) { e.OccurredAt, e.SourceID = at, otogi.SourceEventID(fmt.Sprintf("history-%d", i)) })
	if l.Agent {
		e.Direction = otogi.DirectionOutgoing
		turn := []storedTurn{{Turn: llm.Turn{Role: llm.RoleUser, Content: otogi.TextMessage("(replayed)")}}}
		if _, err := h.m.store.thread(ctx, agent, replayCause(l), to, 0, turn, []sentMessage{{Ref: otogi.EventRef{Platform: "telegram", Account: "main", Conversation: "chat:1", SourceEvent: e.SourceID}}}, at); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.m.store.record(ctx, to, transcriptLine(e)); err != nil {
		t.Fatal(err)
	}
}

func offset(d time.Duration) string { return fmt.Sprintf("+%7.1fs", d.Seconds()) }

// describeRecord is a line on a reply or a held judgment: why the agent
// spoke or did not, how long it waited, and what it said.
func describeRecord(h *harness, r otogi.Record) string {
	p, _ := r.Payload.(map[string]any)
	s := fmt.Sprintf("%s %s trigger=%v", r.Kind, r.Outcome, p["trigger"])
	if r.Kind == "chat.held" {
		return s + fmt.Sprintf(" score=%.2f threshold=%.2f share=%.2f", p["score"], p["threshold"], p["share"])
	}
	if skipped, ok := p["skipped"]; ok {
		s += fmt.Sprintf(" skipped=%v", skipped)
	}
	if b, ok := p["batch"].(map[string]any); ok {
		s += fmt.Sprintf(" waited=%.1fs messages=%v", float64(b["waited_ms"].(int64))/1000, b["messages"])
		if still, ok := b["still"]; ok {
			s += fmt.Sprintf(" still=%.2f", still)
		}
	}
	if err, ok := p["error"]; ok {
		s += fmt.Sprintf(" error=%v", err)
	}
	refs, _ := p["messages"].([]otogi.EventRef)
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ref := range refs {
		for _, st := range h.streams {
			if st.ref.SourceEvent == ref.SourceEvent {
				s += ": " + oneLine(st.final.PlainText())
			}
		}
	}
	return s
}

// timedSummary counts a timed replay's replies by trigger, the waits to
// speak up given up and the replies left out as covered, and the judgments
// the agent's share held back.
func timedSummary(records []timedRecord) string {
	replied, held := map[string]int{}, map[string]int{}
	var gaveUp, covered, failed int
	var chimeWaits []time.Duration
	for _, tr := range records {
		p, _ := tr.r.Payload.(map[string]any)
		trigger, _ := p["trigger"].(string)
		switch {
		case tr.r.Kind == "chat.held":
			held[trigger]++
		case tr.r.Kind != "chat.reply":
		case p["skipped"] == "still":
			gaveUp++
		case p["skipped"] == "covered":
			covered++
		case tr.r.Outcome == "error":
			failed++
		default:
			replied[trigger]++
			if b, ok := p["batch"].(map[string]any); ok && trigger == "chime" {
				chimeWaits = append(chimeWaits, time.Duration(b["waited_ms"].(int64))*time.Millisecond)
			}
		}
	}
	waits := ""
	if len(chimeWaits) > 0 {
		waits = fmt.Sprintf(" (waited %s to %s)", slices.Min(chimeWaits).Round(100*time.Millisecond), slices.Max(chimeWaits).Round(100*time.Millisecond))
	}
	return fmt.Sprintf("replies by trigger %v; spoke up unasked %d%s, gave up %d; covered %d; held back %v; failed %d",
		replied, replied["chime"], waits, gaveUp, covered, held, failed)
}

// TestReplayTimed replays a real group chat at the times its messages were
// sent, to an agent with judge.wake, wait, follow and chime on at their
// defaults, on the real decision service and model, and prints each message
// and record as it comes, then how often the agent spoke and why. Real chats
// stay out of the repository: it runs only when OTOGI_CHAT_REPLAY names a
// local JSON Lines file of replayLine, every line with "at", and
// OTOGI_TYPESAFE_API_KEY and a model's key are set:
//
//	OTOGI_CHAT_REPLAY=chat.jsonl OTOGI_CHAT_REPLAY_AGENT=Mizuki OTOGI_TYPESAFE_API_KEY=... OTOGI_DEEPSEEK_API_KEY=... go test -run ReplayTimed -v -timeout 0 ./modules/chat
//
// The model is DeepSeek's (OTOGI_DEEPSEEK_MODEL, OTOGI_DEEPSEEK_BASE_URL)
// when its key is set, else Gemini's (OTOGI_GEMINI_API_KEY,
// OTOGI_GEMINI_MODEL, OTOGI_GEMINI_BASE_URL). OTOGI_CHAT_REPLAY_SYSTEM names
// a file holding the agent's prompt; OTOGI_CHAT_REPLAY_RULES is its
// judge.rules. OTOGI_CHAT_REPLAY_REALTIME=1 replays on the real clock.
func TestReplayTimed(t *testing.T) {
	file := os.Getenv("OTOGI_CHAT_REPLAY")
	if file == "" || os.Getenv("OTOGI_TYPESAFE_API_KEY") == "" {
		t.Skip("OTOGI_CHAT_REPLAY or OTOGI_TYPESAFE_API_KEY is not set")
	}
	quiet := slog.New(slog.DiscardHandler)
	var model llm.Provider
	var modelName string
	var err error
	switch {
	case os.Getenv("OTOGI_DEEPSEEK_API_KEY") != "":
		model, err = openai.New(openai.Config{Name: "deepseek", BaseURL: cmp.Or(os.Getenv("OTOGI_DEEPSEEK_BASE_URL"), "https://api.deepseek.com"), APIKey: os.Getenv("OTOGI_DEEPSEEK_API_KEY"), Recorder: otogi.NopRecorder{}, Logger: quiet})
		modelName = cmp.Or(os.Getenv("OTOGI_DEEPSEEK_MODEL"), "deepseek-flash")
	case os.Getenv("OTOGI_GEMINI_API_KEY") != "":
		model, err = gemini.New(gemini.Config{APIKey: os.Getenv("OTOGI_GEMINI_API_KEY"), BaseURL: os.Getenv("OTOGI_GEMINI_BASE_URL"), Recorder: otogi.NopRecorder{}, Logger: quiet})
		modelName = cmp.Or(os.Getenv("OTOGI_GEMINI_MODEL"), "gemini-3.8-flash")
	default:
		t.Skip("neither OTOGI_DEEPSEEK_API_KEY nor OTOGI_GEMINI_API_KEY is set")
	}
	if err != nil {
		t.Fatal(err)
	}
	var system []byte
	if path := os.Getenv("OTOGI_CHAT_REPLAY_SYSTEM"); path != "" {
		if system, err = os.ReadFile(path); err != nil {
			t.Fatal(err)
		}
	}
	dm, err := decisionmodule.New(decisionmodule.Config{Jev: &decisionmodule.JevConfig{APIKey: "${OTOGI_TYPESAFE_API_KEY}"}}, os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	dh := &decisionHost{services: map[string]any{}}
	if err := dm.Start(context.Background(), dh); err != nil {
		t.Fatal(err)
	}
	name := cmp.Or(os.Getenv("OTOGI_CHAT_REPLAY_AGENT"), "luna")
	agent := ProfileConfig{Name: name, Keywords: []string{name}, Provider: "replay", Model: modelName, System: strings.TrimSpace(string(system)),
		Judge: &JudgeConfig{Rules: os.Getenv("OTOGI_CHAT_REPLAY_RULES"), Wake: &WakeConfig{}, Wait: &WaitConfig{}, Follow: &FollowConfig{}, Chime: &ChimeConfig{}}}
	realtime := os.Getenv("OTOGI_CHAT_REPLAY_REALTIME") == "1"
	start := time.Now()
	records := replayTimed(t, realtime, readReplay(t, file), Config{Profiles: []ProfileConfig{agent}}, dh.services[decision.Service].(decision.Decider), model)
	t.Logf("%s; %d decision requests; real time %s (realtime %v)", timedSummary(records), len(dh.records), time.Since(start).Round(time.Second), realtime)
}

// scripted answers the judgment by the text of the message asked about: the
// agent is addressed when named, would like to speak up on a message
// starting "anyone", and carries on its exchange on a question; it no
// longer means to speak up once someone says "never mind"; every sender
// has finished.
func scripted(id string, req decision.Request) (float64, bool) {
	text := asked(id, req)
	sure := func(yes bool) float64 {
		if yes {
			return 0.9
		}
		return 0.1
	}
	switch {
	case strings.HasPrefix(id, "done."):
		return 0.95, true
	case strings.HasPrefix(id, "wake."):
		return sure(strings.Contains(text, "luna")), true
	case strings.HasPrefix(id, "chime."):
		return sure(strings.HasPrefix(text, "anyone")), true
	case strings.HasPrefix(id, "follow."):
		return sure(strings.HasSuffix(text, "?")), true
	case strings.HasPrefix(id, "still."):
		history := req.State.(judgeState).History
		return sure(!strings.Contains(history[len(history)-1].Text, "never mind")), true
	}
	return 0, false
}

// A timed replay on virtual time does what the same chat does on the real
// clock, at the same times, with the module's code as it runs live: a chime
// spoken once the chat pauses, a follow-up once its sender has finished, a
// chime given up when the chat moves on, and a message naming the agent
// answered. The services answer by script, taking real time off the
// bubble, which the clock moves on by. With OTOGI_CHAT_REPLAY_REALTIME=1 it
// runs on the real clock, for some four minutes, to the same expectations.
func TestReplayTimedKeepsTheChatsTimes(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 11, 24, 13, 18, 0, 0, time.Local)
	at := func(s float64) int64 { return start.Add(time.Duration(s * float64(time.Second))).UnixMilli() }
	lines := []replayLine{
		{From: "Zoe", Text: "morning all", At: at(-300), History: true},
		{From: "Alice", Text: "anyone know a good ramen place", At: at(0)},
		{From: "Bob", Text: "is it open late?", At: at(40)},
	}
	for i := range 10 { // enough chat to leave the agent's exchange
		lines = append(lines, replayLine{From: []string{"Carol", "Dave"}[i%2], Text: fmt.Sprintf("chatter %d", i), At: at(60 + 5*float64(i))})
	}
	lines = append(lines,
		replayLine{From: "Erin", Text: "anyone seen the new movie", At: at(120)},
		replayLine{From: "Frank", Text: "never mind, it is out next week", At: at(130)},
		replayLine{From: "Gina", Text: "luna what is 2+2", At: at(200)},
	)
	d := &decider{answer: scripted, before: func(decision.Request) { time.Sleep(20 * time.Millisecond) }}
	model := &provider{responses: []llm.Response{answer("sure")}, before: func(int) { time.Sleep(50 * time.Millisecond) }}
	agent := ProfileConfig{Name: "luna", Keywords: []string{"luna"}, Provider: "luna", Model: "m",
		Judge: &JudgeConfig{Wake: &WakeConfig{}, Wait: &WaitConfig{}, Follow: &FollowConfig{}, Chime: &ChimeConfig{}}}
	realtime := os.Getenv("OTOGI_CHAT_REPLAY_REALTIME") == "1"
	began := time.Now()
	records := replayTimed(t, realtime, lines, Config{Profiles: []ProfileConfig{agent}}, d, model)
	t.Logf("%s; real time %s", timedSummary(records), time.Since(began).Round(time.Millisecond))

	type want struct {
		at, waited float64 // seconds
		trigger    string
		skipped    string
		messages   []string
	}
	wants := []want{
		{24, 24, "chime", "", []string{"m2"}},
		{42.75, 2.75, "follow", "", []string{"m3"}},
		// Judged with what comes within wait.min of it, "never mind" has
		// the agent give up once that is past.
		{132, 12, "chime", "still", []string{"m14"}},
		{202.75, 2.75, "decision", "", []string{"m16"}},
	}
	var got []want
	for _, tr := range records {
		if tr.r.Kind != "chat.reply" && tr.r.Kind != "chat.held" {
			continue
		}
		p := tr.r.Payload.(map[string]any)
		b, _ := p["batch"].(map[string]any)
		skipped, _ := p["skipped"].(string)
		messages, _ := b["messages"].([]string)
		waited, _ := b["waited_ms"].(int64)
		got = append(got, want{at: tr.at.Seconds(), waited: float64(waited) / 1000, trigger: p["trigger"].(string), skipped: skipped, messages: messages})
	}
	// The requests take some real time, which the clock moves on by: half
	// a second covers it.
	near := func(a, b float64) bool { return a-b < 0.5 && b-a < 0.5 }
	ok := len(got) == len(wants)
	for i := 0; ok && i < len(got); i++ {
		g, w := got[i], wants[i]
		ok = near(g.at, w.at) && near(g.waited, w.waited) && g.trigger == w.trigger && g.skipped == w.skipped && slices.Equal(g.messages, w.messages)
	}
	if !ok {
		t.Fatalf("records %+v, want %+v", got, wants)
	}
	// The model is told the chat's time, not the bubble's.
	if system := model.requests[0].System; !strings.Contains(system, "It is Tuesday, 24 November 2026, 13:18") {
		t.Fatalf("the agent was told %q", system)
	}
}

// A request off the bubble moves the clock on by the time it took, and one
// slower than its context's deadline ends at that deadline with its error,
// on both sides of the bubble, as it would on the real clock.
func TestOffBubbleKeepsTheRequestsTime(t *testing.T) {
	t.Parallel()
	o := newOffBubble(t)
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		if err := o.do(context.Background(), func(context.Context) { time.Sleep(30 * time.Millisecond) }); err != nil {
			t.Fatal(err)
		}
		if took := time.Since(start); took < 30*time.Millisecond || took > 300*time.Millisecond {
			t.Fatalf("the clock moved %v for a request of 30ms", took)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		start = time.Now()
		var inside error
		err := o.do(ctx, func(ctx context.Context) { <-ctx.Done(); inside = ctx.Err() })
		if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(inside, context.DeadlineExceeded) {
			t.Fatalf("errors %v and, off the bubble, %v; want the deadline", err, inside)
		}
		if took := time.Since(start); took != 100*time.Millisecond {
			t.Fatalf("ended %v after it started, want the deadline's 100ms", took)
		}
	})
}
