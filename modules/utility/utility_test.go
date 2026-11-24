package utility

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

type invocation struct {
	otogi.Invocation
	replies []string
}

func (i *invocation) Send(_ context.Context, _ string, m otogi.Message) (otogi.EventRef, error) {
	i.replies = append(i.replies, m.PlainText())
	return otogi.EventRef{}, nil
}

func command(name string, direction otogi.Direction, actor otogi.ActorID) otogi.Event {
	return otogi.Event{
		Platform: "telegram", Account: "main", Direction: direction,
		Conversation: otogi.Conversation{ID: "chat:42", Kind: otogi.ConversationGroup},
		Actor:        otogi.Actor{ID: actor}, SourceID: "telegram:common:chat:42:7",
		OccurredAt: time.Now().Add(-time.Second), Command: &otogi.Command{Name: name},
	}
}

func TestCommands(t *testing.T) {
	operators := []string{"telegram:user:1"}
	m := New(Config{}, Deps{
		Operators: func() []string { return operators },
		Status: func(context.Context) Status {
			return Status{Version: "v1", Ready: true, Uptime: time.Hour,
				Drivers: []DriverStatus{{Name: "telegram/main", Ready: true, Detail: "0 pending"}}, EventLog: EventLogStatus{OK: true}}
		},
		Commands: func() []CommandInfo {
			return []CommandInfo{{Module: "notes", Spec: otogi.CommandSpec{Name: "note", Usage: "[text]", Description: "notes"}}, {Module: "utility", Spec: otogi.CommandSpec{Name: "help"}}}
		},
	})
	tests := []struct {
		name      string
		event     otogi.Event
		want      []string
		forbidden string
	}{
		{"whoami", command("whoami", otogi.DirectionIncoming, "user:9"), []string{"You: telegram:user:9", "Chat: telegram:chat:42 (group)", "Account: telegram:main"}, "Message"},
		{"ping", command("ping", otogi.DirectionIncoming, "user:9"), []string{"Pong."}, ""},
		{"status for operator", command("status", otogi.DirectionIncoming, "user:1"), []string{"Status: ready", "Driver telegram/main: ready, 0 pending", "Event log: ok"}, ""},
		{"status denied", command("status", otogi.DirectionIncoming, "user:9"), []string{"Only operators", "telegram:user:9"}, "Version"},
		{"help", command("help", otogi.DirectionIncoming, "user:9"), []string{"Commands:\n/help\n/note [text] — notes"}, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inv := &invocation{}
			if err := m.Handle(context.Background(), test.event, inv); err != nil {
				t.Fatal(err)
			}
			got := inv.replies[0]
			for _, want := range test.want {
				if !strings.Contains(got, want) {
					t.Errorf("reply %q lacks %q", got, want)
				}
			}
			if test.forbidden != "" && strings.Contains(got, test.forbidden) {
				t.Errorf("reply %q contains %q", got, test.forbidden)
			}
			if strings.HasPrefix(got, "/") {
				t.Errorf("reply %q starts with a command prefix", got)
			}
		})
	}
	// The operators are the ones there are when the command comes.
	operators = []string{"telegram:user:9"}
	for actor, want := range map[otogi.ActorID]string{"user:9": "Status: ready", "user:1": "Only operators"} {
		inv := &invocation{}
		if err := m.Handle(context.Background(), command("status", otogi.DirectionIncoming, actor), inv); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(inv.replies[0], want) {
			t.Errorf("/status by %s after the operators changed: %q, want %q", actor, inv.replies[0], want)
		}
	}
}
