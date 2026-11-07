package modulestore

import (
	"context"
	"fmt"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore/queries"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Allowlists calls fn for every chat on every module's allowlists.
func (s *Storage) Allowlists(ctx context.Context, fn func(module otogi.ModuleID, list, chat string, at time.Time, by string)) error {
	rows, err := queries.New(s.db).Allowlists(ctx)
	if err != nil {
		return fmt.Errorf("module store: allowlists: %w", err)
	}
	for _, r := range rows {
		fn(otogi.ModuleID(r.Module), r.List, r.Chat, time.UnixMilli(r.AddedAt), r.AddedBy)
	}
	return nil
}

// SetAllowed adds a chat to a module's allowlist, keeping when and by whom
// it was first added, or removes it.
func (s *Storage) SetAllowed(ctx context.Context, module otogi.ModuleID, list, chat string, allowed bool, at time.Time, by string) error {
	q := queries.New(s.db)
	var err error
	if allowed {
		err = q.Allow(ctx, queries.AllowParams{Module: string(module), List: list, Chat: chat, AddedAt: at.UnixMilli(), AddedBy: by})
	} else {
		err = q.Disallow(ctx, queries.DisallowParams{Module: string(module), List: list, Chat: chat})
	}
	if err != nil {
		return fmt.Errorf("module store: set allowed %s/%s %s: %w", module, list, chat, err)
	}
	return nil
}

// BotReceivers calls fn for every module that receives bots' messages,
// whose "ignore bots" switch is off.
func (s *Storage) BotReceivers(ctx context.Context, fn func(module otogi.ModuleID)) error {
	modules, err := queries.New(s.db).BotReceivers(ctx)
	if err != nil {
		return fmt.Errorf("module store: bot receivers: %w", err)
	}
	for _, m := range modules {
		fn(otogi.ModuleID(m))
	}
	return nil
}

// SetIgnoreBots turns a module's "ignore bots" switch on or off.
func (s *Storage) SetIgnoreBots(ctx context.Context, module otogi.ModuleID, ignore bool, at time.Time, by string) error {
	q := queries.New(s.db)
	var err error
	if ignore {
		err = q.IgnoreBots(ctx, string(module))
	} else {
		err = q.ReceiveBots(ctx, queries.ReceiveBotsParams{Module: string(module), ChangedAt: at.UnixMilli(), ChangedBy: by})
	}
	if err != nil {
		return fmt.Errorf("module store: set ignore bots %s: %w", module, err)
	}
	return nil
}
