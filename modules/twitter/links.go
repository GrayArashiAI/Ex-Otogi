package twitter

import (
	"cmp"
	"regexp"
	"slices"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// statusLink matches a post's address on X and on the sites people paste
// instead to get previews: x.com/user/status/123, twitter.com (www., mobile.),
// fxtwitter.com, vxtwitter.com, fixupx.com, fixvx.com, and /i/web/status/123.
var statusLink = regexp.MustCompile(`(?i)(?:https?://)?(?:www\.|mobile\.)?(?:x|twitter|fxtwitter|vxtwitter|fixupx|fixvx)\.com/(?:[A-Za-z0-9_]{1,15}|i/web|i)/status(?:es)?/(\d{1,20})\b`)

// postsIn lists the first limit posts a message links, in the order they
// appear and without repeats: in its text, and behind formatted links.
func postsIn(msg otogi.Message, limit int) []string {
	type at struct {
		part, offset int
		id           string
	}
	var found []at
	for i, p := range msg.Parts {
		if p.Kind != otogi.PartText {
			continue
		}
		for _, m := range statusLink.FindAllStringSubmatchIndex(p.Text, -1) {
			found = append(found, at{i, m[0], p.Text[m[2]:m[3]]})
		}
		for _, a := range p.Annotations {
			if a.Kind == otogi.AnnotationLink {
				if m := statusLink.FindStringSubmatch(a.Target); m != nil {
					found = append(found, at{i, a.Offset, m[1]})
				}
			}
		}
	}
	slices.SortStableFunc(found, func(a, b at) int { return cmp.Or(a.part-b.part, a.offset-b.offset) })
	var ids []string
	for _, f := range found {
		if !slices.Contains(ids, f.id) && len(ids) < limit {
			ids = append(ids, f.id)
		}
	}
	return ids
}
