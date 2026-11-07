package booth

import (
	"cmp"
	"regexp"
	"slices"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// itemLink matches an item page: booth.pm/ja/items/9111596, booth.pm/items/…,
// or a shop's own pyrite9.booth.pm/items/….
var itemLink = regexp.MustCompile(`(?i)(?:https?://)?(?:[a-z0-9-]+\.)?booth\.pm/(?:[a-z]{2}(?:-[a-z]{2})?/)?items/(\d+)\b`)

// maxItems is how many items of one message get a card: a message pasting
// a list is not asking for a wall of cards.
const maxItems = 3

// itemsIn lists the item IDs a message names, in the order they appear and
// without repeats: in its text, and behind formatted links.
func itemsIn(msg otogi.Message) []string {
	type at struct {
		part, offset int
		id           string
	}
	var all []at
	for i, p := range msg.Parts {
		if p.Kind != otogi.PartText {
			continue
		}
		for _, m := range itemLink.FindAllStringSubmatchIndex(p.Text, -1) {
			all = append(all, at{i, m[0], p.Text[m[2]:m[3]]})
		}
		for _, a := range p.Annotations {
			if a.Kind == otogi.AnnotationLink {
				if m := itemLink.FindStringSubmatch(a.Target); m != nil {
					all = append(all, at{i, a.Offset, m[1]})
				}
			}
		}
	}
	slices.SortStableFunc(all, func(a, b at) int { return cmp.Or(a.part-b.part, a.offset-b.offset) })
	var out []string
	for _, a := range all {
		if !slices.Contains(out, a.id) && len(out) < maxItems {
			out = append(out, a.id)
		}
	}
	return out
}
