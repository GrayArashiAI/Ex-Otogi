package twitter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// userAgent names the bot: FxTwitter asks callers to identify themselves.
const userAgent = "ExOtogi/1 (+https://github.com/TundraWork/Ex-Otogi)"

// tweet is what a card shows, whichever source it came from.
type tweet struct {
	ID         string
	Author     author
	Text       string
	Truncated  bool // the source gave only the start of a long post
	Time       time.Time
	ReplyingTo string // the handle replied to
	Media      []media
	Sensitive  bool
	Quote      *tweet
	QuoteGone  string // why a quoted post cannot be shown
	Poll       *poll
	Note       string // a community note

	// Counters a source does not give are nil, and the card leaves them out.
	Replies, Reposts, Likes, Bookmarks, Views *int64
}

type author struct {
	Name, Handle, Avatar string
	Badge                string // "", "blue", "gold" (organizations), or "grey" (governments)
}

type media struct {
	Kind          string // photo, video, or gif
	Image         string // the photo, or the video's poster frame
	Width, Height int
	Seconds       float64 // a video's length
}

type poll struct {
	Choices []choice
	Votes   int64
	Ended   bool
}

type choice struct {
	Label   string
	Percent float64
}

// errGone is a post that cannot be shown: deleted, private, or suspended.
type errGone struct{ reason string }

func (e *errGone) Error() string { return "twitter: post unavailable: " + e.reason }

// client looks posts up through FxTwitter's API, and through X's own embed
// endpoint (the one embedded posts load) when FxTwitter fails.
type client struct {
	fx, syndication string // base URLs; tests point them elsewhere
	http            *http.Client
}

// status looks a post up. A post FxTwitter reports gone is gone; any other
// FxTwitter failure tries the embed endpoint, which lacks views, reposts,
// and the end of long posts. source names where the post came from.
func (c *client) status(ctx context.Context, id string) (t tweet, source string, err error) {
	t, fxErr := c.fxStatus(ctx, id)
	var gone *errGone
	switch {
	case fxErr == nil:
		return t, "fxtwitter", nil
	case errors.As(fxErr, &gone):
		return tweet{}, "fxtwitter", fxErr
	}
	t, synErr := c.syndicationStatus(ctx, id)
	if synErr != nil {
		return tweet{}, "", fmt.Errorf("twitter: %w; then %w", fxErr, synErr)
	}
	return t, "syndication", nil
}

func (c *client) get(ctx context.Context, url string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("twitter: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("twitter: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("twitter: read %s: %w", url, err)
	}
	return resp.StatusCode, data, nil
}

