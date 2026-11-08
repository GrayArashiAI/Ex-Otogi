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

var cardTemplate = template.Must(template.New("card").Funcs(cardimage.Funcs()).Funcs(template.FuncMap{"badge": badge}).Parse(cardHTML))

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
	Translation                     *translatedText // the post in Chinese, under the original, when it is not in Chinese
}

// translatedText is a post's text in Chinese, with who translated it from
// what.
type translatedText struct {
	By    string // 由 Gemini 翻译自英语
	Plain string // the text, as cut for the card
	Text  []span
	More  bool
}

type span struct {
	Text string
	Link bool // a link, mention, or hashtag, shown in blue
}

type grid struct {
	Items     []cell
	Sensitive bool
	Ratio     string // of a lone item: "w / h"
	Covered   bool   // each item under the cover sticker: the platform refused the pictures
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
	Translation               []span // in Chinese, when the post is translated
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

// cover puts the cover sticker over the card's pictures, the post's and
// its quote's, and reports whether it had any to cover.
func (c *card) cover() bool {
	covered := false
	for _, g := range []*grid{&c.Media, quoteMedia(c.Quote)} {
		if g != nil && len(g.Items) > 0 {
			g.Covered, covered = true, true
		}
	}
	return covered
}

func quoteMedia(q *quoted) *grid {
	if q == nil {
		return nil
	}
	return &q.Media
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

// icons are the action bar's, X's own, in currentColor.
var icons = map[string]template.HTML{
	"reply":    `<svg viewBox="0 0 24 24" fill="currentColor"><path d="M1.751 10c0-4.42 3.584-8 8.005-8h4.366c4.49 0 8.129 3.64 8.129 8.13 0 2.96-1.607 5.68-4.196 7.11l-8.054 4.46v-3.69h-.067c-4.49.1-8.183-3.51-8.183-8.01zm8.005-6c-3.317 0-6.005 2.69-6.005 6 0 3.37 2.77 6.08 6.138 6.01l.351-.01h1.761v2.3l5.087-2.81c1.951-1.08 3.163-3.13 3.163-5.36 0-3.39-2.744-6.13-6.129-6.13H9.756z"/></svg>`,
	"repost":   `<svg viewBox="0 0 24 24" fill="currentColor"><path d="M4.5 3.88l4.432 4.14-1.364 1.46L5.5 7.55V16c0 1.1.896 2 2 2H13v2H7.5c-2.209 0-4-1.79-4-4V7.55L1.432 9.48.068 8.02 4.5 3.88zM16.5 6H11V4h5.5c2.209 0 4 1.79 4 4v8.45l2.068-1.93 1.364 1.46-4.432 4.14-4.432-4.14 1.364-1.46 2.068 1.93V8c0-1.1-.896-2-2-2z"/></svg>`,
	"like":     `<svg viewBox="0 0 24 24" fill="currentColor"><path d="M16.697 5.5c-1.222-.06-2.679.51-3.89 2.16l-.805 1.09-.806-1.09C9.984 6.01 8.526 5.44 7.304 5.5c-1.243.07-2.349.78-2.91 1.91-.552 1.12-.633 2.78.479 4.82 1.074 1.97 3.257 4.27 7.129 6.61 3.87-2.34 6.052-4.64 7.126-6.61 1.111-2.04 1.03-3.7.477-4.82-.561-1.13-1.666-1.84-2.908-1.91zm4.187 7.69c-1.351 2.48-4.001 5.12-8.379 7.67l-.503.3-.504-.3c-4.379-2.55-7.029-5.19-8.382-7.67-1.36-2.5-1.41-4.86-.514-6.67.887-1.79 2.647-2.91 4.601-3.01 1.651-.09 3.368.56 4.798 2.01 1.429-1.45 3.146-2.1 4.796-2.01 1.954.1 3.714 1.22 4.601 3.01.896 1.81.846 4.17-.514 6.67z"/></svg>`,
	"bookmark": `<svg viewBox="0 0 24 24" fill="currentColor"><path d="M4 4.5C4 3.12 5.119 2 6.5 2h11C18.881 2 20 3.12 20 4.5v18.44l-8-5.71-8 5.71V4.5zM6.5 4c-.276 0-.5.22-.5.5v14.56l6-4.29 6 4.29V4.5c0-.28-.224-.5-.5-.5h-11z"/></svg>`,
}
