package twitter

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/TundraWork/Ex-Otogi/pkg/cardimage"
)

//go:embed card.html
var cardHTML string

var cardTemplate = template.Must(template.New("card").Funcs(template.FuncMap{"badge": badge}).Parse(cardHTML))

// The card is laid out at the width chat apps show an image and drawn at
// 3x, like X's own post page on a phone.
const (
	cardWidth = 480
	cardScale = 3
)

// card is what the template shows of a post, its pictures inlined.
type card struct {
	Name, Handle, Badge, ReplyingTo string
	Avatar                          template.URL
	Text                            []span
	More                            bool // the text goes on beyond what the card shows
	Media                           grid
	Quote                           *quoted
	QuoteGone, Note                 string
	Poll                            *pollView
	When, Views                     string
	Actions                         []action
	Translated                      string // who translated the text from what, on a translated card
}

type span struct {
	Text string
	Link bool // a link, mention, or hashtag, shown in blue
}

type grid struct {
	Items     []cell
	Sensitive bool
	Ratio     string // of a lone item: "w / h"
}

type cell struct {
	Image      template.URL
	Video, GIF bool
	Length     string
}

type quoted struct {
	Name, Handle, Badge, Date string
	Avatar                    template.URL
	Text                      []span
	Media                     grid
}

type pollView struct {
	Choices []pollChoice
	Votes   string
	State   string
}

type pollChoice struct {
	Label, Percent string
	Width          float64
	Top            bool
}

type action struct {
	Icon  template.HTML
	Value string
}

// pictures are a post's downloaded images, by URL.
type pictures map[string]template.URL

// newCard lays a post out as X shows it, times read in zone.
func newCard(t tweet, pics pictures, zone *time.Location) card {
	text, more := excerpt(t.Text, maxText)
	c := card{
		Name: t.Author.Name, Handle: t.Author.Handle, Badge: t.Author.Badge, Avatar: pics[t.Author.Avatar],
		ReplyingTo: t.ReplyingTo, Text: spans(text), More: more || t.Truncated,
		Media: newGrid(t.Media, t.Sensitive, pics), QuoteGone: t.QuoteGone, Note: t.Note,
		When: clock(t.Time.In(zone)),
		Actions: []action{
			{icons["reply"], counter(t.Replies)},
			{icons["repost"], counter(t.Reposts)},
			{icons["like"], counter(t.Likes)},
			{icons["bookmark"], counter(t.Bookmarks)},
			{icons["share"], ""},
		},
	}
	if t.Views != nil {
		c.Views = count(*t.Views)
	}
	if q := t.Quote; q != nil {
		qtext, _ := excerpt(q.Text, maxQuoteText)
		c.Quote = &quoted{
			Name: q.Author.Name, Handle: q.Author.Handle, Badge: q.Author.Badge, Avatar: pics[q.Author.Avatar],
			Date: day(q.Time.In(zone), t.Time.In(zone)), Text: spans(qtext), Media: newGrid(q.Media, q.Sensitive, pics),
		}
	}
	if p := t.Poll; p != nil {
		c.Poll = &pollView{Votes: count(p.Votes), State: "最终结果"}
		if !p.Ended {
			c.Poll.State = "投票进行中"
		}
		best := 0.0
		for _, ch := range p.Choices {
			best = math.Max(best, ch.Percent)
		}
		for _, ch := range p.Choices {
			c.Poll.Choices = append(c.Poll.Choices, pollChoice{Label: ch.Label, Percent: strconv.FormatFloat(ch.Percent, 'f', -1, 64) + "%", Width: ch.Percent, Top: p.Ended && ch.Percent == best})
		}
	}
	return c
}

// How much text a card shows: a long post is cut where X's timeline would
// offer "Show more", and a quoted one shorter.
const (
	maxText      = 560
	maxQuoteText = 280
)

// excerpt is text cut to about limit characters at a line or word break,
// and whether it was cut.
func excerpt(text string, limit int) (string, bool) {
	if utf8.RuneCountInString(text) <= limit {
		return text, false
	}
	cut := string([]rune(text)[:limit])
	if i := strings.LastIndexAny(cut, "\n "); i > len(cut)*3/4 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " \n") + "…", true
}

var linkish = regexp.MustCompile(`https?://\S+|@[A-Za-z0-9_]{1,15}|#[\p{L}\p{N}_]+`)

// spans splits text into plain runs and the links, mentions, and hashtags
// X shows in blue. Links show as X writes them: no scheme or www., cut
// after 26 characters.
func spans(text string) []span {
	var out []span
	last := 0
	for _, loc := range linkish.FindAllStringIndex(text, -1) {
		if loc[0] > last {
			out = append(out, span{Text: text[last:loc[0]]})
		}
		s := text[loc[0]:loc[1]]
		if strings.HasPrefix(s, "http") {
			s = strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://"), "www.")
			if r := []rune(s); len(r) > 26 {
				s = string(r[:26]) + "…"
			}
		}
		out = append(out, span{Text: s, Link: true})
		last = loc[1]
	}
	if last < len(text) {
		out = append(out, span{Text: text[last:]})
	}
	return out
}