// fxStatus is FxTwitter's /2/status/{id}.
func (c *client) fxStatus(ctx context.Context, id string) (tweet, error) {
	code, data, err := c.get(ctx, c.fx+"/2/status/"+id)
	if err != nil {
		return tweet{}, err
	}
	var out struct {
		Code    int       `json:"code"`
		Message string    `json:"message"`
		Status  *fxStatus `json:"status"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return tweet{}, fmt.Errorf("fxtwitter: HTTP %d: %q", code, data)
	}
	switch {
	case code == http.StatusNotFound:
		return tweet{}, &errGone{reason: "not found or deleted"}
	case code == http.StatusUnauthorized:
		return tweet{}, &errGone{reason: "private"}
	case code != http.StatusOK || out.Status == nil:
		return tweet{}, fmt.Errorf("fxtwitter: HTTP %d: %s", code, data)
	}
	return out.Status.tweet(), nil
}

type fxStatus struct {
	Type    string `json:"type"`
	Reason  string `json:"reason"` // a tombstone's
	Message string `json:"message"`
	ID      string `json:"id"`
	Text    string `json:"text"`
	Author  struct {
		Name         string `json:"name"`
		ScreenName   string `json:"screen_name"`
		AvatarURL    string `json:"avatar_url"`
		Verification *struct {
			Verified bool   `json:"verified"`
			Type     string `json:"type"`
		} `json:"verification"`
	} `json:"author"`
	CreatedTimestamp  int64  `json:"created_timestamp"`
	Replies           *int64 `json:"replies"`
	Reposts           *int64 `json:"reposts"`
	Likes             *int64 `json:"likes"`
	Bookmarks         *int64 `json:"bookmarks"`
	Views             *int64 `json:"views"`
	PossiblySensitive bool   `json:"possibly_sensitive"`
	ReplyingTo        *struct {
		ScreenName string `json:"screen_name"`
	} `json:"replying_to"`
	Media *struct {
		All []struct {
			Type         string  `json:"type"`
			URL          string  `json:"url"`
			ThumbnailURL string  `json:"thumbnail_url"`
			Width        int     `json:"width"`
			Height       int     `json:"height"`
			Duration     float64 `json:"duration"`
		} `json:"all"`
	} `json:"media"`
	Quote *fxStatus `json:"quote"`
	Poll  *struct {
		Choices []struct {
			Label      string  `json:"label"`
			Percentage float64 `json:"percentage"`
		} `json:"choices"`
		TotalVotes int64  `json:"total_votes"`
		TimeLeftEn string `json:"time_left_en"`
	} `json:"poll"`
	CommunityNote *struct {
		Text string `json:"text"`
	} `json:"community_note"`
}

func (s *fxStatus) tweet() tweet {
	t := tweet{
		ID: s.ID, Text: s.Text, Time: time.Unix(s.CreatedTimestamp, 0),
		Author:    author{Name: s.Author.Name, Handle: s.Author.ScreenName, Avatar: s.Author.AvatarURL},
		Sensitive: s.PossiblySensitive,
		Replies:   s.Replies, Reposts: s.Reposts, Likes: s.Likes, Bookmarks: s.Bookmarks, Views: s.Views,
	}
	if v := s.Author.Verification; v != nil && v.Verified {
		t.Author.Badge = map[string]string{"organization": "gold", "government": "grey"}[v.Type]
		if t.Author.Badge == "" {
			t.Author.Badge = "blue"
		}
	}
	if s.ReplyingTo != nil {
		t.ReplyingTo = s.ReplyingTo.ScreenName
	}
	if s.Media != nil {
		for _, m := range s.Media.All {
			item := media{Kind: m.Type, Image: m.URL, Width: m.Width, Height: m.Height, Seconds: m.Duration}
			if m.Type == "video" || m.Type == "gif" {
				item.Image = m.ThumbnailURL
			}
			t.Media = append(t.Media, item)
		}
	}
	switch q := s.Quote; {
	case q == nil:
	case q.Type == "tombstone":
		t.QuoteGone = q.Message
		if t.QuoteGone == "" {
			t.QuoteGone = "这个帖子不可用。"
		}
	default:
		quote := q.tweet()
		t.Quote = &quote
	}
	if p := s.Poll; p != nil {
		t.Poll = &poll{Votes: p.TotalVotes, Ended: p.TimeLeftEn == "" || strings.Contains(p.TimeLeftEn, "Final")}
		for _, c := range p.Choices {
			t.Poll.Choices = append(t.Poll.Choices, choice{Label: c.Label, Percent: c.Percentage})
		}
	}
	if n := s.CommunityNote; n != nil {
		t.Note = n.Text
	}
	return t
}

// syndicationStatus is the endpoint X's embedded posts load.
func (c *client) syndicationStatus(ctx context.Context, id string) (tweet, error) {
	code, data, err := c.get(ctx, c.syndication+"/tweet-result?id="+id+"&token="+syndicationToken)
	if err != nil {
		return tweet{}, err
	}
	if code == http.StatusNotFound {
		return tweet{}, &errGone{reason: "not found"}
	}
	var s synStatus
	if err := json.Unmarshal(data, &s); code != http.StatusOK || err != nil {
		return tweet{}, fmt.Errorf("syndication: HTTP %d: %q", code, data)
	}
	switch {
	case s.Typename == "TweetTombstone":
		return tweet{}, &errGone{reason: s.Tombstone.Text.Text}
	case s.IDStr == "":
		return tweet{}, &errGone{reason: "not found"}
	}
	return s.tweet(), nil
}

// syndicationToken goes with every request to the embed endpoint. X's embed
// script derives it from the post ID, but the endpoint does not check it
// (as of 2026-11): any value works, and none at all returns nothing.
const syndicationToken = "a"

type synStatus struct {
	Typename  string `json:"__typename"`
	Tombstone struct {
		Text struct {
			Text string `json:"text"`
		} `json:"text"`
	} `json:"tombstone"`
	IDStr             string    `json:"id_str"`
	Text              string    `json:"text"`
	DisplayTextRange  []int     `json:"display_text_range"`
	CreatedAt         time.Time `json:"created_at"`
	FavoriteCount     *int64    `json:"favorite_count"`
	ConversationCount *int64    `json:"conversation_count"`
	PossiblySensitive bool      `json:"possibly_sensitive"`
	InReplyTo         string    `json:"in_reply_to_screen_name"`
	NoteTweet         *struct{} `json:"note_tweet"`
	User              struct {
		Name          string `json:"name"`
		ScreenName    string `json:"screen_name"`
		Image         string `json:"profile_image_url_https"`
		IsBlue        bool   `json:"is_blue_verified"`
		VerifiedType  string `json:"verified_type"`
		LegacyChecked bool   `json:"verified"`
	} `json:"user"`
	Entities struct {
		URLs []struct {
			URL         string `json:"url"`
			ExpandedURL string `json:"expanded_url"`
		} `json:"urls"`
		Media []struct {
			URL string `json:"url"`
		} `json:"media"`
	} `json:"entities"`
	MediaDetails []struct {
		Type          string `json:"type"` // photo, video, animated_gif
		MediaURLHTTPS string `json:"media_url_https"`
		OriginalInfo  struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"original_info"`
		VideoInfo *struct {
			DurationMillis float64 `json:"duration_millis"`
		} `json:"video_info"`
	} `json:"mediaDetails"`
	QuotedTweet *synStatus `json:"quoted_tweet"`
}

