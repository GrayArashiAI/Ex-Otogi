package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// revertActor is who saved a revision otogi settings revert stored.
const revertActor = "otogi settings revert"

// revert puts back the settings a section or a module had before its last
// change, for a process that does not start with the change: it stores the
// revision before the current one again, as the latest, as restoring it in
// the panel does, and says so on out. Run again it goes one revision further
// back each time, and after the first revision to the settings there were
// before anything was saved: none, and a module off. It is run with the
// process stopped, which starts with what it stored.
func (s *moduleSettings) revert(ctx context.Context, id otogi.ModuleID, out io.Writer) error {
	revisions, err := s.store.SettingsRevisions(ctx, id, math.MaxInt64, math.MaxInt32) // all, newest first
	if err != nil {
		return err
	}
	if len(revisions) == 0 {
		return fmt.Errorf("%s has no saved settings to revert: want a section, such as system:accounts, or a module's ID", id)
	}
	// Each revert before this one, with no other change since, stands for
	// one step back from the revision under them.
	reverts := 0
	for reverts < len(revisions) && revisions[reverts].By == revertActor {
		reverts++
	}
	cur := revisions[0]
	next := modulestore.SettingsRevision{Module: id, Revision: cur.Revision + 1, Settings: json.RawMessage("{}"), By: revertActor, At: time.Now()}
	what := "empty settings, as before the first were saved"
	switch back := 2*reverts + 1; {
	case back < len(revisions):
		next.Enabled, next.Settings = revisions[back].Enabled, revisions[back].Settings
		what = fmt.Sprintf("the settings of revision %d", revisions[back].Revision)
	case back > len(revisions):
		return fmt.Errorf("%s is back at the settings before the first were saved (revision %d): there is nothing earlier to revert to", id, cur.Revision)
	}
	if err := s.store.AddSettings(ctx, next); err != nil {
		return err
	}
	s.reconfigured(ctx, next, []otogi.ModuleID{})
	_, err = fmt.Fprintf(out, "%s: was at revision %d; revision %d now has %s. The bot starts with it.\n", id, cur.Revision, next.Revision, what)
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}
