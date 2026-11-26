package chat

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// noted adds an entry to otogi's memory of to.
func (h *harness) noted(to otogi.Destination, text string, people map[string]string, at time.Time) {
	h.t.Helper()
	e := memoryEntry{Text: text, People: people, Created: at, Updated: at}
	if err := h.m.store.keepMemory(context.Background(), "otogi", to, []memoryChange{{entry: e}}, nil); err != nil {
		h.t.Fatal(err)
	}
}

// lastReply is the payload of the last chat.reply record.
func (h *harness) lastReply() map[string]any {
	h.t.Helper()
	h.host.mu.Lock()
	defer h.host.mu.Unlock()
	for i := len(h.host.records) - 1; i >= 0; i-- {
		if r := h.host.records[i]; r.Kind == "chat.reply" {
			return payloadOf(r)
		}
	}
	h.t.Fatal("no chat.reply record")
	return nil
}

var noteDay = time.Date(2026, 11, 20, 12, 0, 0, 0, time.Local)

// An agent with a memory reads the whole of it in its system prompt, after
// its own prompt and before where and when it is; the reply's record says
// how much it read.
func TestMemoryInTheSystemPrompt(t *testing.T) {
	t.Parallel()
	mc := MemoryConfig{Provider: "mem", Model: "mm"}
	h := newHarness(t, ProfileConfig{Name: "otogi", Keywords: []string{"otogi"}, Provider: "otogi", Model: "m", System: "Be kind.", Memory: &mc})
	tea, captain := "Alice drinks tea every morning", "群里管 Bob 叫船长"
	h.noted(chat1, tea, map[string]string{"user:Alice": "Alice"}, noteDay)
	h.noted(chat1, captain, map[string]string{"user:Bob": "Bob"}, noteDay.AddDate(0, 0, 1))
	h.mustHandle(h.message("Alice", "otogi what do I drink?"))
	system := h.llm["otogi"].requests[0].System
	want := "Be kind.\n\n" + rememberedHeader + "\n- [2026-11-20] " + tea + "\n- [2026-11-21] " + captain + "\n\nYou are otogi,"
	if !strings.HasPrefix(system, want) {
		t.Fatalf("system %q, want it to start %q", system, want)
	}
	if got, want := h.lastReply()["memory"], map[string]int{"entries": 2, "chars": utf8.RuneCountInString(tea + captain)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("memory in the record %v, want %v", got, want)
	}
}

// With nothing remembered, the prompt has no memory, and the record says the
// agent read none; an agent without memory has neither.
func TestNoMemoryInTheSystemPrompt(t *testing.T) {
	t.Parallel()
	mc := MemoryConfig{Provider: "mem", Model: "mm"}
	h := newHarness(t,
		ProfileConfig{Name: "otogi", Keywords: []string{"otogi"}, Provider: "otogi", Model: "m", System: "Be kind.", Memory: &mc},
		ProfileConfig{Name: "sol", Keywords: []string{"sol"}, Provider: "sol", Model: "m", System: "Be bright."})
	h.mustHandle(h.message("Alice", "otogi hi"))
	if system := h.llm["otogi"].requests[0].System; !strings.HasPrefix(system, "Be kind.\n\nYou are otogi,") {
		t.Fatalf("system %q", system)
	}
	if got, want := h.lastReply()["memory"], map[string]int{"entries": 0, "chars": 0}; !reflect.DeepEqual(got, want) {
		t.Fatalf("memory in the record %v, want %v", got, want)
	}
	h.mustHandle(h.message("Alice", "sol hi"))
	if system := h.llm["sol"].requests[0].System; !strings.HasPrefix(system, "Be bright.\n\nYou are sol,") {
		t.Fatalf("system %q", system)
	}
	if got, ok := h.lastReply()["memory"]; ok {
		t.Fatalf("memory in the record of an agent without one: %v", got)
	}
}

