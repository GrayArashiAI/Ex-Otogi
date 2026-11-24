package kernel

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// timerAttempts is how many times a timer run is tried before it is given
// up. With the waits of timerWait the last attempt comes 63 minutes after
// the timer was due: long enough to outlast a platform's short outage, and
// a failure that retrying cannot change stops within about an hour.
const timerAttempts = 7

// timerWait is how long a timer run waits after its failed attempt (counting
// from 1) before the next: a minute, doubling each time.
func timerWait(attempt int) time.Duration {
	return time.Minute << (attempt - 1)
}

func set(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// startOrder orders modules so every provider of a service starts before the
// modules requiring it, breaking ties by module ID. It rejects duplicate
// providers, missing providers, and cycles. It also returns each service's
// provider.
func startOrder(entries []*moduleEntry) (map[string]*moduleEntry, []*moduleEntry, error) {
	providers := map[string]*moduleEntry{}
	for _, entry := range entries {
		for name := range entry.provides {
			if other := providers[name]; other != nil {
				return nil, nil, fmt.Errorf("service %q provided by both %q and %q: %w", name, other.descriptor.ID, entry.descriptor.ID, otogi.ErrInvalid)
			}
			providers[name] = entry
		}
	}
	for _, entry := range entries {
		for name := range entry.requires {
			if providers[name] == nil {
				return nil, nil, fmt.Errorf("module %q requires service %q, which no module provides: %w", entry.descriptor.ID, name, otogi.ErrInvalid)
			}
		}
	}
	var order []*moduleEntry
	placed := map[*moduleEntry]bool{}
	for len(order) < len(entries) {
		progressed := false
		for _, entry := range entries { // entries are sorted by ID
			if placed[entry] {
				continue
			}
			ready := true
			for name := range entry.requires {
				if !placed[providers[name]] { // requiring your own service never becomes ready: a cycle
					ready = false
				}
			}
			if ready {
				order, placed[entry], progressed = append(order, entry), true, true
				break
			}
		}
		if !progressed {
			var stuck []otogi.ModuleID
			for _, entry := range entries {
				if !placed[entry] {
					stuck = append(stuck, entry.descriptor.ID)
				}
			}
			return nil, nil, fmt.Errorf("service dependency cycle among %v: %w", stuck, otogi.ErrInvalid)
		}
	}
	return providers, order, nil
}

// job is a scheduled module job. next and pending are owned by the clock.
type job struct {
	entry    *moduleEntry
	name     string
	schedule cron.Schedule
	fn       otogi.Job
	next     time.Time
	pending  atomic.Bool // a run is queued or running; the next one is skipped
}

// clock fires due jobs and timers once a second until the kernel stops.
func (k *Kernel) clock() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-k.runCtx.Done():
			return
		case now := <-ticker.C:
			k.tick(now)
		}
	}
}

func (k *Kernel) tick(now time.Time) {
	k.mu.Lock()
	r, ready := k.routes, k.state == stateReady
	k.mu.Unlock()
	if !ready {
		return // draining: no new runs
	}
	for _, j := range r.jobs {
		if j.next.After(now) {
			continue
		}
		at := j.next
		j.next = j.schedule.Next(now)
		k.runJob(j, at)
	}
	if k.cfg.Storage == nil {
		return
	}
	var handlers []otogi.ModuleID
	for _, entry := range r.modules {
		if _, ok := entry.module.(otogi.TimerHandler); ok {
			handlers = append(handlers, entry.descriptor.ID)
		}
	}
	if len(handlers) == 0 {
		return
	}
	err := k.cfg.Storage.DueTimers(k.runCtx, now, handlers, func(module otogi.ModuleID, t otogi.Timer, attempt int) { k.runTimer(module, t, attempt) })
	if err != nil && k.runCtx.Err() == nil {
		k.log.Error("timers: load due timers", "error", err)
		k.record(otogi.Event{}, "timer.failed", "kernel", "error", 0, map[string]any{"step": "load due timers", "error": err.Error()})
	}
}

