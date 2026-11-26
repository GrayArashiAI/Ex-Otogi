package chat

import (
	"slices"
	"strings"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// sender is someone named in what a model gets: their sender ID, and the
// name the platform gives them (their ID when it gives none).
type sender struct{ id, shown, role string }

// known is what the people list says of who sent as actor on platform.
func (m *Module) known(platform otogi.PlatformID, actor string) (person, bool) {
	p, ok := m.people[string(platform)+":"+actor]
	return p, ok
}

// operatorMark follows the names of the bot's operators wherever a model is
// told who said what, so that nobody passes for one by taking their name:
// the module adds it by account, and takes it out of every name a platform
// gives (EXO-43).
const operatorMark = "⟦operator⟧"

// The marks after the name of a group's owner and its admins, added by the
// standing the platform gives with each message (EXO-58), and taken out of
// names as the operator mark is.
const (
	ownerMark = "⟦group owner⟧"
	adminMark = "⟦group admin⟧"
)

// roleMark is the mark for a sender's standing, "" for none.
func roleMark(role string) string {
	switch otogi.ActorRole(role) {
	case otogi.RoleOwner:
		return ownerMark
	case otogi.RoleAdmin:
		return adminMark
	}
	return ""
}

// withoutOperatorMark is s with no operator mark in it, nor half of one,
// so none of the marks between ⟦ and ⟧: what a name a platform gives, or
// anything else people wrote that goes where the marks are explained, must
// be before a model reads it.
func withoutOperatorMark(s string) string {
	s = strings.ReplaceAll(s, operatorMark, "")
	return strings.TrimSpace(strings.NewReplacer("⟦", "", "⟧", "").Replace(s))
}

// isOperator reports whether actor on platform is one of the bot's operators.
func (m *Module) isOperator(platform otogi.PlatformID, actor string) bool {
	return m.operators != nil && slices.Contains(m.operators(), string(platform)+":"+actor)
}

// nameOf is how a model is told who sent a message: the name the people list
// gives them, then the name the platform shows where it differs, since
// others mention them by it, the mark of the group owner or an admin by
// role, the standing the message was sent with, and the operator mark for an
// operator. The
// transcript keeps the platform's names, so a changed list applies at once
// to the recent messages and judgments, which are read from it each time; a
// thread's turns keep the names they were sent with.
func (m *Module) nameOf(platform otogi.PlatformID, actor, shown, role string) string {
	if shown = withoutOperatorMark(shown); shown == "" {
		shown = actor
	}
	name := shown
	if p, ok := m.known(platform, actor); ok && p.name != "" && p.name != shown {
		name = p.name
		if shown != actor { // the platform gives a name
			name += " (" + shown + ")"
		}
	}
	if mark := roleMark(role); mark != "" {
		name += " " + mark
	}
	if m.isOperator(platform, actor) {
		name += " " + operatorMark
	}
	return name
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
			lines = append(lines, "- "+m.nameOf(platform, s.id, s.shown, s.role)+": "+p.note)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "About people here:\n" + strings.Join(lines, "\n")
}
