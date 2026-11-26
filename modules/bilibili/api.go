package bilibili

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// userAgent is a browser's: Bilibili's risk control refuses others.
const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36"

// video is what the module uses of x/web-interface/view.
type video struct {
	BVID     string `json:"bvid"`
	AID      int64  `json:"aid"`
	Title    string `json:"title"`
	Pic      string `json:"pic"`
	Desc     string `json:"desc"`
	Duration int64  `json:"duration"`
	PubDate  int64  `json:"pubdate"`
	Videos   int    `json:"videos"`
	Pages    []page `json:"pages"`
	Owner    struct {
		Name string `json:"name"`
		Face string `json:"face"`
	} `json:"owner"`
	Stat struct {
		View, Danmaku, Reply, Favorite, Coin, Share, Like int64
	} `json:"stat"`
	HonorReply struct {
		Honor []struct {
			Desc string `json:"desc"`
		} `json:"honor"`
	} `json:"honor_reply"`
}

// page is one part of a video with several.
type page struct {
	Page     int    `json:"page"`
	Part     string `json:"part"`
	Duration int64  `json:"duration"`
}

// apiError is a reply Bilibili gave with a non-zero code.
type apiError struct {
	Code    int
	Message string
}

func (e *apiError) Error() string { return fmt.Sprintf("bilibili: code %d: %s", e.Code, e.Message) }

// gone reports a video that does not exist or cannot be seen: deleted,
// private, or under review.
func (e *apiError) gone() bool {
	switch e.Code {
	case -404, 62002, 62004, 62012:
		return true
	}
	return false
}

// client calls Bilibili's web API as a visitor. Bilibili refuses visitors
// without the device cookies its own pages get first, from finger/spi.
type client struct {
	api    string // https://api.bilibili.com; tests point it elsewhere
	http   *http.Client
	now    func() time.Time
	mu     sync.Mutex
	cookie string
	got    time.Time
}

// cookieAge is how long the device cookies are reused before fresh ones.
const cookieAge = 24 * time.Hour

func (c *client) cookies(ctx context.Context, fresh bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cookie != "" && !fresh && c.now().Sub(c.got) < cookieAge {
		return c.cookie, nil
	}
	var out struct {
		Data struct {
			B3 string `json:"b_3"`
			B4 string `json:"b_4"`
		} `json:"data"`
	}
	if err := c.get(ctx, c.api+"/x/frontend/finger/spi", "", &out); err != nil {
		return "", fmt.Errorf("bilibili: device cookies: %w", err)
	}
	c.cookie, c.got = "buvid3="+out.Data.B3+"; buvid4="+url.QueryEscape(out.Data.B4), c.now()
	return c.cookie, nil
}

// view looks a video up by its BV ID or av number. Refused as a suspicious
// visitor, it gets fresh cookies and tries once more.
func (c *client) view(ctx context.Context, id string) (video, error) {
	q := url.Values{}
	if aid, ok := strings.CutPrefix(strings.ToLower(id), "av"); ok {
		q.Set("aid", aid)
	} else {
		q.Set("bvid", id)
	}
	for attempt := 0; ; attempt++ {
		cookie, err := c.cookies(ctx, attempt > 0)
		if err != nil {
			return video{}, err
		}
		var out struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Data    video  `json:"data"`
		}
		err = c.get(ctx, c.api+"/x/web-interface/view?"+q.Encode(), cookie, &out)
		var status *statusError
		switch {
		case errors.As(err, &status) && status.code == http.StatusPreconditionFailed && attempt == 0,
			err == nil && (out.Code == -412 || out.Code == -352) && attempt == 0:
			continue
		case err != nil:
			return video{}, err
		case out.Code != 0:
			return video{}, &apiError{Code: out.Code, Message: out.Message}
		}
		return out.Data, nil
	}
}

// statusError is an HTTP status other than 200.
type statusError struct {
	code int
	body string
}

func (e *statusError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.code, e.body) }

func (c *client) get(ctx context.Context, u, cookie string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("bilibili: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", "https://www.bilibili.com/")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("bilibili: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("bilibili: read %s: %w", u, err)
	}
	if resp.StatusCode != http.StatusOK {
		return &statusError{code: resp.StatusCode, body: string(data)}
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("bilibili: decode %s: %w", u, err)
	}
	return nil
}

// image downloads a picture from Bilibili's image servers at a size,
// letting them scale it: "@960w_540h_1c.webp" is 960×540, cropped, WebP.
func (c *client) image(ctx context.Context, src, size string) (image, error) {
	u := strings.Replace(src, "http://", "https://", 1) + size
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return image{}, fmt.Errorf("bilibili: image: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", "https://www.bilibili.com/")
	resp, err := c.http.Do(req)
	if err != nil {
		return image{}, fmt.Errorf("bilibili: image: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return image{}, fmt.Errorf("bilibili: image %s: %w", u, err)
	}
	if resp.StatusCode != http.StatusOK {
		return image{}, fmt.Errorf("bilibili: image %s: HTTP %d", u, resp.StatusCode)
	}
	return image{data: data, mime: resp.Header.Get("Content-Type")}, nil
}

// resolve follows a short link (b23.tv) to where it points, without
// fetching that page.
func (c *client) resolve(ctx context.Context, short string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, short, nil)
	if err != nil {
		return "", fmt.Errorf("bilibili: resolve %s: %w", short, err)
	}
	req.Header.Set("User-Agent", userAgent)
	noFollow := *c.http
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := noFollow.Do(req)
	if err != nil {
		return "", fmt.Errorf("bilibili: resolve %s: %w", short, err)
	}
	_ = resp.Body.Close()
	location := resp.Header.Get("Location")
	if resp.StatusCode < 300 || resp.StatusCode >= 400 || location == "" {
		return "", fmt.Errorf("bilibili: resolve %s: HTTP %d without a redirect", short, resp.StatusCode)
	}
	return location, nil
}
