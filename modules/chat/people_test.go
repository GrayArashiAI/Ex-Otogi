package chat

import (
	"math"
	"strings"
	"testing"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// know gives the module a people list.
func (h *harness) know(people ...PersonConfig) {
	h.t.Helper()
	known, err := (&Config{People: people}).people()
	if err != nil {
		h.t.Fatal(err)
	}
	h.m.people = known
}

func TestPeopleConfig(t *testing.T) {
	t.Parallel()
	providers := func(string) (llm.Provider, error) { return &provider{}, nil }
	base := []ProfileConfig{{Name: "a", Keywords: []string{"a"}, Provider: "p", Model: "m"}}
	m, err := New(Config{Profiles: base, People: []PersonConfig{
		{User: "qq:user:7C2E", Name: " 苗润 ", Note: "the owner's\nfriend", ThresholdFactor: 0.9},
		{User: "telegram:user:7"},
	}}, providers)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.people["qq:user:7C2E"]; got != (person{name: "苗润", note: "the owner's friend", factor: 0.9}) {
		t.Fatalf("person %+v", got)
	}
	if got := m.people["telegram:user:7"]; got.factor != 1 {
		t.Fatalf("unset factor %v, want 1", got.factor)
	}
	for user, want := range map[string]string{
		"7C2E":          "people[1].user",
		":user:7C2E":    "people[1].user",
		"qq:":           "people[1].user",
		"qq:user:B3 F8": "people[1].user",
		"qq:user:x":     "people[1].user \"qq:user:x\" is listed twice",
	} {
		_, err := New(Config{Profiles: base, People: []PersonConfig{{User: "qq:user:x"}, {User: user}}}, providers)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("user %q: err %v, want %q", user, err, want)
		}
	}
	for _, f := range []float64{0.05, -1, 20} {
		_, err := New(Config{Profiles: base, People: []PersonConfig{{User: "qq:user:x", ThresholdFactor: f}}}, providers)
		if err == nil || !strings.Contains(err.Error(), "people[0].threshold_factor") {
			t.Errorf("factor %v: err %v", f, err)
		}
	}
}

// Wherever the model is told who sent a message, a listed person goes by the
// name the list gives, the platform's following where it differs; the
// transcript keeps the platform's, so a changed list applies to it at once.
func TestNamesFromThePeopleList(t *testing.T) {
	h := newHarness(t)
	h.llm["otogi"].responses = []llm.Response{answer("hi"), answer("again")}
	h.know(PersonConfig{User: "telegram:user:45", Name: "苗润"}, PersonConfig{User: "telegram:user:Bob", Name: "Bob"})
	old := h.message("45", "the meeting moved")
	h.mustHandle(old)
	h.mustHandle(h.message("Bob", "ok"))
	h.mustHandle(h.message("Carol", "otogi, when?", replyingTo(string(old.SourceID))))
	text := lastUserText(h.llm["otogi"].requests[0])
	for _, want := range []string{"] 苗润 (45): the meeting moved", "] Bob: ok", "Replying to 苗润 (45):\nthe meeting moved", "Carol: otogi, when?"} {
		if !strings.Contains(text, want) {
			t.Errorf("request lacks %q:\n%s", want, text)
		}
	}

	h.know(PersonConfig{User: "telegram:user:45", Name: "润润"})
	h.mustHandle(h.message("45", "otogi, and now?"))
	text = lastUserText(h.llm["otogi"].requests[1])
	for _, want := range []string{"] 润润 (45): the meeting moved", "\n润润 (45): otogi, and now?"} {
		if !strings.Contains(text, want) {
			t.Errorf("after the list changed, request lacks %q:\n%s", want, text)
		}
	}
}

