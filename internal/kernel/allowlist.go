package kernel

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// AllowedChat is a chat on a module's allowlist.
type AllowedChat struct {
	Module otogi.ModuleID `json:"module"`
	List   string         `json:"list"`
	Chat   string         `json:"chat"` // see otogi.Event.ChatKey
	At     time.Time      `json:"added_at"`
	By     string         `json:"added_by"`
}

type allowKey struct {
	module     otogi.ModuleID
	list, chat string
}

// loadAllowlists reads the allowlists into memory, where Allowed answers
// from.
func (k *Kernel) loadAllowlists(ctx context.Context) error {
	if k.cfg.Storage == nil {
		return nil
	}
	lists := map[allowKey]AllowedChat{}
	err := k.cfg.Storage.Allowlists(ctx, func(module otogi.ModuleID, list, chat string, at time.Time, by string) {
		lists[allowKey{module, list, chat}] = AllowedChat{Module: module, List: list, Chat: chat, At: at, By: by}
	})
	if err != nil {
		return fmt.Errorf("load allowlists: %w", err)
	}
	k.listsMu.Lock()
	k.lists = lists
	k.listsMu.Unlock()
	return nil
}

func (k *Kernel) allowed(module otogi.ModuleID, list, chat string) bool {
	k.listsMu.RLock()
	defer k.listsMu.RUnlock()
	_, ok := k.lists[allowKey{module, list, chat}]
	return ok
}

// SetAllowed adds chat to a module's allowlist, or removes it, and records
// the change as allowlist.changed with who made it: an actor key, or the
// operator's name in the panel. Setting what is already so changes nothing.
// The module need not be running, so lists can be set up before it is
// turned on.
func (k *Kernel) SetAllowed(ctx context.Context, module otogi.ModuleID, list, chat string, allowed bool, by string) error {
	if platform, id, ok := strings.Cut(chat, ":"); module == "" || list == "" || !ok || platform == "" || id == "" {
		return fmt.Errorf("set allowed: module %q, list %q, chat %q: want a module, a list, and <platform>:<conversation id>: %w", module, list, chat, otogi.ErrInvalid)
	}
	if k.cfg.Storage == nil {
		return errNoStorage
	}
	// One change at a time keeps the store and memory in step.
	k.listsMu.Lock()
	defer k.listsMu.Unlock()
	key := allowKey{module, list, chat}
	if _, ok := k.lists[key]; ok == allowed {
		return nil
	}
	now := time.Now()
	if err := k.cfg.Storage.SetAllowed(ctx, module, list, chat, allowed, now, by); err != nil {
		return err
	}
	if allowed {
		k.lists[key] = AllowedChat{Module: module, List: list, Chat: chat, At: now, By: by}
	} else {
		delete(k.lists, key)
	}
	k.log.Info("allowlist changed", "module", module, "list", list, "chat", chat, "allowed", allowed, "by", by)
	k.recordFor(ctx, module, otogi.Record{At: now, Kind: "allowlist.changed", Outcome: "ok", Payload: map[string]any{"list": list, "chat": chat, "allowed": allowed, "by": by}})
	return nil
}

// Allowlists returns every chat on every module's allowlists, by module,
// list, and chat.
func (k *Kernel) Allowlists() []AllowedChat {
	k.listsMu.RLock()
	out := make([]AllowedChat, 0, len(k.lists))
	for _, c := range k.lists {
		out = append(out, c)
	}
	k.listsMu.RUnlock()
	slices.SortFunc(out, func(a, b AllowedChat) int {
		return cmp.Or(cmp.Compare(a.Module, b.Module), cmp.Compare(a.List, b.List), cmp.Compare(a.Chat, b.Chat))
	})
	return out
}
