package bilibili

import (
	"regexp"
	"slices"
	"strings"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

var (
	// videoLink matches a video page: bilibili.com/video/BV1xx411c7mD or
	// /video/av170001, on www., m., or no subdomain.
	videoLink = regexp.MustCompile(`(?i)(?:https?://)?(?:www\.|m\.)?bilibili\.com/video/(BV[0-9A-Za-z]{10}|av\d+)\b`)
	// shortLink matches a share link, which redirects to the page.
	shortLink = regexp.MustCompile(`(?i)(?:https?://)?(?:b23\.tv|bili2233\.cn)/([0-9A-Za-z]+)\b`)
)

// maxVideos is how many videos of one message get a card: a message pasting
// a list is not asking for a wall of cards.
const maxVideos = 3

// found is a video named in a message: its ID, or a short link to resolve.
type found struct {
	id    string // BV… or av…, empty until a short link is resolved
	short string // https://b23.tv/…
}

// videosIn lists the videos a message names, in order and without
// repeats: in its text, and behind formatted links.
func videosIn(msg otogi.Message) []found {
	var texts []string
	for _, p := range msg.Parts {
		if p.Kind != otogi.PartText {
			continue
		}
		texts = append(texts, p.Text)
		for _, a := range p.Annotations {
			if a.Kind == otogi.AnnotationLink && a.Target != "" {
				texts = append(texts, a.Target)
			}
		}
	}
	var out []found
	seen := map[string]bool{}
	for _, text := range texts {
		for _, m := range matches(text) {
			key := strings.ToLower(m.id + m.short)
			if seen[key] || len(out) == maxVideos {
				continue
			}
			seen[key] = true
			out = append(out, m)
		}
	}
	return out
}

// matches finds video and share links in text, in the order they appear.
func matches(text string) []found {
	type at struct {
		pos int
		f   found
	}
	var all []at
	for _, m := range videoLink.FindAllStringSubmatchIndex(text, -1) {
		all = append(all, at{m[0], found{id: normalize(text[m[2]:m[3]])}})
	}
	for _, m := range shortLink.FindAllStringSubmatchIndex(text, -1) {
		code := text[m[2]:m[3]]
		f := found{short: "https://b23.tv/" + code}
		if strings.HasPrefix(strings.ToUpper(code), "BV") && len(code) == 12 {
			f = found{id: normalize(code)} // b23.tv/BV… names the video itself
		}
		all = append(all, at{m[0], f})
	}
	slices.SortFunc(all, func(a, b at) int { return a.pos - b.pos })
	out := make([]found, len(all))
	for i, a := range all {
		out[i] = a.f
	}
	return out
}

// normalize writes a video ID as Bilibili does: BV with its case kept (it
// is case-sensitive after the prefix), av in lower case.
func normalize(id string) string {
	if strings.HasPrefix(strings.ToLower(id), "av") {
		return strings.ToLower(id)
	}
	return "BV" + id[2:]
}

// idIn finds the video a resolved short link points to.
func idIn(u string) (string, bool) {
	m := videoLink.FindStringSubmatch(u)
	if m == nil {
		return "", false
	}
	return normalize(m[1]), true
}
