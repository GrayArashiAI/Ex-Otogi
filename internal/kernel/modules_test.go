package kernel

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// instance is one instance of a module, logging its lifecycle and the
// events it handles as "<name> <what>".
type instance struct {
	moduleFunc
	name  string
	log   *eventLog
	start func(otogi.Host) error
}

type eventLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *eventLog) add(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, line)
}

func (l *eventLog) get() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.lines)
}

func newInstance(d otogi.ModuleDescriptor, name string, log *eventLog, handle func(context.Context, otogi.Event) error) instance {
	return instance{name: name, log: log, moduleFunc: moduleFunc{descriptor: d, handle: func(ctx context.Context, e otogi.Event, _ otogi.Invocation) error {
		log.add(name + " " + e.Message.PlainText())
		if handle != nil {
			return handle(ctx, e)
		}
		return nil
	}}}
}

func (m instance) Start(_ context.Context, h otogi.Host) error {
	m.log.add(m.name + " start")
	if m.start != nil {
		return m.start(h)
	}
	return nil
}

func (m instance) Stop(context.Context) error {
	m.log.add(m.name + " stop")
	return nil
}

var subscriber = otogi.ModuleDescriptor{ID: "a", Subscribes: []otogi.EventAction{otogi.ActionMessageCreated}}

func TestReconfigureStartsTheNewInstanceBeforeRetiringTheOld(t *testing.T) {
	log := &eventLog{}
	release := make(chan struct{})
	v1 := newInstance(subscriber, "v1", log, func(context.Context, otogi.Event) error {
		<-release
		return nil
	})
	k := start(t, Config{}, []otogi.Module{v1}, nil, nil)
	first := submit(t, k, event("c", "one"))
	eventually(t, "v1 handles the first event", func() bool { return slices.Contains(log.get(), "v1 one") })

	if err := k.Reconfigure(context.Background(), []otogi.Module{newInstance(subscriber, "v2", log, nil)}, nil); err != nil {
		t.Fatal(err)
	}
	second := submit(t, k, event("c", "two"))
	other := await(t, submit(t, k, event("d", "three")))
	if len(other.Modules) != 1 || other.Modules[0].Err != nil {
		t.Fatalf("other conversation outcome = %+v", other)
	}
	if got := log.get(); !slices.Equal(got, []string{"v1 start", "v1 one", "v2 start", "v2 three"}) {
		t.Fatalf("before release: %v; want v1 still running, and v2 waiting behind it in c", got)
	}
	close(release)
	await(t, first)
	await(t, second)
	eventually(t, "v1 stops", func() bool { return slices.Contains(log.get(), "v1 stop") })
	if got := log.get(); !slices.Equal(got[4:], []string{"v2 two", "v1 stop"}) && !slices.Equal(got[4:], []string{"v1 stop", "v2 two"}) {
		t.Fatalf("after release: %v", got)
	}
	if c := k.Stats().Modules["a"]; c.Completed != 3 {
		t.Fatalf("counts = %+v, want 3 completed across both instances", c)
	}
}

func TestReconfigureKeepsTheOldInstanceWhenTheNewOneFails(t *testing.T) {
	log := &eventLog{}
	k := start(t, Config{}, []otogi.Module{newInstance(subscriber, "v1", log, nil)}, nil, nil)
	broken := newInstance(subscriber, "v2", log, nil)
	broken.start = func(otogi.Host) error { return errors.New("bad settings") }
	if err := k.Reconfigure(context.Background(), []otogi.Module{broken}, nil); err == nil {
		t.Fatal("Reconfigure with a failing Start succeeded")
	}
	await(t, submit(t, k, event("c", "one")))
	if got := log.get(); !slices.Equal(got, []string{"v1 start", "v2 start", "v1 one"}) {
		t.Fatalf("lifecycle = %v", got)
	}
}

func TestReconfigureAddsAndRemovesModules(t *testing.T) {
	log := &eventLog{}
	k := start(t, Config{}, []otogi.Module{newInstance(subscriber, "a", log, nil)}, nil, nil)
	b := otogi.ModuleDescriptor{ID: "b", Subscribes: subscriber.Subscribes}
	if err := k.Reconfigure(context.Background(), []otogi.Module{newInstance(b, "b", log, nil)}, []otogi.ModuleID{"a"}); err != nil {
		t.Fatal(err)
	}
	o := await(t, submit(t, k, event("c", "one")))
	if len(o.Modules) != 1 || o.Modules[0].Module != "b" {
		t.Fatalf("outcome = %+v, want b only", o)
	}
	eventually(t, "a stops", func() bool { return slices.Contains(log.get(), "a stop") })
	if err := k.Reconfigure(context.Background(), nil, []otogi.ModuleID{"a"}); !errors.Is(err, otogi.ErrInvalid) {
		t.Fatalf("removing a missing module = %v, want ErrInvalid", err)
	}
}

