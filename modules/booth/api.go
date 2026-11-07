package booth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"
)

// userAgent is a browser's, as Booth's pages expect.
const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36"

// item is what the module uses of an item page's JSON (/ja/items/<id>.json).
type item struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Price       string    `json:"price"` // as the page shows it: "¥ 100"
	PublishedAt time.Time `json:"published_at"`
	Adult       bool      `json:"is_adult"`
	SoldOut     bool      `json:"is_sold_out"`
	EndOfSale   bool      `json:"is_end_of_sale"`
	Wishes      int64     `json:"wish_lists_count"`
	Category    struct {
		Name   string `json:"name"`
		Parent struct {
			Name string `json:"name"`
		} `json:"parent"`
	} `json:"category"`
	Images []struct {
		Original string `json:"original"`
	} `json:"images"`
	Shop struct {
		Name      string `json:"name"`
		Thumbnail string `json:"thumbnail_url"`
	} `json:"shop"`
	Tags []struct {
		Name string `json:"name"`
	} `json:"tags"`
	Variations []variation `json:"variations"`
}

// variation is one of the ways an item is sold.
type variation struct {
	Name  string `json:"name"`
	Price int64  `json:"price"`
}

// preview is the item's picture for the card: the first image of its
// gallery that does not move. The gallery holds pictures only (videos are
// embeds in the description); a GIF may be animated, so it is passed over.
func (it item) preview() string {
	for _, img := range it.Images {
		switch strings.ToLower(path.Ext(img.Original)) {
		case ".jpg", ".jpeg", ".png", ".webp":
			return img.Original
		}
	}
	return ""
}

// icon is the shop's icon at a size sharp on a card drawn at 3x; Booth
// gives it at 48×48 and also serves 128×128.
func (it item) icon() string {
	return strings.Replace(it.Shop.Thumbnail, "/c/48x48/", "/c/128x128/", 1)
}

// errGone is an item that does not exist or is no longer public.
var errGone = errors.New("booth: no such item")

// client reads Booth's public item pages.
type client struct {
	site string // https://booth.pm; tests point it elsewhere
	http *http.Client
}

// item looks an item up. The adult cookie is the age confirmation Booth's
// pages ask for once.
func (c *client) item(ctx context.Context, id string) (item, error) {
	data, _, err := c.get(ctx, c.site+"/ja/items/"+id+".json", "adult=t")
	var status *statusError
	switch {
	case errors.As(err, &status) && status.code == http.StatusNotFound:
		return item{}, errGone
	case err != nil:
		return item{}, err
	}
	var it item
	if err := json.Unmarshal(data, &it); err != nil {
		return item{}, fmt.Errorf("booth: decode item %s: %w", id, err)
	}
	return it, nil
}

// image downloads a picture.
func (c *client) image(ctx context.Context, u string) (image, error) {
	if u == "" {
		return image{}, nil
	}
	data, mime, err := c.get(ctx, u, "")
	return image{data: data, mime: mime}, err
}

func (c *client) get(ctx context.Context, u, cookie string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", fmt.Errorf("booth: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", "https://booth.pm/")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("booth: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("booth: read %s: %w", u, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("booth: %s: %w", u, &statusError{code: resp.StatusCode, body: string(data)})
	}
	return data, resp.Header.Get("Content-Type"), nil
}

// statusError is an HTTP status other than 200.
type statusError struct {
	code int
	body string
}

func (e *statusError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.code, e.body) }