func (k *Kernel) runJob(j *job, at time.Time) {
	id := j.entry.descriptor.ID
	if j.pending.Swap(true) {
		k.log.Warn("job skipped: previous run still pending", "module", id, "job", j.name)
		k.record(otogi.Event{}, "job.skipped", "module:"+string(id), "skipped", 0, map[string]any{"job": j.name, "scheduled_at": at, "reason": "previous run still pending"})
		return
	}
	d := &delivery{
		module:   j.entry,
		lane:     laneKey{module: id, conversation: otogi.ConversationID("job:" + j.name)},
		origin:   otogi.Origin{Job: j.name, ScheduledAt: at},
		originID: fmt.Sprintf("job:%s:%s:%d", id, j.name, at.Unix()),
		call:     func(ctx context.Context, inv *invocation) error { return j.fn(ctx, inv) },
		done:     func(otogi.ModuleOutcome) { j.pending.Store(false) },
	}
	if !k.enqueueWork(d) {
		j.pending.Store(false)
	}
}

func (k *Kernel) runTimer(module otogi.ModuleID, t otogi.Timer, attempt int) {
	running := string(module) + "\x00" + t.Key
	k.mu.Lock()
	entry := k.routes.byID[module]
	if entry == nil || k.timers[running] {
		k.mu.Unlock()
		return // removed since the tick began, or running
	}
	handler, ok := entry.module.(otogi.TimerHandler)
	if !ok {
		k.mu.Unlock()
		return // replaced since the tick began by an instance without timers
	}
	k.timers[running] = true
	k.mu.Unlock()
	timer := t
	d := &delivery{
		module:   entry,
		lane:     laneKey{module: module, conversation: otogi.ConversationID("timer:" + t.Key)},
		origin:   otogi.Origin{Timer: &timer, ScheduledAt: t.At},
		originID: fmt.Sprintf("timer:%s:%s:%d", module, t.Key, t.At.UnixMilli()),
		call:     func(ctx context.Context, inv *invocation) error { return handler.HandleTimer(ctx, timer, inv) },
		done:     func(o otogi.ModuleOutcome) { k.finishTimer(module, timer, attempt, running, o) },
	}
	if !k.enqueueWork(d) {
		k.mu.Lock()
		delete(k.timers, running)
		k.mu.Unlock()
	}
}

// finishTimer deletes a timer after a successful run and postpones a failed
// one, until its last attempt fails: then the timer is deleted and recorded
// with all it held, since nothing will run it again. A run abandoned because
// the kernel stopped is left due and uncounted, so it fires on the next start.
func (k *Kernel) finishTimer(module otogi.ModuleID, t otogi.Timer, attempt int, running string, o otogi.ModuleOutcome) {
	defer func() {
		k.mu.Lock()
		delete(k.timers, running)
		k.mu.Unlock()
	}()
	if k.runCtx.Err() != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var err error
	switch {
	case o.Err == nil:
		err = k.cfg.Storage.FinishTimer(ctx, module, t.Key, t.At)
	case attempt < timerAttempts:
		err = k.cfg.Storage.RetryTimer(ctx, module, t.Key, t.At, time.Now().Add(timerWait(attempt)))
	default:
		err = k.cfg.Storage.FinishTimer(ctx, module, t.Key, t.At)
		// Modules keep JSON in their timers; written as it is, an operator
		// reads it in the event log. Other bytes go in as bytes.
		var payload any = t.Payload
		if json.Valid(t.Payload) {
			payload = json.RawMessage(t.Payload)
		}
		k.log.Error("timers: gave up", "module", module, "timer", t.Key, "attempts", attempt, "error", o.Err)
		k.record(otogi.Event{}, "timer.failed", "module:"+string(module), "error", 0, map[string]any{
			"step": "gave up", "timer": t.Key, "at": t.At, "attempts": attempt, "error": o.Err.Error(), "payload": payload,
		})
	}
	if err != nil {
		k.log.Error("timers: update after run", "module", module, "timer", t.Key, "error", err)
		k.record(otogi.Event{}, "timer.failed", "module:"+string(module), "error", 0, map[string]any{"step": "update after run", "timer": t.Key, "error": err.Error()})
	}
}

// enqueueWork queues a job or timer delivery unless the kernel is draining or
// stopped, or the instance was replaced since the tick began; the
// replacement's own jobs and timers run on later ticks.
func (k *Kernel) enqueueWork(d *delivery) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.state != stateReady || d.module.retired {
		return false
	}
	k.sched.enqueue(d)
	return true
}
