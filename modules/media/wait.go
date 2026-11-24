package media

import (
	"context"
	"sync"
	"time"

	contract "github.com/TundraWork/Ex-Otogi/pkg/media"
)

// keepResults is how long a finished result stays in memory for a caller
// that asks after the module finished: the chat module asks within seconds
// of a message arriving. Without it, a caller asking about media whose
// every model failed would wait out its whole deadline.
const keepResults = time.Minute

// result is how the module finished with a piece of media: described, or
// an error (contract.ErrNotDescribed, otogi.ErrUnavailable).
type result struct {
	d   contract.Description
	err error
}

// waits hands results from Handle to callers of Describe, by wait key.
type waits struct {
	mu      sync.Mutex
	waiting map[string][]chan result
	done    map[string]finished
	now     func() time.Time
}

type finished struct {
	result
	at time.Time
}

func newWaits(now func() time.Time) *waits {
	return &waits{waiting: map[string][]chan result{}, done: map[string]finished{}, now: now}
}

// finish hands r to everyone waiting on k and keeps it for keepResults.
// It also drops expired results: when nobody waits, as for agents that only
// look descriptions up, wait never runs to drop them.
func (w *waits) finish(k string, r result) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, ch := range w.waiting[k] {
		ch <- r // buffered: a waiter that left is not blocked on
	}
	delete(w.waiting, k)
	now := w.now()
	w.expire(now)
	w.done[k] = finished{result: r, at: now}
}

// expire drops the results kept longer than keepResults. w.mu is held.
func (w *waits) expire(now time.Time) {
	for key, f := range w.done {
		if now.Sub(f.at) > keepResults {
			delete(w.done, key)
		}
	}
}

// wait returns the result for k, kept or to come, or ctx's error once ctx
// ends first.
func (w *waits) wait(ctx context.Context, k string) (result, error) {
	w.mu.Lock()
	w.expire(w.now())
	if f, ok := w.done[k]; ok {
		w.mu.Unlock()
		return f.result, nil
	}
	ch := make(chan result, 1)
	w.waiting[k] = append(w.waiting[k], ch)
	w.mu.Unlock()
	select {
	case r := <-ch:
		return r, nil
	case <-ctx.Done():
		w.mu.Lock()
		defer w.mu.Unlock()
		chans := w.waiting[k]
		for i, c := range chans {
			if c == ch {
				w.waiting[k] = append(chans[:i:i], chans[i+1:]...)
				break
			}
		}
		if len(w.waiting[k]) == 0 {
			delete(w.waiting, k)
		}
		return result{}, ctx.Err()
	}
}
