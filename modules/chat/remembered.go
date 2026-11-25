package chat

// An agent with a memory block reads its whole memory of a chat in the
// system prompt of each of its replies there, after its own prompt and
// before what says where it is and when: a book is small (max_chars), so
// nothing is picked, and the prompt starts the same while the book does,
// which a provider's prompt cache can reuse.

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// rememberedHeader leads the memory in a reply's system prompt.
const rememberedHeader = "What you remember of this chat from before, each with the date it was noted. It may be out of date: where someone now says otherwise, go by what they say. Do not recite it."

// remembered is p's memory of the chat to as a reply's system prompt shows
// it, "" when there is none, and its size for the reply's record. Someone
// whose name now differs from the name noted with an entry about them is
// told at the end: their name in the people list, or the one they last
// sent a message there under.
func (m *Module) remembered(ctx context.Context, p *profile, to otogi.Destination) (string, map[string]int, error) {
	book, err := m.store.book(ctx, p.Name, to)
	if err != nil {
		return "", nil, err
	}
	size := bookSize(book)
	if len(book) == 0 {
		return "", size, nil
	}
	lines := make([]string, 0, len(book))
	noted := map[[2]string]bool{} // actor and a name noted for them
	var actors []string
	for _, e := range book {
		// The memory is the model's words on what people wrote, which may
		// carry the operator mark: only names the bot gives may show it.
		lines = append(lines, fmt.Sprintf("- [%s] %s", e.Created.In(time.Local).Format("2006-01-02"), withoutOperatorMark(e.Text)))
		for id, name := range e.People {
			if name != "" && !noted[[2]string{id, name}] {
				noted[[2]string{id, name}] = true
				actors = append(actors, id)
			}
		}
	}
	slices.Sort(actors)
	shown, err := m.store.shownNames(ctx, to, slices.Compact(actors))
	if err != nil {
		return "", nil, err
	}
	// The operator mark alone is no new name: an entry noted before
	// someone became an operator, or stopped being one, names them still.
	bare := func(name string) string { return strings.TrimSuffix(name, " "+operatorMark) }
	var renamed []string
	for k := range noted {
		s, seen := shown[k[0]]
		if who, ok := m.known(to.Platform, k[0]); !seen && (!ok || who.name == "") {
			continue // nothing says what they are called now
		}
		now := m.nameOf(to.Platform, k[0], s)
		if bare(now) == bare(k[1]) {
			continue
		}
		// With no message left in the chat, only the list's half of the name
		// is known now: "Captain (Bob)" noted is no other name than "Captain".
		if old := bare(k[1]); !seen && strings.HasPrefix(old, bare(now)+" (") && strings.HasSuffix(old, ")") {
			continue
		}
		renamed = append(renamed, k[1]+" is now "+now)
	}
	text := rememberedHeader + "\n" + strings.Join(lines, "\n")
	if len(renamed) > 0 {
		slices.Sort(renamed)
		text += "\nNames noted then that differ now: " + strings.Join(renamed, "; ") + "."
	}
	return text, size, nil
}
