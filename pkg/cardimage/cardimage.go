// Package cardimage draws HTML cards as PNG images with chrome-headless-shell.
// Chrome starts for each image and exits after it: modules draw a few cards
// a day, which does not justify a browser kept in memory.
//
// A card is a page the module builds itself: values escaped by html/template
// and every image inlined with DataURI. Chrome gets no network, so a card
// cannot load anything, and runs without its sandbox, which a service user
// cannot have on Ubuntu 24.04 and which would guard only against hostile
// web content.
package cardimage

import (
	"context"
	_ "embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"net/http"
	"os"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// Renderer draws cards with the chrome-headless-shell binary at Chrome.
type Renderer struct {
	Chrome string
}

// Check reports whether path is an executable, for validating a module's
// configuration before anything starts.
func Check(path string) error {
	if path == "" {
		return fmt.Errorf("the path of chrome-headless-shell is required")
	}
	info, err := os.Stat(path)
	switch {
	case err != nil:
		return fmt.Errorf("chrome: %w", err)
	case info.IsDir() || info.Mode()&0o111 == 0:
		return fmt.Errorf("chrome: %s is not an executable", path)
	}
	return nil
}

// Render lays html out width CSS pixels wide, and returns the first element
// matching the CSS selector as a PNG drawn at scale: a card laid out at the
// width chat apps show images (about 480 px) and drawn at 3x is read at the
// sizes its styles set. Render waits for fonts and images before drawing.
func (r Renderer) Render(ctx context.Context, html, selector string, width int64, scale float64) ([]byte, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(r.Chrome),
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
		_, err = cdp.Call(ctx, t, page.SetDocumentContent, page.SetDocumentContentParams{FrameID: tree.FrameTree.Frame.ID, HTML: html})
		return err
	})
	if err := chromedp.Do(tab, chromedp.EmulateViewport(width, 600, chromedp.EmulateScale(scale)), chromedp.Navigate("about:blank"), setContent); err != nil {
		return nil, fmt.Errorf("cardimage: load: %w", err)
	}
	ready := chromedp.Evaluate[bool](`document.fonts.ready.then(() => Promise.all([...document.images].map(i => i.decode().catch(() => {})))).then(() => true)`, chromedp.EvalAwaitPromise)
	if _, err := chromedp.Run(tab, ready); err != nil {
		return nil, fmt.Errorf("cardimage: wait for fonts and images: %w", err)
	}
	png, err := chromedp.Run(tab, chromedp.Screenshot(chromedp.CSS(selector)))
	if err != nil {
		return nil, fmt.Errorf("cardimage: draw %s: %w", selector, err)
	}
	return png, nil
}

// DataURI inlines an image for a card; mime is sniffed when empty. Empty
// data gives an empty URI, which a template can test for.
func DataURI(data []byte, mime string) template.URL {
	if len(data) == 0 {
		return ""
	}
	if mime == "" {
		mime = http.DetectContentType(data)
	}
	return template.URL("data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data))
}

//go:embed cover.webp
var cover []byte

// Cover is the sticker a card puts over its pictures, blurred, when the
// platform refused the card for what they show (otogi.ErrRefused).
func Cover() template.URL { return DataURI(cover, "image/webp") }
