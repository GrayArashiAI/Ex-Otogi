package chat

import (
	"context"
	"encoding/json"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// silentTool is the tool an agent with silent ends a turn with when it
// replies nothing. It is offered on every turn: when to use it is for the
// agent's prompt and notes to say, so that being called does not rule it
// out but only advises against it.
const silentTool = "stay_silent"

// silence is whether the model called stay_silent in one reply.
type silence struct{ called bool }

func (s *silence) tool() llm.Tool {
	return llm.NewEndingTool(llm.Function{
		Name:        silentTool,
		Description: "Send nothing for this turn. This ends your reply: anything you wrote before calling it still goes out.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(context.Context, otogi.Event, json.RawMessage) (any, error) {
		s.called = true
		return map[string]bool{"silent": true}, nil
	})
}

// note is the note ending a turn trigger started: the notes set for it, or,
// without notes, the built-in ones of follow-ups and chimes.
func (p *profile) note(trigger string) string {
	n := p.Notes
	if n == nil {
		switch trigger {
		case "follow":
			return followNote
		case "chime":
			return chimeNote
		}
		return ""
	}
	switch trigger {
	case "mention":
		return n.Mention
	case "reply":
		return n.Reply
	case "keyword":
		return n.Keyword
	case "decision":
		return n.Decision
	case "follow":
		return n.Follow
	case "chime":
		return n.Chime
	}
	return ""
}