// The notes of the people in a request lead it, in the order they appear,
// each once; they go in that request only, not into the thread.
func TestNotesOnThePeoplePresent(t *testing.T) {
	h := newHarness(t)
	h.llm["otogi"].responses = []llm.Response{answer("hi"), answer("sure")}
	h.know(
		PersonConfig{User: "telegram:user:45", Name: "苗润", Note: "the owner's friend"},
		PersonConfig{User: "telegram:user:Bob", Note: "runs the group"},
		PersonConfig{User: "telegram:user:Dan", Note: "not here"},
		PersonConfig{User: "telegram:user:Eve", Name: "Evie"},
	)
	h.mustHandle(h.message("Bob", "hello"))
	h.mustHandle(h.message("Eve", "hey"))
	h.mustHandle(h.message("45", "otogi, hi"))
	text := lastUserText(h.llm["otogi"].requests[0])
	if want := "About people here:\n- Bob: runs the group\n- 苗润 (45): the owner's friend\n\nRecent messages in this chat"; !strings.HasPrefix(text, want) {
		t.Fatalf("request does not start with %q:\n%s", want, text)
	}
	if strings.Contains(text, "not here") || strings.Count(text, "the owner's friend") != 1 {
		t.Fatalf("request:\n%s", text)
	}

	h.mustHandle(h.message("45", "and then?", replyingTo("agent-1")))
	if kept := h.llm["otogi"].requests[1].Turns[0].Content.PlainText(); kept != "苗润 (45): otogi, hi" {
		t.Fatalf("the thread keeps %q", kept)
	}
}

// A private chat lists its person's note too; with no recent messages shown,
// only the sender and whom they quote are listed.
func TestNotesInPrivateChatsAndWithoutContext(t *testing.T) {
	h := newHarness(t, ProfileConfig{Name: "otogi", Keywords: []string{"otogi"}, Provider: "p", Model: "m", Context: ptr(0)})
	h.host.lists[[2]string{"otogi", "telegram:user:7"}] = "test"
	h.llm["p"].responses = []llm.Response{answer("hi"), answer("ok")}
	h.know(PersonConfig{User: "telegram:user:Alice", Note: "the owner"}, PersonConfig{User: "telegram:user:Bob", Note: "runs the group"}, PersonConfig{User: "telegram:user:Carol", Note: "a friend"})
	h.mustHandle(h.message("Alice", "otogi, hi", func(e *otogi.Event) {
		e.Conversation = otogi.Conversation{ID: "user:7", Kind: otogi.ConversationDirect}
	}))
	if text := lastUserText(h.llm["p"].requests[0]); text != "About people here:\n- Alice: the owner\n\notogi, hi" {
		t.Fatalf("private request %q", text)
	}

	h.mustHandle(h.message("Bob", "in the group"))
	quoted := h.message("Carol", "a question")
	h.mustHandle(quoted)
	h.mustHandle(h.message("Alice", "otogi, answer her", replyingTo(string(quoted.SourceID))))
	text := lastUserText(h.llm["p"].requests[1])
	if !strings.HasPrefix(text, "About people here:\n- Carol: a friend\n- Alice: the owner\n\nReplying to Carol:") || strings.Contains(text, "runs the group") {
		t.Fatalf("request:\n%s", text)
	}
}

// A sender's threshold factor multiplies the judgment threshold; one that
// takes it past 1 leaves their messages unjudged, while mentions still work.
func TestThresholdFactor(t *testing.T) {
	d := &decider{scores: map[string]float64{"a1": 0.65}}
	p := judgedProfile("luna", 0.7)
	p.Mention = true
	h := newJudged(t, d, p)
	h.know(PersonConfig{User: "telegram:user:Owner", ThresholdFactor: 0.9}, PersonConfig{User: "telegram:user:Joker", ThresholdFactor: 2})

	h.mustHandle(h.message("Alice", "what do you think, luna?"))
	if len(h.llm["luna"].requests) != 0 {
		t.Fatal("0.65 woke the agent at 0.7")
	}
	h.mustHandle(h.message("Owner", "what do you think, luna?"))
	if len(h.llm["luna"].requests) != 1 {
		t.Fatal("0.65 did not wake the agent at 0.7 × 0.9")
	}
	j := lastReply(h)["judged"].(*judgment)
	if j.Score != 0.65 || math.Abs(j.Threshold-0.63) > 1e-9 || j.Factor != 0.9 {
		t.Fatalf("judged %+v", j)
	}

	asked := len(d.requests)
	d.scores["a1"] = 0.99
	h.mustHandle(h.message("Joker", "luna, what do you think?"))
	if len(d.requests) != asked || len(h.llm["luna"].requests) != 1 {
		t.Fatalf("a factor past 1 was judged (%d requests) or woke the agent", len(d.requests)-asked)
	}
	h.mustHandle(h.message("Joker", "@bot what do you think?", func(e *otogi.Event) { e.Mentioned = true }))
	if len(h.llm["luna"].requests) != 2 || lastReply(h)["trigger"] != "mention" {
		t.Fatalf("a mention did not trigger: %+v", lastReply(h))
	}
}
