package kernel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// invocation is one unit of module work: an event delivery for now.
type invocation struct {
	kernel   *Kernel
	event    otogi.Event
	origin   otogi.Origin
	originID string
	module   otogi.ModuleID
	host     *host
	streams  *streams
	mu       sync.Mutex
	slots    map[string]bool
}

func (i *invocation) Host() otogi.Host { return i.host }

func (i *invocation) Origin() otogi.Origin { return i.origin }

func (i *invocation) WithOrigin(id string) otogi.Invocation {
	return &invocation{kernel: i.kernel, event: i.event, origin: i.origin, originID: "module:" + id, module: i.module, host: i.host, streams: i.streams}
}

func (i *invocation) Send(ctx context.Context, slot string, msg otogi.Message) (otogi.EventRef, error) {
	if i.event.Conversation.ID == "" {
		return otogi.EventRef{}, fmt.Errorf("send: no event conversation to reply in; use SendTo: %w", otogi.ErrInvalid)
	}
	return i.SendTo(ctx, slot, i.event.Destination(), msg)
}

func (i *invocation) SendTo(ctx context.Context, slot string, to otogi.Destination, msg otogi.Message) (otogi.EventRef, error) {
	if to.Platform == "" || to.Account == "" || to.Conversation == "" {
		return otogi.EventRef{}, fmt.Errorf("send: incomplete destination %+v: %w", to, otogi.ErrInvalid)
	}
	if err := validContent(msg); err != nil {
		return otogi.EventRef{}, err
	}
	return i.apply(ctx, slot, otogi.Effect{Kind: otogi.EffectSend, To: to, Message: msg})
}

func (i *invocation) Edit(ctx context.Context, slot string, ref otogi.EventRef, msg otogi.Message) error {
	if err := validContent(msg); err != nil {
		return err
	}
	_, err := i.applyToRef(ctx, slot, otogi.Effect{Kind: otogi.EffectEdit, Ref: ref, Message: msg})
	return err
}

func (i *invocation) Delete(ctx context.Context, slot string, ref otogi.EventRef) error {
	_, err := i.applyToRef(ctx, slot, otogi.Effect{Kind: otogi.EffectDelete, Ref: ref})
	return err
}

func (i *invocation) React(ctx context.Context, slot string, ref otogi.EventRef, reaction string) error {
	_, err := i.applyToRef(ctx, slot, otogi.Effect{Kind: otogi.EffectReact, Ref: ref, Reaction: reaction})
	return err
}

func (i *invocation) FetchMedia(ctx context.Context, from otogi.Destination, src otogi.MediaSource) ([]byte, error) {
	fetcher, ok := i.kernel.effector.(otogi.MediaFetcher)
	if !ok {
		return nil, fmt.Errorf("fetch media: %w", otogi.ErrUnsupported)
	}
	return fetcher.FetchMedia(ctx, from, src)
}

func (i *invocation) applyToRef(ctx context.Context, slot string, e otogi.Effect) (otogi.EventRef, error) {
	if e.Ref.Platform == "" || e.Ref.Account == "" || e.Ref.SourceEvent == "" {
		return otogi.EventRef{}, fmt.Errorf("%s: reference does not identify a message: %w", e.Kind, otogi.ErrInvalid)
	}
	return i.apply(ctx, slot, e)
}

// apply claims slot, completes e with its idempotency key, hands it to the
// effector, and records the outcome.
func (i *invocation) apply(ctx context.Context, slot string, e otogi.Effect) (otogi.EventRef, error) {
	if err := i.claim(e.Kind, slot); err != nil {
		return otogi.EventRef{}, err
	}
	e.Module, e.Origin, e.Key = i.module, i.originID, otogi.IdempotencyKeyFor(i.originID, i.module, slot)
	started := time.Now()
	ref, err := i.kernel.effector.Apply(ctx, e)
	component, scope := "module:"+string(i.module), effectScope(i.event, e)
	if err != nil {
		i.kernel.record(scope, "effect.failed", component, "error", time.Since(started), map[string]any{"slot": slot, "effect": recordable(e), "error": err.Error()})
		return otogi.EventRef{}, err
	}
	i.kernel.record(scope, "effect.applied", component, "ok", time.Since(started), map[string]any{"slot": slot, "effect": recordable(e), "ref": ref})
	return ref, nil
}

// effectScope is where an effect's record belongs: the event it answers,
// for its trace, and the conversation it changes, which a job or a SendTo
// does not share with the event.
func effectScope(event otogi.Event, e otogi.Effect) otogi.Event {
	if e.To.Conversation != "" { // a send or a stream
		event.Platform, event.Account, event.Conversation.ID = e.To.Platform, e.To.Account, e.To.Conversation
	} else {
		event.Platform, event.Account, event.Conversation.ID = e.Ref.Platform, e.Ref.Account, e.Ref.Conversation
	}
	return event
}

// claim reserves slot for one effect of this unit of work. Reusing a slot is
// an error: both effects would share a key and the second would be lost.
func (i *invocation) claim(kind otogi.EffectKind, slot string) error {
	if slot == "" {
		return fmt.Errorf("%s: empty slot: %w", kind, otogi.ErrInvalid)
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.slots[slot] {
		return fmt.Errorf("%s: slot %q already used: %w", kind, slot, otogi.ErrInvalid)
	}
	if i.slots == nil {
		i.slots = make(map[string]bool)
	}
	i.slots[slot] = true
	return nil
}

func validContent(msg otogi.Message) error {
	if len(msg.Parts) == 0 {
		return fmt.Errorf("empty message: %w", otogi.ErrInvalid)
	}
	return msg.Validate()
}

// recordable is e as its record keeps it: inline media appear as their size
// and SHA-256, as language model calls record them. The bytes are in the
// platform message the effect made, and a video sent inline would put tens
// of megabytes in the log. A medium that already names its SHA-256 keeps
// that one, not two of the same.
func recordable(e otogi.Effect) otogi.Effect {
	var parts []otogi.Part
	for i, p := range e.Message.Parts {
		if p.Media == nil || p.Media.Source.Kind != otogi.MediaSourceInline {
			continue
		}
		if parts == nil {
			parts = slices.Clone(e.Message.Parts)
		}
		m := *p.Media
		sum := sha256.Sum256(m.Source.Inline)
		m.ByteSize = int64(len(m.Source.Inline))
		if !slices.ContainsFunc(m.Hashes, func(h otogi.MediaHash) bool {
			return h.Algorithm == "sha256" && bytes.Equal(h.Value, sum[:])
		}) {
			m.Hashes = append(slices.Clone(m.Hashes), otogi.MediaHash{Algorithm: "sha256", Value: sum[:]})
		}
		m.Source.Inline = nil
		parts[i].Media = &m
	}
	if parts != nil {
		e.Message.Parts = parts
	}
	return e
}
