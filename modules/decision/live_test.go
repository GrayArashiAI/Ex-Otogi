package decision

import (
	"context"
	"os"
	"testing"
	"time"

	contract "github.com/TundraWork/Ex-Otogi/pkg/decision"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// TestLiveJev calls the real TypeSafe API. It runs only when
// OTOGI_TYPESAFE_API_KEY is set, so it is not part of make quality:
//
//	OTOGI_TYPESAFE_API_KEY=... go test -run Live -v ./modules/decision
func TestLiveJev(t *testing.T) {
	if os.Getenv("OTOGI_TYPESAFE_API_KEY") == "" {
		t.Skip("OTOGI_TYPESAFE_API_KEY is not set")
	}
	m, err := New(Config{Jev: &JevConfig{APIKey: "${OTOGI_TYPESAFE_API_KEY}", Timeout: otogi.Duration(30 * time.Second)}}, os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	h := &host{provided: map[string]any{}}
	if err := m.Start(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	got, err := m.Decide(context.Background(), contract.Request{
		State: map[string]any{"history": []map[string]string{
			{"sender": "Alice", "text": "Mizuki, what do you think of this song?"},
		}},
		Questions: map[string]contract.Question{
			"to_mizuki": {Instructions: "Is the last message in history addressed to Mizuki?"},
			"weather":   {Instructions: "Is the last message in history about the weather?"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("answers %v", got)
	if got["to_mizuki"] <= 0.5 || got["weather"] >= 0.5 {
		t.Errorf("answers = %v, want to_mizuki above 0.5 and weather below", got)
	}
	payload, _ := h.records[0].Payload.(map[string]any)
	usage, _ := payload["usage"].(jevUsage)
	t.Logf("model %v, usage %+v", payload["model"], usage)
	if usage.InputTokens <= 0 {
		t.Errorf("usage = %+v, want input tokens", usage)
	}
}
