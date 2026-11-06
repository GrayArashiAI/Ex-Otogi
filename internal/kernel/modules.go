package kernel

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// routing is the module set the kernel delivers to. Reconfigure replaces it
// whole; it is guarded by Kernel.mu.
type routing struct {
	modules    []*moduleEntry // sorted by module ID
	startOrder []*moduleEntry // providers before the modules requiring them
	byID       map[otogi.ModuleID]*moduleEntry
	commands   map[string][]*moduleEntry // command name -> declaring module
	providers  map[string]*moduleEntry   // service name -> providing module
	services   map[string]any            // filled by Provide during Start
	jobs       []*job                    // filled by Schedule during Start
}

// newEntry validates a module's descriptor and returns its entry, without a
// host.
func (k *Kernel) newEntry(module otogi.Module) (*moduleEntry, error) {
	d := module.Descriptor()
	if d.ID == "" {
		return nil, fmt.Errorf("empty module ID: %w", otogi.ErrInvalid)
	}
	entry := &moduleEntry{
		module: module, descriptor: d, subscribes: make(map[otogi.EventAction]bool, len(d.Subscribes)), timeout: k.cfg.HandlerTimeout,
		provides: set(d.Provides), requires: set(d.Requires), counts: &outcomeCounts{},
	}
	for _, action := range d.Subscribes {
		if !action.Valid() {
			return nil, fmt.Errorf("module %q subscribes to invalid action %q: %w", d.ID, action, otogi.ErrInvalid)
		}
		entry.subscribes[action] = true
	}
	if d.Timeout != 0 {
		entry.timeout = d.Timeout
	}
	lists := map[string]bool{}
	for _, l := range d.Allowlists {
		if l.Name == "" || lists[l.Name] {
			return nil, fmt.Errorf("module %q: empty or duplicate allowlist name %q: %w", d.ID, l.Name, otogi.ErrInvalid)
		}
		lists[l.Name] = true
	}
	entry.host = &host{kernel: k, entry: entry, log: k.log.With("module", d.ID)}
	return entry, nil
}

// newRouting validates a module set as a whole: unique IDs and commands, and
// a service graph without gaps or cycles.
func newRouting(entries []*moduleEntry) (*routing, error) {
	entries = slices.Clone(entries)
	slices.SortFunc(entries, func(a, b *moduleEntry) int { return strings.Compare(string(a.descriptor.ID), string(b.descriptor.ID)) })
	r := &routing{modules: entries, byID: map[otogi.ModuleID]*moduleEntry{}, commands: map[string][]*moduleEntry{}, services: map[string]any{}}
	for _, entry := range entries {
		if r.byID[entry.descriptor.ID] != nil {
			return nil, fmt.Errorf("duplicate module ID %q: %w", entry.descriptor.ID, otogi.ErrInvalid)
		}
		r.byID[entry.descriptor.ID] = entry
		for _, c := range entry.descriptor.Commands {
			if !commandName.MatchString(c.Name) {
				return nil, fmt.Errorf("module %q command %q: want lowercase letters, digits, or _: %w", entry.descriptor.ID, c.Name, otogi.ErrInvalid)
			}
			if owner := r.commands[c.Name]; owner != nil {
				return nil, fmt.Errorf("command %q declared by both %q and %q: %w", c.Name, owner[0].descriptor.ID, entry.descriptor.ID, otogi.ErrInvalid)
			}
			r.commands[c.Name] = []*moduleEntry{entry}
		}
	}
	var err error
	if r.providers, r.startOrder, err = startOrder(entries); err != nil {
		return nil, err
	}
	return r, nil
}

