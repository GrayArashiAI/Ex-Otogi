package chat

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// afterSlot is the slot of what an agent says once its picture or sticker
// did not go.
const afterSlot = "after-failure"

// afterFailed is how chat.reply records the agent asked again once its
// picture or sticker did not go: the message it then sent, absent when it
// wrote nothing, the request's usage, and the error that left it unsaid.
type afterFailed struct {
	Ref   string    `json:"ref,omitempty"`
	Usage llm.Usage `json:"usage"`
	Error string    `json:"error,omitempty"`
}

// notSent makes the result of the call that picked a picture or sticker,
// in turns, say that it did not go and why, in place: the agent then reads
// its call as having failed, as it did.
func notSent(turns []llm.Turn, err error) {
	for i := len(turns) - 1; i >= 0; i-- {
		for j, r := range turns[i].Results {
			if (r.Name != stickerTool && r.Name != resendTool) || r.Error != "" {
				continue
			}
			var out struct {
				Sent bool `json:"sent"`
			}
			if json.Unmarshal(r.Output, &out) != nil || !out.Sent {
				continue
			}
			turns[i].Results[j].Output = nil
			turns[i].Results[j].Error = "not sent: " + err.Error() + ". If it matters, tell them so in a few words; or write nothing."
			return
		}
	}
}

// afterFailure asks a's agent again once its picture or sticker did not
// go, req continued by turns, the reply's own, whose call now says so (see
// notSent), and sends what the agent writes as a message of its own. It
// may call no function: with no rounds allowed, a call is answered by
// asking it to answer with what it has. It returns the record, the turns
// the agent added, and the sent message and its text, nil and empty when
// it wrote nothing or it could not be sent.
func (m *Module) afterFailure(ctx context.Context, a ask, req llm.Request, turns []llm.Turn, tools map[string]llm.Tool,
	inv otogi.Invocation, last otogi.Event, quote bool) (*afterFailed, []llm.Turn, *otogi.EventRef, string) {
	p := a.p
	if ctx.Err() != nil {
		return &afterFailed{Error: ctx.Err().Error()}, nil, nil, ""
	}
	req.Turns = append(slices.Clone(req.Turns), turns...)
	out, err := llm.Run(llm.ContextWithAgent(ctx, p.Name), p.provider, req, tools, last, llm.Limits{Rounds: 0, Reserve: *m.cfg.ToolReserve},
		nil, nil, func(c llm.CallRecord) { m.recordCall(ctx, last, p, c) })
	rec := &afterFailed{Usage: out.Usage}
	if err != nil {
		rec.Error = err.Error()
		return rec, nil, nil, ""
	}
	if strings.TrimSpace(out.Reply.PlainText()) == "" {
		return rec, out.Turns, nil, ""
	}
	ref, err := inv.Send(ctx, afterSlot, present(p, last, out.Reply, quote))
	if err != nil {
		rec.Error = err.Error()
		return rec, out.Turns, nil, ""
	}
	rec.Ref = string(ref.SourceEvent)
	return rec, out.Turns, &ref, out.Reply.PlainText()
}