func (s *synStatus) tweet() tweet {
	text := s.Text
	for _, u := range s.Entities.URLs {
		text = strings.ReplaceAll(text, u.URL, u.ExpandedURL)
	}
	for _, m := range s.Entities.Media {
		text = strings.ReplaceAll(text, m.URL, "")
	}
	t := tweet{
		ID: s.IDStr, Text: strings.TrimSpace(text), Truncated: s.NoteTweet != nil, Time: s.CreatedAt,
		ReplyingTo: s.InReplyTo, Sensitive: s.PossiblySensitive, Likes: s.FavoriteCount, Replies: s.ConversationCount,
		Author: author{Name: s.User.Name, Handle: s.User.ScreenName, Avatar: strings.Replace(s.User.Image, "_normal.", "_200x200.", 1)},
	}
	switch {
	case s.User.VerifiedType == "Business":
		t.Author.Badge = "gold"
	case s.User.VerifiedType == "Government":
		t.Author.Badge = "grey"
	case s.User.IsBlue || s.User.LegacyChecked:
		t.Author.Badge = "blue"
	}
	for _, m := range s.MediaDetails {
		item := media{Kind: m.Type, Image: m.MediaURLHTTPS, Width: m.OriginalInfo.Width, Height: m.OriginalInfo.Height}
		if m.Type == "animated_gif" {
			item.Kind = "gif"
		}
		if m.VideoInfo != nil {
			item.Seconds = m.VideoInfo.DurationMillis / 1000
		}
		t.Media = append(t.Media, item)
	}
	if s.QuotedTweet != nil {
		q := s.QuotedTweet.tweet()
		t.Quote = &q
	}
	return t
}

// picture downloads an image. Photos come from X's media servers at a
// size: "medium" fits 1200 px, for a photo across the card (1440 px drawn);
// "small" fits 680 px, for grid cells and thumbnails.
func (c *client) picture(ctx context.Context, src, size string) ([]byte, string, error) {
	if src == "" {
		return nil, "", nil
	}
	u := src
	if size != "" && strings.Contains(u, "pbs.twimg.com/media/") {
		base, _, _ := strings.Cut(u, "?")
		format := "jpg"
		if i := strings.LastIndex(base, "."); i > strings.LastIndex(base, "/") {
			base, format = base[:i], base[i+1:]
		}
		u = base + "?format=" + format + "&name=" + size
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", fmt.Errorf("twitter: picture: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("twitter: picture: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("twitter: picture %s: %w", u, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("twitter: picture %s: HTTP %d", u, resp.StatusCode)
	}
	return data, resp.Header.Get("Content-Type"), nil
}
