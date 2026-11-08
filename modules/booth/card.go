package booth

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"strconv"
	"strings"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/cardimage"
)

//go:embed card.html
var cardHTML string

// newTemplate parses the card's template, drawing the pictures it must not
// show plainly as p says.
func newTemplate(p cardimage.Pictures) *template.Template {
	return template.Must(template.New("card").Funcs(p.Funcs()).Parse(cardHTML))
}

// card is what the template shows of an item.
type card struct {
	Title, Shop, Category, Price, Published, Wishes, Desc, ID string
	Picture, Icon                                             template.URL // data: URIs
	Covered                                                   bool         // the picture under the cover sticker: the platform refused it
	Adult, SoldOut, EndOfSale                                 bool
	Options                                                   []option
	More                                                      int // options left out
	Tags                                                      []string
}

// option is one of the ways an item is sold, with its price.
type option struct{ Name, Price string }

// maxOptions and maxTags bound the lists a card shows.
const (
	maxOptions = 4
	maxTags    = 6
)

// newCard lays out it with its picture and its shop's icon, already
// downloaded.
func newCard(it item, picture, icon image) card {
	c := card{
		Title: it.Name, Shop: it.Shop.Name, Category: category(it), Price: price(it), Wishes: strconv.FormatInt(it.Wishes, 10),
		Desc: lines(it.Description), ID: strconv.FormatInt(it.ID, 10),
		Picture: picture.dataURI(), Icon: icon.dataURI(),
		Adult: it.Adult, SoldOut: it.SoldOut, EndOfSale: it.EndOfSale,
	}
	if !it.PublishedAt.IsZero() {
		c.Published = it.PublishedAt.In(japan).Format("2006-01-02") + " 发布"
	}
	if len(it.Variations) > 1 {
		for _, v := range it.Variations {
			if len(c.Options) == maxOptions {
				c.More++
				continue
			}
			c.Options = append(c.Options, option{v.Name, yen(v.Price)})
		}
	}
	for _, t := range it.Tags {
		if len(c.Tags) < maxTags {
			c.Tags = append(c.Tags, t.Name)
		}
	}
	return c
}

// category is the item's category under its parent: 3Dモデル › 3D装飾品.
func category(it item) string {
	if it.Category.Parent.Name == "" {
		return it.Category.Name
	}
	return it.Category.Parent.Name + " › " + it.Category.Name
}

// price is what the item costs: its options' range, or the page's price.
func price(it item) string {
	if len(it.Variations) == 0 {
		return it.Price
	}
	low, high := it.Variations[0].Price, it.Variations[0].Price
	for _, v := range it.Variations {
		low, high = min(low, v.Price), max(high, v.Price)
	}
	if low == high {
		return yen(low)
	}
	return yen(low) + " – " + yen(high)
}

// yen writes a price as Booth does, with free said so: ¥1,200, 免费.
func yen(n int64) string {
	if n == 0 {
		return "免费"
	}
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return "¥" + s
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

// japan is where Booth's times are meant to be read.
var japan = time.FixedZone("JST", 9*60*60)

// image is downloaded image data.
type image struct {
	data []byte
	mime string
}

func (i image) dataURI() template.URL { return cardimage.DataURI(i.data, i.mime) }

// render draws a card as a PNG.
func render(ctx context.Context, r cardimage.Renderer, t *template.Template, c card) ([]byte, error) {
	var html bytes.Buffer
	if err := t.Execute(&html, c); err != nil {
		return nil, fmt.Errorf("booth: card template: %w", err)
	}
	return r.Render(ctx, html.String(), ".card", cardWidth, cardScale)
}

// The card is laid out at the width chat apps show an image, cardWidth CSS
// pixels (as .card sets), and drawn at cardScale for sharp text: 1440 pixels
// wide.
const (
	cardWidth = 480
	cardScale = 3
)
