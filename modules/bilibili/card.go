package bilibili

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

//go:embed card.html
var cardHTML string

var cardTemplate = template.Must(template.New("card").Parse(cardHTML))

// card is what the template shows of a video.
type card struct {
	Title, Owner, Published, Duration, Desc, ID string
	Cover, Face                                 template.URL // data: URIs
	Parts                                       int
	Honors                                      []string
	Stats                                       []stat
}

type stat struct {
	Icon         template.HTML
	Value, Label string
	Hot          bool // the counters people react with, in the accent color
}

// newCard lays out v with its cover and the uploader's face, already
// downloaded.
func newCard(v video, cover, face image) card {
	c := card{
		Title: v.Title, Owner: v.Owner.Name, Desc: lines(v.Desc), ID: v.BVID, Parts: v.Videos,
		Published: time.Unix(v.PubDate, 0).In(china).Format("2006-01-02 15:04") + " 发布",
		Duration:  duration(v.Duration),
		Cover:     cover.dataURI(), Face: face.dataURI(),
		Stats: []stat{
			{icons["view"], count(v.Stat.View), "播放", false},
			{icons["danmaku"], count(v.Stat.Danmaku), "弹幕", false},
			{icons["reply"], count(v.Stat.Reply), "评论", false},
			{icons["share"], count(v.Stat.Share), "分享", false},
			{icons["like"], count(v.Stat.Like), "点赞", true},
			{icons["coin"], count(v.Stat.Coin), "投币", true},
			{icons["favorite"], count(v.Stat.Favorite), "收藏", true},
		},
	}
	for _, h := range v.HonorReply.Honor {
		c.Honors = append(c.Honors, h.Desc)
	}
	return c
}

// lines is text without its blank lines, which a three-line excerpt cannot
// spare.
func lines(text string) string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// china is where Bilibili's times are meant to be read.
var china = time.FixedZone("CST", 8*60*60)

// count writes a counter the way Bilibili does: 9999, 1.2万, 3.4亿.
func count(n int64) string {
	switch {
	case n >= 100_000_000:
		return trim(float64(n)/100_000_000) + "亿"
	case n >= 10_000:
		return trim(float64(n)/10_000) + "万"
	}
	return strconv.FormatInt(n, 10)
}

func trim(f float64) string {
	return strconv.FormatFloat(float64(int64(f*10))/10, 'f', -1, 64)
}

// duration writes seconds as 2:08 or 1:02:08.
func duration(s int64) string {
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// image is downloaded image data.
type image struct {
	data []byte
	mime string
}

func (i image) dataURI() template.URL {
	if len(i.data) == 0 {
		return ""
	}
	mime := i.mime
	if mime == "" {
		mime = http.DetectContentType(i.data)
	}
	return template.URL("data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(i.data))
}

// renderer draws cards with chrome-headless-shell, started for each card:
// a few cards a day do not justify a browser kept in memory.
type renderer struct {
	chrome string
}

// render returns the card as a PNG at twice its CSS size.
func (r renderer) render(ctx context.Context, c card) ([]byte, error) {
	var html bytes.Buffer
	if err := cardTemplate.Execute(&html, c); err != nil {
		return nil, fmt.Errorf("bilibili: card template: %w", err)
	}
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(r.chrome),
		// The page is our own template with its images inline; Chrome needs
		// no network, so it gets none. Without the network the sandbox
		// (unavailable to a service user on Ubuntu 24.04) guards nothing
		// the template does not already.
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("host-resolver-rules", "MAP * ~NOTFOUND"),
		chromedp.Flag("hide-scrollbars", true),
	)
	allocCtx, cancel := chromedp.NewExecAllocator(ctx, opts...)
	defer cancel()
	tab, cancelTab := chromedp.NewContext(allocCtx)
	defer cancelTab()
	setContent := chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		tree, err := cdp.Call(ctx, t, page.GetFrameTree, cdp.Empty{})
		if err != nil {
			return err
		}
		_, err = cdp.Call(ctx, t, page.SetDocumentContent, page.SetDocumentContentParams{FrameID: tree.FrameTree.Frame.ID, HTML: html.String()})
		return err
	})
	ready := chromedp.Evaluate[bool](`document.fonts.ready.then(() => Promise.all([...document.images].map(i => i.decode().catch(() => {})))).then(() => true)`, chromedp.EvalAwaitPromise)
	if err := chromedp.Do(tab, chromedp.EmulateViewport(cardWidth, 600, chromedp.EmulateScale(2)), chromedp.Navigate("about:blank"), setContent); err != nil {
		return nil, fmt.Errorf("bilibili: render: %w", err)
	}
	if _, err := chromedp.Run(tab, ready); err != nil {
		return nil, fmt.Errorf("bilibili: render: wait for fonts and images: %w", err)
	}
	png, err := chromedp.Run(tab, chromedp.Screenshot(chromedp.CSS(".frame")))
	if err != nil {
		return nil, fmt.Errorf("bilibili: render: screenshot: %w", err)
	}
	return png, nil
}

// cardWidth is the card's CSS width, which .frame sets too.
const cardWidth = 720

// icons are the counters' symbols, drawn in currentColor.
var icons = map[string]template.HTML{
	"view":     `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round"><rect x="3" y="5" width="18" height="14" rx="3.5"/><path d="M10.2 9.4v5.2l4.4-2.6z" fill="currentColor" stroke-width="1.2"/></svg>`,
	"danmaku":  `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><rect x="3" y="5" width="18" height="14" rx="3.5"/><path d="M7 10h4M13.5 10H17M7 14h7.5"/></svg>`,
	"reply":    `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round"><path d="M4 6.5A2.5 2.5 0 0 1 6.5 4h11A2.5 2.5 0 0 1 20 6.5v8a2.5 2.5 0 0 1-2.5 2.5H11l-4.2 3.2V17h-.3A2.5 2.5 0 0 1 4 14.5z"/></svg>`,
	"share":    `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M13 5l7 6.5-7 6.5v-3.7c-4.4 0-7.2 1.4-9 4.2.5-5.2 3.4-8.6 9-9.2z"/></svg>`,
	"like":     `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round"><path d="M8 10.5V20H5a1 1 0 0 1-1-1v-7.5a1 1 0 0 1 1-1zM8 10.5l3.6-6.2a1.7 1.7 0 0 1 3.1 1.2L14 9.5h4.6a2 2 0 0 1 2 2.4l-1.3 6.5a2 2 0 0 1-2 1.6H8"/></svg>`,
	"coin":     `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><circle cx="12" cy="12" r="8.5"/><path d="M9 9.5h6M12 9.5V16M9.5 13h5" stroke-linecap="round"/></svg>`,
	"favorite": `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round"><path d="M12 3.8l2.5 5.1 5.6.8-4 4 .9 5.6-5-2.7-5 2.7.9-5.6-4-4 5.6-.8z"/></svg>`,
}
