package main

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/management"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Modules see the names the panel shows: the operator's name for a chat,
// a sender's last known name, and in a private chat, the chat's name for
// the other person.
func TestNamesMiddleware(t *testing.T) {
	ctx := context.Background()
	events, err := management.OpenEventLog(ctx, filepath.Join(t.TempDir(), "eventlog.sqlite"), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = events.Close() })
	received := func(e otogi.Event) otogi.Event {
		events.Record(ctx, otogi.Record{At: time.Now(), Kind: "event.received", Component: "kernel", Event: e.ID, Platform: e.Platform, Account: e.Account, Conversation: e.Conversation.ID, Payload: e})
		return e
	}
	message := func(conversation otogi.ConversationID, kind otogi.ConversationKind, actor otogi.ActorID, name string) otogi.Event {
		return otogi.Event{ID: otogi.EventID("e-" + string(conversation) + name), Platform: "qq", Account: "otogi", Action: otogi.ActionMessageCreated, Direction: otogi.DirectionIncoming,
			Conversation: otogi.Conversation{ID: conversation, Kind: kind}, Actor: otogi.Actor{ID: actor, Name: name}, Message: otogi.TextMessage("hi")}
	}
	names := namesMiddleware{events: events, log: slog.New(slog.DiscardHandler)}

	received(message("group:G1", otogi.ConversationGroup, "user:M1", "Alice"))
	if _, err := events.NameChat(ctx, "qq", "group:G1", "Bot Playground"); err != nil {
		t.Fatal(err)
	}
	e := received(message("group:G1", otogi.ConversationGroup, "user:M1", ""))
	if v, err := names.Intercept(ctx, &e); v != otogi.Continue || err != nil || e.Conversation.Title != "Bot Playground" || e.Actor.Name != "Alice" {
		t.Fatalf("group event %+v", e)
	}

	received(message("user:U9", otogi.ConversationDirect, "user:U9", ""))
	if _, err := events.NameChat(ctx, "qq", "user:U9", "Tundra"); err != nil {
		t.Fatal(err)
	}
	e = received(message("user:U9", otogi.ConversationDirect, "user:U9", ""))
	if _, err := names.Intercept(ctx, &e); err != nil || e.Conversation.Title != "Tundra" || e.Actor.Name != "Tundra" {
		t.Fatalf("private event %+v", e)
	}
}
