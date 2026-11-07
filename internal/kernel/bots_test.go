package kernel

import (
	"context"
	"sync"
	"testing"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Modules ignore bots' messages until the operator turns that off for one;
// the change applies at once, is recorded once, and outlasts a restart.
func TestIgnoreBotsSwitch(t *testing.T) {
	ctx := context.Background()
	storage, err := modulestore.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	var mu sync.Mutex
	got := map[otogi.ModuleID]int{}
	count := func(id otogi.ModuleID) otogi.Module {
		return module(id, func(context.Context, otogi.Event, otogi.Invocation) error {
			mu.Lock()
			defer mu.Unlock()
			got[id]++
			return nil
		})
	}
	rec := &recorder{}
	k := start(t, Config{Storage: storage, Recorder: rec}, []otogi.Module{count("cards"), count("transcript")}, nil, nil)
	fromBot := event("c", "x.com/a/status/1")
	fromBot.Actor = otogi.Actor{ID: "user:6", Bot: true}

	if o := await(t, submit(t, k, fromBot)); len(o.Modules) != 0 {
		t.Fatalf("a bot's message reached %+v", o.Modules)
	}
	for range 2 {
		if err := k.SetIgnoreBots(ctx, "transcript", false, "Tundra"); err != nil {
			t.Fatal(err)
		}
	}
	if o := await(t, submit(t, k, fromBot)); len(o.Modules) != 1 || o.Modules[0].Module != "transcript" {
		t.Fatalf("with transcript taking bots, the outcome is %+v", o)
	}
	await(t, submit(t, k, event("c", "hi")))
	mu.Lock()
	if got["cards"] != 1 || got["transcript"] != 2 {
		t.Fatalf("deliveries %v", got)
	}
	mu.Unlock()
	var changes int
	for _, r := range rec.records {
		if r.Kind == "bots.changed" {
			changes++
		}
	}
	if changes != 1 || k.IgnoresBots("transcript") || !k.IgnoresBots("cards") {
		t.Fatalf("%d changes recorded; transcript ignores bots %v", changes, k.IgnoresBots("transcript"))
	}

	again := start(t, Config{Storage: storage}, nil, nil, nil)
	if again.IgnoresBots("transcript") || !again.IgnoresBots("cards") {
		t.Fatal("the switch did not outlast a restart")
	}
}
