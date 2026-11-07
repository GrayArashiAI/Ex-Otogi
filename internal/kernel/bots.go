package kernel

import (
	"context"
	"fmt"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Every module has an "ignore bots" switch, on unless the operator turns
// it off: a module ignoring bots receives no event whose sender is a bot
// account (otogi.Actor.Bot). Modules answering links or commands should
// not answer another bot.

// loadBotReceivers reads which modules receive bots' messages.
func (k *Kernel) loadBotReceivers(ctx context.Context) error {
	if k.cfg.Storage == nil {
		return nil
	}
	receivers := map[otogi.ModuleID]bool{}
	if err := k.cfg.Storage.BotReceivers(ctx, func(m otogi.ModuleID) { receivers[m] = true }); err != nil {
		return fmt.Errorf("load bot receivers: %w", err)
	}
	k.botsMu.Lock()
	k.botReceivers = receivers
	k.botsMu.Unlock()
	return nil
}

// IgnoresBots reports whether module's "ignore bots" switch is on.
func (k *Kernel) IgnoresBots(module otogi.ModuleID) bool {
	k.botsMu.RLock()
	defer k.botsMu.RUnlock()
	return !k.botReceivers[module]
}

// SetIgnoreBots turns module's "ignore bots" switch on or off, at once, and
// records the change as bots.changed with who made it. Setting what is
// already so changes nothing. The module need not be running.
func (k *Kernel) SetIgnoreBots(ctx context.Context, module otogi.ModuleID, ignore bool, by string) error {
	if module == "" {
		return fmt.Errorf("set ignore bots: no module: %w", otogi.ErrInvalid)
	}
	if k.cfg.Storage == nil {
		return errNoStorage
	}
	// One change at a time keeps the store and memory in step.
	k.botsMu.Lock()
	defer k.botsMu.Unlock()
	if !k.botReceivers[module] == ignore {
		return nil
	}
	now := time.Now()
	if err := k.cfg.Storage.SetIgnoreBots(ctx, module, ignore, now, by); err != nil {
		return err
	}
	if ignore {
		delete(k.botReceivers, module)
	} else {
		k.botReceivers[module] = true
	}
	k.log.Info("bots switch changed", "module", module, "ignore_bots", ignore, "by", by)
	k.recordFor(ctx, module, otogi.Record{At: now, Kind: "bots.changed", Outcome: "ok", Payload: map[string]any{"ignore_bots": ignore, "by": by}})
	return nil
}
