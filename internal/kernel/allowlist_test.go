package kernel

import (
	"context"
	"errors"
	"testing"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// A module answers in the chats on its list; a change made while handling an
// event joins that event's trace, and the lists outlast a restart.
func TestAllowlists(t *testing.T) {
	ctx := context.Background()
	storage, err := modulestore.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	answered := make(chan string, 10)
	cards := moduleFunc{
		descriptor: otogi.ModuleDescriptor{ID: "cards", Subscribes: subscriber.Subscribes, Allowlists: []otogi.AllowlistSpec{{Name: "cards", Title: "Cards"}}},
		handle: func(ctx context.Context, e otogi.Event, inv otogi.Invocation) error {
			if e.Message.PlainText() == "add qq:group:G1" {
				return inv.Host().SetAllowed(ctx, "cards", "qq:group:G1", true, e.ActorKey())
			}
			if inv.Host().Allowed("cards", e.ChatKey()) {
				answered <- string(e.Conversation.ID)
			}
			return nil
		},
	}
	rec := &recorder{}
	k := start(t, Config{Storage: storage, Recorder: rec}, []otogi.Module{cards}, nil, nil)

	if err := k.SetAllowed(ctx, "cards", "cards", "test:c", true, "Tundra's iPhone"); err != nil {
		t.Fatal(err)
	}
	if err := k.SetAllowed(ctx, "cards", "cards", "test:c", true, "again"); err != nil {
		t.Fatal(err)
	}
	await(t, submit(t, k, event("c", "hi")))
	await(t, submit(t, k, event("d", "hi")))
	if got := <-answered; got != "c" || len(answered) != 0 {
		t.Fatalf("answered in %q, then %d more", got, len(answered))
	}
	e := event("c", "add qq:group:G1")
	e.ID, e.Actor = "e-add", otogi.Actor{ID: "user:op"}
	await(t, submit(t, k, e))
	if r, ok := rec.kinds("e-add")["allowlist.changed module:cards"]; !ok || r.Payload.(map[string]any)["by"] != "test:user:op" {
		t.Fatalf("the change is not in the event's trace: %+v", rec.kinds("e-add"))
	}
	if err := k.SetAllowed(ctx, "cards", "cards", "test:c", false, "Tundra's iPhone"); err != nil {
		t.Fatal(err)
	}
	var changes int
	for _, r := range rec.records {
		if r.Kind == "allowlist.changed" {
			changes++
		}
	}
	if changes != 3 {
		t.Fatalf("%d changes recorded, want 3: setting what is already so is no change", changes)
	}
	if err := k.SetAllowed(ctx, "cards", "cards", "G1", true, "x"); !errors.Is(err, otogi.ErrInvalid) {
		t.Fatalf("a chat without its platform: %v, want ErrInvalid", err)
	}

	again, err := New(Config{Storage: storage}, []otogi.Module{cards}, nil, &messenger{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if err := again.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = again.Stop(ctx) }()
	got := again.Allowlists()
	if len(got) != 1 || got[0].Module != "cards" || got[0].List != "cards" || got[0].Chat != "qq:group:G1" || got[0].By != "test:user:op" || got[0].At.IsZero() {
		t.Fatalf("after restart: %+v", got)
	}
}
