package management

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Received events build the chat directory: titles as the platform last
// gave them, names the operator sets, the latest message, and daily counts,
// which go with the records while the chats stay.
func TestChatDirectory(t *testing.T) {
	l := openLog(t)
	ctx := context.Background()
	day1 := time.Date(2026, 11, 1, 23, 0, 0, 0, time.UTC)
	receive := func(at time.Time, conversation, title string, action otogi.EventAction, text string) {
		e := otogi.Event{ID: otogi.EventID("e-" + text), Platform: "telegram", Account: "main", Action: action, Message: otogi.TextMessage(text),
			Conversation: otogi.Conversation{ID: otogi.ConversationID(conversation), Kind: otogi.ConversationGroup, Title: title}}
		l.Record(ctx, otogi.Record{At: at, Kind: "event.received", Component: "kernel", Event: e.ID, Platform: e.Platform, Account: e.Account, Conversation: e.Conversation.ID, Payload: e})
	}
	receive(day1, "chat:1", "Friends", otogi.ActionMessageCreated, "one")
	receive(day1.Add(2*time.Hour), "chat:1", "", otogi.ActionMessageCreated, "two") // the next day, without a title
	receive(day1.Add(3*time.Hour), "chat:1", "", otogi.ActionMessageEdited, "two, edited")
	receive(day1.Add(time.Hour), "user:7", "Alice", otogi.ActionMessageCreated, "hi")
	l.Record(ctx, otogi.Record{At: day1, Kind: "module.completed", Component: "module:chat", Conversation: "chat:2"})

	if ok, err := l.NameChat(ctx, "telegram", "user:7", "Alice (test)"); !ok || err != nil {
		t.Fatalf("NameChat = %v, %v", ok, err)
	}
	if ok, _ := l.NameChat(ctx, "telegram", "chat:404", "x"); ok {
		t.Fatal("named a chat never seen")
	}
	chats, err := l.Chats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 2 {
		t.Fatalf("chats %+v", chats)
	}
	friends, alice := chats[0], chats[1]
	if friends.Key() != "telegram:chat:1" || friends.Title != "Friends" || !friends.FirstSeen.Equal(day1) || !friends.LastSeen.Equal(day1.Add(3*time.Hour)) ||
		friends.LastMessage == nil || !strings.Contains(string(friends.LastMessage.Payload), `"two"`) {
		t.Fatalf("friends %+v, last message %+v", friends, friends.LastMessage)
	}
	if alice.Name != "Alice (test)" || alice.Title != "Alice" {
		t.Fatalf("alice %+v", alice)
	}
	days, err := l.ChatDays(ctx, "telegram", "chat:1", day1.Add(-24*time.Hour))
	if err != nil || len(days) != 2 || days[0] != (ChatDay{"2026-11-01", 1}) || days[1] != (ChatDay{"2026-11-02", 1}) {
		t.Fatalf("days %+v, %v", days, err)
	}

	if err := l.Prune(ctx, day1.Add(time.Hour+time.Minute)); err != nil {
		t.Fatal(err)
	}
	chats, _ = l.Chats(ctx)
	days, _ = l.ChatDays(ctx, "telegram", "chat:1", day1.Add(-24*time.Hour))
	if len(chats) != 2 || chats[1].LastMessage != nil || len(days) != 1 {
		t.Fatalf("after pruning: chats %+v, days %+v", chats, days)
	}
}