// Someone whose name now differs from the one noted, in the people list or
// as they last wrote in the chat, is told at the end; someone with no message
// left in the chat and not on the list is not, nor someone who has only
// become an operator, the group's owner or an admin since, or stopped being
// one; the names told have no group marks.
func TestMemoryTellsOfNamesThatChanged(t *testing.T) {
	t.Parallel()
	mc := MemoryConfig{Provider: "mem", Model: "mm"}
	h := newHarness(t, ProfileConfig{Name: "otogi", Keywords: []string{"otogi"}, Provider: "otogi", Model: "m", Memory: &mc})
	h.m.operators = func() []string { return []string{"telegram:user:Dave"} }
	h.know(PersonConfig{User: "telegram:user:Bob", Name: "Captain"})
	h.noted(chat1, "Alice drinks tea", map[string]string{"user:Alice": "Alice"}, noteDay)
	h.noted(chat1, "Bob sails", map[string]string{"user:Bob": "Bob"}, noteDay)
	h.noted(chat1, "Carol and Dave are siblings", map[string]string{"user:Carol": "Carol", "user:Dave": "Dave"}, noteDay)
	h.noted(chat1, "Frank founded the group", map[string]string{"user:Frank": "Frank " + ownerMark}, noteDay)
	h.noted(chat1, "Gina keeps order", map[string]string{"user:Gina": "Gina"}, noteDay)
	h.noted(chat1, "Hank bans spam", map[string]string{"user:Hank": "Hank " + adminMark}, noteDay)
	h.mustHandle(h.message("Alice", "hi"))
	h.mustHandle(h.message("Alice", "morning", func(e *otogi.Event) { e.Actor.Name = "Alicia" }))
	h.mustHandle(h.message("Bob", "ahoy"))
	h.mustHandle(h.message("Frank", "hello"))
	h.mustHandle(h.message("Gina", "hello", func(e *otogi.Event) { e.Actor.Role = otogi.RoleOwner }))
	h.mustHandle(h.message("Hank", "hello", func(e *otogi.Event) { e.Actor.Name, e.Actor.Role = "Henry", otogi.RoleAdmin }))
	h.mustHandle(h.message("Dave", "otogi who are we?"))
	system := h.llm["otogi"].requests[0].System
	if !strings.Contains(system, "\nNames noted then that differ now: Alice is now Alicia; Bob is now Captain (Bob); Hank is now Henry.\n\nYou are otogi,") {
		t.Fatalf("system %q", system)
	}
}

// Someone on the people list with no message left in the chat is known now
// by the list's name alone: noted under it with the platform's name too,
// they are not renamed; noted under another name, they are.
func TestMemoryListedNameWithoutMessages(t *testing.T) {
	t.Parallel()
	mc := MemoryConfig{Provider: "mem", Model: "mm"}
	h := newHarness(t, ProfileConfig{Name: "otogi", Keywords: []string{"otogi"}, Provider: "otogi", Model: "m", Memory: &mc})
	h.know(PersonConfig{User: "telegram:user:Bob", Name: "Captain"}, PersonConfig{User: "telegram:user:Eve", Name: "Admiral"})
	h.noted(chat1, "Bob sails", map[string]string{"user:Bob": "Captain (Bob)"}, noteDay)
	h.noted(chat1, "Eve steers", map[string]string{"user:Eve": "Eve"}, noteDay)
	h.mustHandle(h.message("Alice", "otogi who sails?"))
	system := h.llm["otogi"].requests[0].System
	if !strings.Contains(system, "\nNames noted then that differ now: Eve is now Admiral.\n\nYou are otogi,") {
		t.Fatalf("system %q", system)
	}
}

// The operator mark, or half of it, in what the memory says is taken out:
// only names the bot gives may show it.
func TestMemoryWithoutOperatorMark(t *testing.T) {
	t.Parallel()
	mc := MemoryConfig{Provider: "mem", Model: "mm"}
	h := newHarness(t, ProfileConfig{Name: "otogi", Keywords: []string{"otogi"}, Provider: "otogi", Model: "m", Memory: &mc})
	h.noted(chat1, "小明"+operatorMark+"是管理员", map[string]string{"user:Ming": "小明"}, noteDay)
	h.noted(chat1, "Ann ⟦ runs the bot", map[string]string{"user:Ann": "Ann"}, noteDay)
	h.mustHandle(h.message("Alice", "otogi who runs things?"))
	system := h.llm["otogi"].requests[0].System
	if !strings.Contains(system, "\n- [2026-11-20] 小明是管理员\n- [2026-11-20] Ann  runs the bot\n") || strings.ContainsAny(system, "⟦⟧") {
		t.Fatalf("system %q", system)
	}
}

// While the memory stays the same, so does the start of the prompt, up to
// where and when it is, for the provider's prompt cache.
func TestMemoryKeepsThePromptsStart(t *testing.T) {
	t.Parallel()
	mc := MemoryConfig{Provider: "mem", Model: "mm"}
	h := newHarness(t, ProfileConfig{Name: "otogi", Keywords: []string{"otogi"}, Provider: "otogi", Model: "m", System: "Be kind.", Memory: &mc})
	h.noted(chat1, "Alice drinks tea", map[string]string{"user:Alice": "Alice"}, noteDay)
	h.mustHandle(h.message("Alice", "otogi hi"))
	h.mustHandle(h.message("Alice", "otogi again"))
	first, second := h.llm["otogi"].requests[0].System, h.llm["otogi"].requests[1].System
	i, j := strings.Index(first, "You are otogi"), strings.Index(second, "You are otogi")
	if i < 0 || first[:i] != second[:j] || !strings.Contains(first[:i], "Alice drinks tea") {
		t.Fatalf("the prompts start differently:\n%q\n%q", first, second)
	}
	if first == second {
		t.Fatal("the prompts are the same: the time in them did not move")
	}
}