// Reconfigure changes the running module set: each module in set replaces
// the running module with its ID, or is added, and each module in remove is
// removed. The new instances start first, in dependency order, and receive
// every delivery from then on; the instances they replace finish their
// deliveries in flight and are then stopped, in the background. Per
// conversation, a replacement's deliveries still run after the old
// instance's. If a new instance fails to start, the new ones already started
// are stopped, nothing else changes, and the error is returned.
//
// The modules requiring a replaced module's services hold the old services,
// so they must be replaced with it (see Dependents); otherwise Reconfigure
// returns ErrInvalid. Reconfigure calls run one at a time.
func (k *Kernel) Reconfigure(ctx context.Context, set []otogi.Module, remove []otogi.ModuleID) error {
	k.reconfiguring.Lock()
	defer k.reconfiguring.Unlock()
	k.mu.Lock()
	cur, ready := k.routes, k.state == stateReady
	k.mu.Unlock()
	if !ready {
		return fmt.Errorf("reconfigure: kernel not ready: %w", otogi.ErrUnavailable)
	}
	replaced := map[otogi.ModuleID]bool{}
	for _, id := range remove {
		if cur.byID[id] == nil {
			return fmt.Errorf("reconfigure: remove %q: no such module: %w", id, otogi.ErrInvalid)
		}
		replaced[id] = true
	}
	var fresh []*moduleEntry
	for _, m := range set {
		entry, err := k.newEntry(m)
		if err != nil {
			return fmt.Errorf("reconfigure: %w", err)
		}
		id := entry.descriptor.ID
		if slices.Contains(remove, id) {
			return fmt.Errorf("reconfigure: module %q both set and removed: %w", id, otogi.ErrInvalid)
		}
		if old := cur.byID[id]; old != nil {
			entry.counts = old.counts
			replaced[id] = true
		}
		fresh = append(fresh, entry)
	}
	entries := slices.Clone(fresh)
	for _, entry := range cur.modules {
		if replaced[entry.descriptor.ID] {
			continue
		}
		for name := range entry.requires {
			if p := cur.providers[name]; replaced[p.descriptor.ID] {
				return fmt.Errorf("reconfigure: module %q holds service %q of %q; replace it too: %w", entry.descriptor.ID, name, p.descriptor.ID, otogi.ErrInvalid)
			}
		}
		entries = append(entries, entry)
	}
	next, err := newRouting(entries)
	if err != nil {
		return fmt.Errorf("reconfigure: %w", err)
	}
	for name, service := range cur.services {
		if !replaced[cur.providers[name].descriptor.ID] {
			next.services[name] = service
		}
	}
	for _, j := range cur.jobs {
		if !replaced[j.entry.descriptor.ID] {
			next.jobs = append(next.jobs, j)
		}
	}

	k.mu.Lock()
	k.pending = next
	k.mu.Unlock()
	starting := slices.DeleteFunc(slices.Clone(next.startOrder), func(e *moduleEntry) bool { return !slices.Contains(fresh, e) })
	err = k.startModules(ctx, starting)
	k.mu.Lock()
	k.pending = nil
	if err == nil && k.state != stateReady {
		err = fmt.Errorf("reconfigure: kernel stopping: %w", otogi.ErrUnavailable)
		k.mu.Unlock()
		return errors.Join(err, k.stopModules(ctx, starting))
	}
	if err != nil {
		k.mu.Unlock()
		return fmt.Errorf("reconfigure: %w", err)
	}
	k.routes = next
	var old []*moduleEntry
	for _, entry := range cur.startOrder {
		if replaced[entry.descriptor.ID] {
			entry.retired = true // no delivery is enqueued for it from now on
			old = append(old, entry)
		}
	}
	k.retiring.Go(func() { k.retire(old) })
	k.mu.Unlock()
	k.log.Info("modules reconfigured", "started", ids(starting), "removed", remove)
	return nil
}

// retire stops replaced instances, in reverse start order, once their
// deliveries in flight have finished.
func (k *Kernel) retire(entries []*moduleEntry) {
	for _, entry := range entries {
		entry.active.Wait()
	}
	if err := k.stopModules(context.Background(), entries); err != nil {
		k.log.Error("replaced module did not stop cleanly", "error", err)
		k.record(otogi.Event{}, "module.failed", "kernel", "error", 0, map[string]any{"step": "stop replaced instance", "error": err.Error()})
	}
}

// Dependents returns the running modules that hold services of the given
// modules, directly or through other modules, sorted by ID. Replacing a
// module means replacing its dependents with it.
func (k *Kernel) Dependents(modules ...otogi.ModuleID) []otogi.ModuleID {
	k.mu.Lock()
	r := k.routes
	k.mu.Unlock()
	held := map[otogi.ModuleID]bool{}
	for _, id := range modules {
		held[id] = true
	}
	var out []otogi.ModuleID
	for _, entry := range r.startOrder { // providers first, so one pass is transitive
		if held[entry.descriptor.ID] {
			continue
		}
		for name := range entry.requires {
			if held[r.providers[name].descriptor.ID] {
				held[entry.descriptor.ID] = true
				out = append(out, entry.descriptor.ID)
				break
			}
		}
	}
	slices.Sort(out)
	return out
}

// startModules starts entries, in order, while what they Provide and
// Schedule goes to the routing they join. If one fails to start, those
// already started are stopped.
func (k *Kernel) startModules(ctx context.Context, entries []*moduleEntry) error {
	for i, entry := range entries {
		starter, ok := entry.module.(otogi.Starter)
		if !ok {
			continue
		}
		k.mu.Lock()
		entry.starting = true
		k.mu.Unlock()
		err := guard(func() error { return starter.Start(ctx, entry.host) })
		k.mu.Lock()
		entry.starting = false
		k.mu.Unlock()
		if err != nil {
			stopErr := k.stopModules(ctx, entries[:i])
			return errors.Join(fmt.Errorf("start module %q: %w", entry.descriptor.ID, err), stopErr)
		}
	}
	return nil
}

// stopModules stops modules implementing otogi.Stopper, in reverse order.
func (k *Kernel) stopModules(ctx context.Context, entries []*moduleEntry) error {
	var errs []error
	for _, entry := range slices.Backward(entries) {
		if stopper, ok := entry.module.(otogi.Stopper); ok {
			if err := guard(func() error { return stopper.Stop(ctx) }); err != nil {
				errs = append(errs, fmt.Errorf("stop module %q: %w", entry.descriptor.ID, err))
			}
		}
	}
	return errors.Join(errs...)
}

// joining returns the routing an entry belongs to: the one being started
// while it starts, else the running one. The caller holds k.mu.
func (k *Kernel) joining(entry *moduleEntry) *routing {
	if entry.starting && k.pending != nil {
		return k.pending
	}
	return k.routes
}

func ids(entries []*moduleEntry) []otogi.ModuleID {
	out := make([]otogi.ModuleID, len(entries))
	for i, e := range entries {
		out[i] = e.descriptor.ID
	}
	return out
}
