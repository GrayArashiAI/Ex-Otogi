package management

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/management/queries"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Chat is a chat the bot has seen.
type Chat struct {
	Platform     string    `json:"platform"`
	Conversation string    `json:"conversation"`
	Account      string    `json:"account"` // the account that saw it last
	Kind         string    `json:"kind,omitempty"`
	Title        string    `json:"title,omitempty"` // as the platform names it
	Name         string    `json:"name,omitempty"`  // as the operator names it
	FirstSeen    time.Time `json:"first_seen"`
	LastSeen     time.Time `json:"last_seen"`
	// LastMessage is the event.received record of its latest new message,
	// unless that is older than the retention.
	LastMessage *Entry `json:"last_message,omitempty"`
}

// Key is the chat as /whoami prints it and allowlists name it.
func (c Chat) Key() string { return c.Platform + ":" + c.Conversation }

// ChatDay is how many new messages a chat had on one day (UTC).
type ChatDay struct {
	Day      string `json:"day"` // YYYY-MM-DD
	Messages int64  `json:"messages"`
}

func day(t time.Time) string { return t.UTC().Format(time.DateOnly) }

// seeChat updates the directory with a received event: the chat's facts,
// and for a new message, its latest message and the day's count.
func seeChat(ctx context.Context, q *queries.Queries, e otogi.Event, at time.Time, record int64) error {
	var last int64
	if e.Action == otogi.ActionMessageCreated {
		last = record
	}
	err := q.SeeChat(ctx, queries.SeeChatParams{
		Platform: string(e.Platform), Conversation: string(e.Conversation.ID), Account: string(e.Account),
		Kind: string(e.Conversation.Kind), Title: e.Conversation.Title, At: at.UnixMilli(), LastMessage: last,
	})
	if err == nil && last != 0 {
		err = q.CountMessage(ctx, queries.CountMessageParams{Platform: string(e.Platform), Conversation: string(e.Conversation.ID), Day: day(at)})
	}
	if err != nil {
		return fmt.Errorf("chat directory: %w", err)
	}
	return nil
}

// Chats returns every chat the bot has seen, most recently seen first.
func (l *EventLog) Chats(ctx context.Context) ([]Chat, error) {
	rows, err := queries.New(l.db).Chats(ctx)
	if err != nil {
		return nil, fmt.Errorf("event log: chats: %w", err)
	}
	out := make([]Chat, len(rows))
	for i, r := range rows {
		out[i] = Chat{
			Platform: r.Platform, Conversation: r.Conversation, Account: r.Account, Kind: r.Kind, Title: r.Title, Name: r.Name,
			FirstSeen: time.UnixMilli(r.FirstSeen).UTC(), LastSeen: time.UnixMilli(r.LastSeen).UTC(),
		}
		if r.MessageID.Valid {
			out[i].LastMessage = &Entry{
				ID: r.MessageID.Int64, At: time.UnixMilli(r.MessageAt.Int64).UTC(), Kind: "event.received", Component: "kernel",
				Platform: r.Platform, Account: r.Account, Conversation: r.Conversation, Payload: json.RawMessage(r.Message.String),
			}
		}
	}
	return out, nil
}

// NameChat sets the name the operator gives a chat; "" clears it. It
// reports whether the chat is known.
func (l *EventLog) NameChat(ctx context.Context, platform, conversation, name string) (bool, error) {
	n, err := queries.New(l.db).NameChat(ctx, queries.NameChatParams{Platform: platform, Conversation: conversation, Name: name})
	if err != nil {
		return false, fmt.Errorf("event log: name chat: %w", err)
	}
	return n > 0, nil
}

// ChatDays returns a chat's daily new messages since a day, oldest first;
// days without messages are left out.
func (l *EventLog) ChatDays(ctx context.Context, platform, conversation string, since time.Time) ([]ChatDay, error) {
	rows, err := queries.New(l.db).ChatDays(ctx, queries.ChatDaysParams{Platform: platform, Conversation: conversation, Since: day(since)})
	if err != nil {
		return nil, fmt.Errorf("event log: chat days: %w", err)
	}
	out := make([]ChatDay, len(rows))
	for i, r := range rows {
		out[i] = ChatDay{Day: r.Day, Messages: r.Messages}
	}
	return out, nil
}
