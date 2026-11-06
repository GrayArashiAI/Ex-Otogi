package main

import (
	"context"
	"log/slog"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/management"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// namesMiddleware names what drivers could not, from the chat directory,
// so modules (chat agents in particular) see what the panel shows: a chat
// by the name the operator gave it, else by the platform's latest title,
// and a sender by the latest name they were seen with. In a private chat,
// the other person is called what the chat is. The directory has already
// taken in the event itself, which the kernel records before middleware.
type namesMiddleware struct {
	events *management.EventLog
	log    *slog.Logger
}

func (namesMiddleware) ID() otogi.MiddlewareID { return "names" }

// Intercept never drops an event: names only label it, so a failed lookup
// is logged and the event goes on as the driver made it.
func (m namesMiddleware) Intercept(ctx context.Context, e *otogi.Event) (otogi.Verdict, error) {
	chat, person, err := m.events.Names(ctx, string(e.Platform), string(e.Conversation.ID), string(e.Actor.ID))
	if err != nil {
		m.log.Warn("names: look up", "event", e.ID, "error", err)
		return otogi.Continue, nil
	}
	if chat != "" {
		e.Conversation.Title = chat
	}
	if e.Actor.Name == "" {
		e.Actor.Name = person
	}
	if e.Actor.Name == "" && e.Conversation.Kind == otogi.ConversationDirect && e.Direction == otogi.DirectionIncoming {
		e.Actor.Name = chat
	}
	return otogi.Continue, nil
}
