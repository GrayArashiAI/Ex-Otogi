package management

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

func taskEvents(p TaskPage) []string {
	out := []string{}
	for _, t := range p.Tasks {
		out = append(out, t.Event)
	}
	return out
}

// An event's records make one task when a module did something about it;
// one only received and passed over is no task, and one a module handles
// now is running.
func TestTasks(t *testing.T) {
	l := openLog(t)
	ctx := context.Background()
	at := time.Unix(1000, 0)
	write := func(event, kind, component, outcome string, d time.Duration, payload any) {
		at = at.Add(time.Minute)
		l.Record(ctx, otogi.Record{At: at, Kind: kind, Component: component, Event: otogi.EventID(event), Platform: "telegram", Conversation: "c1", Outcome: outcome, Duration: d, Payload: payload})
	}
	received := func(event, text string) {
		e := otogi.Event{ID: otogi.EventID(event), Action: otogi.ActionMessageCreated, Actor: otogi.Actor{ID: "u1", Name: "Alice"}, Message: otogi.TextMessage(text)}
		write(event, "event.received", "kernel", "", 0, e)
	}
	received("busy", "luna, read the card")
	write("busy", "module.completed", "module:bilibili", "ok", 0, nil)
	write("busy", "llm.completed", "llm:gemini", "ok", 8*time.Second, nil)
	write("busy", "chat.tool", "module:chat", "error", time.Second, map[string]string{"function": "todou_card"})
	write("busy", "chat.reply", "module:chat", "ok", 3*time.Minute, nil)
	received("quiet", "hello")
	write("quiet", "module.completed", "module:chat", "ok", 0, nil)
	received("slow", "and this?")
	write("slow", "module.completed", "module:chat", "slow", time.Minute, nil)
	received("new", "luna?")
	running := []Running{{Module: "chat", Origin: "new", Event: "new", Platform: "telegram", Conversation: "c1", Started: at}}

	page, err := l.Tasks(ctx, TaskQuery{}, running)
	if err != nil || !reflect.DeepEqual(taskEvents(page), []string{"new", "slow", "busy"}) || page.More {
		t.Fatalf("tasks = %v more=%v, %v", taskEvents(page), page.More, err)
	}
	busy := page.Tasks[2]
	want := Task{
		Event: "busy", Platform: "telegram", Conversation: "c1", Action: "message.created", Actor: "Alice", Text: "luna, read the card",
		Started: time.Unix(1000, 0).Add(time.Minute).UTC(), Ended: time.Unix(1000, 0).Add(5 * time.Minute).UTC(),
		Records: 5, Outcome: "error", Components: []string{"llm:gemini", "module:chat"}, Running: []Running{}, Cursor: busy.Cursor,
	}
	if !reflect.DeepEqual(busy, want) {
		t.Fatalf("busy =\n%+v, want\n%+v", busy, want)
	}
	if n := page.Tasks[0]; n.Outcome != "running" || len(n.Running) != 1 || n.Records != 1 || n.Text != "luna?" {
		t.Fatalf("new = %+v", n)
	}

	first, _ := l.Tasks(ctx, TaskQuery{Limit: 1}, running)
	if got := taskEvents(first); !reflect.DeepEqual(got, []string{"new", "slow"}) || !first.More {
		t.Fatalf("first page = %v more=%v", got, first.More)
	}
	next, _ := l.Tasks(ctx, TaskQuery{Limit: 1, Before: first.Tasks[1].Cursor}, running)
	if got := taskEvents(next); !reflect.DeepEqual(got, []string{"busy"}) || next.More {
		t.Fatalf("next page = %v more=%v", got, next.More)
	}
	for name, test := range map[string]struct {
		q    TaskQuery
		want []string
	}{
		"error":        {TaskQuery{Outcome: "error"}, []string{"busy"}},
		"slow":         {TaskQuery{Outcome: "slow"}, []string{"slow"}},
		"running":      {TaskQuery{Outcome: "running"}, []string{"new"}},
		"text":         {TaskQuery{Text: "the card"}, []string{"busy"}},
		"other chat":   {TaskQuery{Conversation: "c2"}, []string{}},
		"text is kept": {TaskQuery{Text: "todou_card"}, []string{"busy"}},
	} {
		page, err := l.Tasks(ctx, test.q, running)
		if got := taskEvents(page); err != nil || !reflect.DeepEqual(got, test.want) {
			t.Errorf("%s: got %v (%v), want %v", name, got, err, test.want)
		}
	}
}