func newGrid(items []media, sensitive bool, pics pictures) grid {
	g := grid{Sensitive: sensitive, Ratio: "16 / 9"}
	for _, m := range items[:min(len(items), 4)] {
		c := cell{Image: pics[m.Image], Video: m.Kind == "video", GIF: m.Kind == "gif"}
		if c.Video && m.Seconds > 0 {
			c.Length = length(m.Seconds)
		}
		g.Items = append(g.Items, c)
	}
	if len(items) == 1 && items[0].Width > 0 && items[0].Height > 0 {
		// X shows a lone item at its own shape, within limits.
		r := math.Min(math.Max(float64(items[0].Width)/float64(items[0].Height), 0.75), 2)
		g.Ratio = strconv.FormatFloat(r, 'f', 3, 64)
	}
	return g
}

// clock is a post's time as X's Chinese interface writes it: 下午3:04 ·
// 2026年11月6日.
func clock(t time.Time) string {
	half, hour := "上午", t.Hour()
	if hour >= 12 {
		half = "下午"
	}
	if hour %= 12; hour == 0 {
		hour = 12
	}
	return fmt.Sprintf("%s%d:%02d · %d年%d月%d日", half, hour, t.Minute(), t.Year(), t.Month(), t.Day())
}

// day is a quoted post's date: 11月6日, with the year when it differs from
// the quoting post's.
func day(t, from time.Time) string {
	if t.Year() != from.Year() {
		return fmt.Sprintf("%d年%d月%d日", t.Year(), t.Month(), t.Day())
	}
	return fmt.Sprintf("%d月%d日", t.Month(), t.Day())
}

// length writes a video's seconds as 0:41 or 1:02:05.
func length(seconds float64) string {
	s := int64(math.Round(seconds))
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// counter writes a counter, or nothing when the source did not give it.
func counter(n *int64) string {
	if n == nil {
		return ""
	}
	return count(*n)
}

// count writes a number as X's Chinese interface does: 1,234, 1.2万, 3.4亿.
func count(n int64) string {
	switch {
	case n >= 100_000_000:
		return trim(float64(n)/100_000_000) + "亿"
	case n >= 10_000:
		return trim(float64(n)/10_000) + "万"
	case n >= 1000:
		return strconv.FormatInt(n/1000, 10) + "," + fmt.Sprintf("%03d", n%1000)
	}
	return strconv.FormatInt(n, 10)
}

func trim(f float64) string {
	return strconv.FormatFloat(float64(int64(f*10))/10, 'f', -1, 64)
}

// render draws a card as a PNG.
func render(ctx context.Context, r cardimage.Renderer, c card) ([]byte, error) {
	var html bytes.Buffer
	if err := cardTemplate.Execute(&html, c); err != nil {
		return nil, fmt.Errorf("twitter: card template: %w", err)
	}
	return r.Render(ctx, html.String(), ".card", cardWidth, cardScale)
}

// badge is the verification mark beside a name, in its kind's color.
func badge(kind string) template.HTML {
	color := map[string]string{"blue": "#1d9bf0", "gold": "#e2b719", "grey": "#829aab"}[kind]
	if color == "" {
		return ""
	}
	return template.HTML(`<svg class="badge" viewBox="0 0 22 22" fill="` + color + `"><path d="M20.4 11c0-1.3-.8-2.5-2-3.1.4-1.3.2-2.7-.8-3.6-.9-1-2.3-1.2-3.6-.8C13.4 2.3 12.3 1.5 11 1.5S8.6 2.3 8 3.5c-1.3-.4-2.7-.2-3.6.8-1 .9-1.2 2.3-.8 3.6-1.2.6-2 1.8-2 3.1s.8 2.5 2 3.1c-.4 1.3-.2 2.7.8 3.6.9 1 2.3 1.2 3.6.8.6 1.2 1.8 2 3.1 2s2.5-.8 3.1-2c1.3.4 2.7.2 3.6-.8 1-.9 1.2-2.3.8-3.6 1.2-.6 2-1.8 2-3.1zm-10.7 4.1-3.4-3.4 1.3-1.3 2.1 2.1 4.6-5 1.4 1.3-6 6.3z"/></svg>`)
}

// icons are the action bar's, in currentColor.
var icons = map[string]template.HTML{
	"reply":    `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round"><path d="M9.5 4h5a6.5 6.5 0 0 1 0 13H12l-4.5 3.5V16.6A6.5 6.5 0 0 1 9.5 4z"/></svg>`,
	"repost":   `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M4.5 9.5 7.5 6.5l3 3M7.5 7v7.5a2.5 2.5 0 0 0 2.5 2.5h4M19.5 14.5l-3 3-3-3M16.5 17V9.5A2.5 2.5 0 0 0 14 7h-4"/></svg>`,
	"like":     `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round"><path d="M12 20s-7.5-4.6-7.5-10.1A4.2 4.2 0 0 1 12 7.4a4.2 4.2 0 0 1 7.5 2.5C19.5 15.4 12 20 12 20z"/></svg>`,
	"bookmark": `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round"><path d="M6.5 4.5h11v15.5L12 16.2 6.5 20z"/></svg>`,
	"share":    `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 15V4M8 7.5 12 3.5l4 4M5 13v5.5A1.5 1.5 0 0 0 6.5 20h11a1.5 1.5 0 0 0 1.5-1.5V13"/></svg>`,
}
