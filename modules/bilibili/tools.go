package bilibili

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// The tools agents get: a video's details, looked up as the cards look
// videos up, and its subtitles, through yt-dlp, when the module has it and
// a login.

// videoParam is the parameter that names a video, the same in each tool.
const videoParam = `"video": {"type": "string", "description": "the video: its BV ID (BV1xx411c7mD), its av number (av170001), or a link to it, bilibili.com/video/… or a b23.tv share link"}`

func (m *Module) tools() []llm.Tool {
	tools := []llm.Tool{llm.NewTool(llm.Function{
		Name:        "bilibili_video",
		Description: "Look up a Bilibili video: its title, uploader, when it was published, length, description, counts, and parts.",
		Parameters:  json.RawMessage(`{"type": "object", "properties": {` + videoParam + `}, "required": ["video"]}`),
	}, m.videoTool)}
	if m.subtitles != nil {
		tools = append(tools, llm.NewTool(llm.Function{
			Name:        "bilibili_subtitles",
			Description: "Read a Bilibili video's subtitles: the uploader's, or the ones Bilibili generates, one line each, without times. Not every video has them.",
			Parameters: json.RawMessage(`{"type": "object", "properties": {` + videoParam + `,
				"part": {"type": "integer", "minimum": 1, "description": "which part of a video with several; 1 when left out"}},
				"required": ["video"]}`),
		}, m.subtitlesTool))
	}
	return tools
}

// bareID is a video's ID alone.
var bareID = regexp.MustCompile(`(?i)^(?:BV[0-9A-Za-z]{10}|av\d+)$`)

// videoID is the video a tool's parameter names: an ID, or a link, which a
// share link's redirect is followed for.
func (m *Module) videoID(ctx context.Context, s string) (string, error) {
	if bareID.MatchString(s) {
		return normalize(s), nil
	}
	ms := matches(s)
	if len(ms) == 0 {
		return "", fmt.Errorf("video %q: want a BV ID (BV1xx411c7mD), an av number (av170001), or a link to the video", s)
	}
	if f := ms[0].found; f.id != "" {
		return f.id, nil
	}
	target, err := m.api.resolve(ctx, ms[0].short)
	if err != nil {
		return "", err
	}
	id, ok := idIn(target)
	if !ok {
		return "", fmt.Errorf("%s does not lead to a video", ms[0].short)
	}
	return id, nil
}

// lookUp is the video a tool's parameter names, with a plain error when it
// cannot be seen.
func (m *Module) lookUp(ctx context.Context, param string) (video, error) {
	id, err := m.videoID(ctx, param)
	if err != nil {
		return video{}, err
	}
	v, err := m.api.view(ctx, id)
	var apiErr *apiError
	if errors.As(err, &apiErr) && apiErr.gone() {
		return video{}, fmt.Errorf("video %s does not exist or cannot be seen (Bilibili code %d: %s)", id, apiErr.Code, apiErr.Message)
	}
	return v, err
}

// maxParts is how many parts of a video bilibili_video lists.
const maxParts = 50

type videoAnswer struct {
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	Uploader    string       `json:"uploader"`
	Published   string       `json:"published"`
	Length      string       `json:"length"`
	Description string       `json:"description,omitempty"`
	Views       int64        `json:"views"`
	Danmaku     int64        `json:"danmaku"`
	Likes       int64        `json:"likes"`
	Coins       int64        `json:"coins"`
	Favorites   int64        `json:"favorites"`
	Comments    int64        `json:"comments"`
	Shares      int64        `json:"shares"`
	Honors      []string     `json:"honors,omitempty"`
	Parts       []partAnswer `json:"parts,omitempty"`
	AllParts    int          `json:"all_parts,omitempty"`
}

type partAnswer struct {
	Part   int    `json:"part"`
	Title  string `json:"title"`
	Length string `json:"length"`
}

func (m *Module) videoTool(ctx context.Context, _ otogi.Event, args json.RawMessage) (any, error) {
	var in struct{ Video string }
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("arguments: %w", err)
	}
	v, err := m.lookUp(ctx, in.Video)
	if err != nil {
		return nil, err
	}
	out := videoAnswer{ID: v.BVID, Title: v.Title, Uploader: v.Owner.Name, Published: time.Unix(v.PubDate, 0).In(china).Format("2006-01-02 15:04") + " (UTC+8)",
		Length: duration(v.Duration), Description: v.Desc, Views: v.Stat.View, Danmaku: v.Stat.Danmaku, Likes: v.Stat.Like, Coins: v.Stat.Coin,
		Favorites: v.Stat.Favorite, Comments: v.Stat.Reply, Shares: v.Stat.Share}
	for _, h := range v.HonorReply.Honor {
		out.Honors = append(out.Honors, h.Desc)
	}
	if len(v.Pages) > 1 {
		for _, p := range v.Pages[:min(len(v.Pages), maxParts)] {
			out.Parts = append(out.Parts, partAnswer{Part: p.Page, Title: p.Part, Length: duration(p.Duration)})
		}
		if len(v.Pages) > maxParts {
			out.AllParts = len(v.Pages)
		}
	}
	return out, nil
}

// generated reports a track Bilibili made rather than the uploader.
func generated(lan string) bool { return strings.HasPrefix(lan, "ai-") }

// pick is the track to read: the uploader's Chinese, Bilibili's Chinese,
// or the first by language.
func pick(tracks map[string]string) string {
	lans := slices.Sorted(maps.Keys(tracks))
	for _, lan := range lans {
		if !generated(lan) && strings.HasPrefix(lan, "zh") {
			return lan
		}
	}
	for _, lan := range lans {
		if strings.HasPrefix(lan, "ai-zh") {
			return lan
		}
	}
	return lans[0]
}

type subtitlesAnswer struct {
	ID        string `json:"id"`
	Part      int    `json:"part"`
	Language  string `json:"language"`
	Generated bool   `json:"generated_by_bilibili"`
	Text      string `json:"text"`
	Chars     int    `json:"chars"`
	Truncated bool   `json:"truncated,omitempty"`
}

func (m *Module) subtitlesTool(ctx context.Context, _ otogi.Event, args json.RawMessage) (any, error) {
	var in struct {
		Video string
		Part  int
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("arguments: %w", err)
	}
	if in.Part == 0 {
		in.Part = 1
	}
	if in.Part < 1 {
		return nil, fmt.Errorf("part %d: parts count from 1", in.Part)
	}
	v, err := m.lookUp(ctx, in.Video)
	if err != nil {
		return nil, err
	}
	if parts := max(len(v.Pages), 1); in.Part > parts {
		return nil, fmt.Errorf("part %d: video %s has %d", in.Part, v.BVID, parts)
	}
	tracks, err := m.subtitles.tracks(ctx, v.BVID, in.Part)
	if err != nil {
		return nil, err
	}
	if len(tracks) == 0 {
		return nil, fmt.Errorf("video %s has no subtitles", v.BVID)
	}
	lan := pick(tracks)
	text := strings.Join(srtText(tracks[lan]), "\n")
	out := subtitlesAnswer{ID: v.BVID, Part: in.Part, Language: lan, Generated: generated(lan), Text: text, Chars: utf8.RuneCountInString(text)}
	if out.Chars > m.maxSubtitle {
		out.Text, out.Truncated = string([]rune(text)[:m.maxSubtitle]), true
	}
	return out, nil
}