type named string

func (n named) Greet() string { return string(n) }

func TestReconfigureReplacesDependentsWithTheirProvider(t *testing.T) {
	provider := func(greeting string) otogi.Module {
		return starterModule{moduleFunc: describe("p", []string{"greeter"}, nil), start: func(h otogi.Host) error { return h.Provide("greeter", named(greeting)) }}
	}
	var greeting atomic.Value
	consumer := starterModule{moduleFunc: describe("c", nil, []string{"greeter"}), start: func(h otogi.Host) error {
		g, err := otogi.Lookup[greeter](h, "greeter")
		if err == nil {
			greeting.Store(g.Greet())
		}
		return err
	}}
	k := start(t, Config{}, []otogi.Module{provider("hello"), consumer}, nil, nil)
	if got := k.Dependents("p"); !slices.Equal(got, []otogi.ModuleID{"c"}) {
		t.Fatalf("Dependents(p) = %v", got)
	}
	for name, change := range map[string]func() error{
		"replace alone": func() error { return k.Reconfigure(context.Background(), []otogi.Module{provider("hi")}, nil) },
		"remove alone":  func() error { return k.Reconfigure(context.Background(), nil, []otogi.ModuleID{"p"}) },
	} {
		if err := change(); !errors.Is(err, otogi.ErrInvalid) {
			t.Fatalf("%s: %v, want ErrInvalid", name, err)
		}
	}
	if err := k.Reconfigure(context.Background(), []otogi.Module{provider("hi"), consumer}, nil); err != nil {
		t.Fatal(err)
	}
	if got := greeting.Load(); got != "hi" {
		t.Fatalf("consumer sees %v, want the new provider's service", got)
	}
}

// The composition root finds the service the running provider gave: the
// new instance's after a replacement, none once the provider is removed.
func TestServiceIsTheRunningProviders(t *testing.T) {
	provider := func(greeting string) otogi.Module {
		return starterModule{moduleFunc: describe("p", []string{"greeter"}, nil), start: func(h otogi.Host) error { return h.Provide("greeter", named(greeting)) }}
	}
	k := start(t, Config{}, []otogi.Module{provider("hello")}, nil, nil)
	greeting := func() any {
		s, ok := k.Service("greeter")
		if !ok {
			return nil
		}
		return s.(greeter).Greet()
	}
	if got := greeting(); got != "hello" {
		t.Fatalf("Service = %v, want hello", got)
	}
	if err := k.Reconfigure(context.Background(), []otogi.Module{provider("hi")}, nil); err != nil {
		t.Fatal(err)
	}
	if got := greeting(); got != "hi" {
		t.Fatalf("after the replacement, Service = %v, want hi", got)
	}
	if err := k.Reconfigure(context.Background(), nil, []otogi.ModuleID{"p"}); err != nil {
		t.Fatal(err)
	}
	if got := greeting(); got != nil {
		t.Fatalf("after the removal, Service = %v, want none", got)
	}
	if _, ok := k.Service("nothing"); ok {
		t.Fatal("a service nobody provides is found")
	}
}

func TestReconfigureMovesJobsToTheNewInstance(t *testing.T) {
	var runs [2]atomic.Int64
	scheduler := func(i int) otogi.Module {
		return starterModule{moduleFunc: describe("jobs", nil, nil), start: func(h otogi.Host) error {
			return h.Schedule("tick", "@every 1s", func(context.Context, otogi.Invocation) error {
				runs[i].Add(1)
				return nil
			})
		}}
	}
	k := start(t, Config{}, []otogi.Module{scheduler(0)}, nil, nil)
	eventually(t, "the first instance's job runs", func() bool { return runs[0].Load() > 0 })
	if err := k.Reconfigure(context.Background(), []otogi.Module{scheduler(1)}, nil); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the new instance's job runs", func() bool { return runs[1].Load() > 0 })
	old := runs[0].Load() // a run queued before the swap has finished by now
	eventually(t, "the new instance's job runs again", func() bool { return runs[1].Load() > 1 })
	if runs[0].Load() != old {
		t.Fatalf("the replaced instance's job ran after the swap")
	}
}
