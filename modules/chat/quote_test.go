package chat

import (
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	mediasvc "github.com/TundraWork/Ex-Otogi/pkg/media"
)

// An agent that never quotes sends its reply and its sticker as messages of
// their own; replying to its reply still carries on its thread.
func TestQuoteNever(t *testing.T) {
	t.Parallel()
	p := stickerProfile()
	p.Quote = "never"
	h := newHarnessWith(t, map[string]any{mediasvc.Service: newDescriber(), mediasvc.StickersService: newStickerBox()}, p)
	h.llm["otogi"].responses = []llm.Response{picks("Here.", 3), answer("you're welcome")}
	h.mustHandle(h.message("Alice", "otogi cheer me up"))
	said := h.streams[0]
	if said.final.PlainText() != "Otogi\nHere." || len(said.final.Relations) != 0 {
		t.Fatalf("reply %q quotes %+v", said.final.PlainText(), said.final.Relations)
	}
	if len(h.sends) != 1 || len(h.sends[0].msg.Relations) != 0 {
		t.Fatalf("sticker %+v", h.sends)
	}
	h.echo(said)
	h.mustHandle(h.message("Alice", "thanks", replyingTo(string(said.ref.SourceEvent))))
	if _, payload := lastReplyRecord(t, h); payload["trigger"] != "reply" || payload["parent"] == int64(0) {
		t.Fatalf("the reply to the reply: %+v", payload)
	}
}

// when_interrupted quotes only when the chat said something else after the
// message to answer before the reply started, whatever the agent's context:
// here none, as the decision is the transcript's, not the prompt's.
func TestQuoteWhenInterrupted(t *testing.T) {
	t.Parallel()
	none := 0
	p := waitingProfile("luna", 100*time.Millisecond, 3*time.Second)
	p.Quote, p.Context = "when_interrupted", &none
	h := newJudged(t, &decider{answer: finished()}, p)

	h.mustHandle(h.message("Alice", "luna what is 2+2"))
	if quoted := h.streams[0].final.Relations; len(quoted) != 0 {
		t.Fatalf("quoted %+v with nothing said after the message", quoted)
	}

	ask := h.message("Alice", "luna and 3+3")
	errc := make(chan error, 1)
	go func() { errc <- h.handle(ask) }()
	h.mustHandle(h.message("Bob", "lunch anyone"))
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	quoted := h.streams[1].final.Relations
	if len(quoted) != 1 || quoted[0].Target.SourceEvent != ask.SourceID {
		t.Fatalf("quoted %+v, want the message answered, as Bob spoke after it", quoted)
	}
}
