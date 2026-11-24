package chat

import (
	"strings"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// sender is someone named in what a model gets: their sender ID, and the
// name the platform gives them (their ID when it gives none).
type sender struct{ id, shown string }

// known is what the people list says of who sent as actor on platform.
func (m *Module) known(platform otogi.PlatformID, actor string) (person, bool) {
	p, ok := m.people[string(platform)+":"+actor]
	return p, ok
}

// nameOf is how a model is told who sent a message: the name the people list
// gives them, then the name the platform shows where it differs, since
// others mention them by it. The transcript keeps the platform's names, so
// a changed list applies at once to the recent messages and judgments, which
// are read from it each time; a thread's turns keep the names they were sent
// with.
func (m *Module) nameOf(platform otogi.PlatformID, actor, shown string) string {
	p, ok := m.known(platform, actor)
	switch {
	case !ok || p.name == "" || p.name == shown:
		return shown
	case shown == "" || shown == actor: // the platform gives no name
		return p.name
	}
	return p.name + " (" + shown + ")"
}

// factor multiplies the judgment thresholds of actor's messages.
func (m *Module) factor(platform otogi.PlatformID, actor string) float64 {
	if p, ok := m.known(platform, actor); ok {
		return p.factor
	}
	return 1
}

// aboutPeople is the notes of the people named, in the order they are
// named and each once, for the model to know who they are; "" when none of
// them has one.
func (m *Module) aboutPeople(platform otogi.PlatformID, named []sender) string {
	var lines []string
	seen := map[string]bool{}
	for _, s := range named {
		if seen[s.id] {
			continue
		}
		seen[s.id] = true
		if p, ok := m.known(platform, s.id); ok && p.note != "" {
			lines = append(lines, "- "+m.nameOf(platform, s.id, s.shown)+": "+p.note)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "About people here:\n" + strings.Join(lines, "\n")
}
