package bilibili

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// fakeYtDlp stands in for yt-dlp: it notes its arguments, its cookie file's
// mode, lines, and path beside itself, and answers by the SESSDATA it was
// given and the part asked for.
const fakeYtDlp = `#!/bin/sh
dir=$(dirname "$0")
printf '%s\n' "$@" > "$dir/args"
jar=""; url=""
while [ $# -gt 0 ]; do
	case "$1" in
	--cookies) jar="$2"; shift ;;
	https://*) url="$1" ;;
	esac
	shift
done
printf '%s\n' "$jar" > "$dir/jar"
stat -c %a "$jar" > "$dir/mode"
grep -v '^#' "$jar" > "$dir/cookies"
sess=$(awk -F'\t' '$6 == "SESSDATA" { print $7 }' "$jar")
none='{"subtitles": {"danmaku": [{"ext": "xml", "url": "https://comment.bilibili.com/1.xml"}]}}'
case "$sess" in
slow) exec sleep 10 ;;
fail) printf '%s\n' "ERROR: [BiliBili] 1gm4a6yEor: This video may be deleted or geo-restricted." >&2; exit 1 ;;
dead) printf '%s\n' "WARNING: [BiliBili] 1gm4a6yEor: Subtitles are only available when logged in. Use --cookies" >&2
	printf '%s\n' "$none"; exit 0 ;;
esac
case "$url" in
*BV1gm4a6yEor\?p=1) printf '%s\n' '{"subtitles": {"danmaku": [{"ext": "xml", "url": "https://comment.bilibili.com/1.xml"}],
	"ai-zh": [{"ext": "srt", "data": "1\n00:00:00,000 --> 00:00:02,000\n自动生成的第一句\n"}],
	"zh-CN": [{"ext": "srt", "data": "1\n00:00:00,000 --> 00:00:02,000\n各位好\n\n2\n00:00:02,000 --> 00:00:04,000\n今天聊\n银河\n"}]}}' ;;
*BV1gm4a6yEor\?p=2) printf '%s\n' '{"subtitles": {"ai-zh": [{"ext": "srt", "data": "1\n00:00:00,000 --> 00:00:02,000\n自动生成的第一句\n"}]}}' ;;
*) printf '%s\n' "$none" ;;
esac
`

// withYtDlp gives m the fake yt-dlp and a login, and returns the directory
// it notes things in.
func withYtDlp(t *testing.T, m *Module, h *host, sessdata string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "yt-dlp")
	if err := os.WriteFile(path, []byte(fakeYtDlp), 0o700); err != nil {
		t.Fatal(err)
	}
	m.subtitles = &ytDlp{path: path, sessdata: sessdata, limit: ytDlpLimit}
	if err := m.Start(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	return dir
}

// tool is one of the tools the module offers, if it offers it.
func tool(h *host, name string) llm.Tool {
	for _, t := range h.services[llm.ToolsService("bilibili")].([]llm.Tool) {
		if t.Function().Name == name {
			return t
		}
	}
	return nil
}

// call runs one of the module's tools.
func call(t *testing.T, h *host, name, args string) (any, error) {
	t.Helper()
	found := tool(h, name)
	if found == nil {
		t.Fatalf("no tool %s", name)
	}
	return found.Call(context.Background(), otogi.Event{}, json.RawMessage(args))
}

func noted(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

// A video named by its ID, its av number, or a share link; its parts are
// listed; one that cannot be seen says so. Without yt-dlp and a login,
// there is no subtitles tool.
func TestVideoTool(t *testing.T) {
	t.Parallel()
	_, _, h := newTestModule(t, nil)
	for _, video := range []string{"BV1gm4a6yEor", "https://b23.tv/AbC1", "看 https://www.bilibili.com/video/BV1gm4a6yEor?p=2"} {
		got, err := call(t, h, "bilibili_video", `{"video": "`+video+`"}`)
		if err != nil {
			t.Fatalf("%s: %v", video, err)
		}
		v := got.(videoAnswer)
		if v.ID != "BV1gm4a6yEor" || v.Uploader != "AozakiArisu" || v.Length != "2:08" || v.Published != "2026-11-06 11:30 (UTC+8)" ||
			v.Views != 65306 || len(v.Parts) != 2 || v.Parts[1] != (partAnswer{Part: 2, Title: "正片", Length: "1:08"}) || v.Honors[0] != "热门收录" {
			t.Fatalf("%s: %+v", video, v)
		}
	}
	if _, err := call(t, h, "bilibili_video", `{"video": "BV1gone00000"}`); err == nil || !strings.Contains(err.Error(), "does not exist or cannot be seen") {
		t.Fatalf("gone: %v", err)
	}
	if _, err := call(t, h, "bilibili_video", `{"video": "the one about the galaxy"}`); err == nil || !strings.Contains(err.Error(), "want a BV ID") {
		t.Fatalf("not a video: %v", err)
	}
	if tool(h, "bilibili_subtitles") != nil {
		t.Fatal("subtitles offered without yt-dlp and a login")
	}
}

// Subtitles come from yt-dlp, given the login in a cookie file of its own,
// readable by the owner alone, removed after, and never on its command
// line: the uploader's Chinese track before Bilibili's, a part's own,
// numbers and times left out, cut at the limit.
func TestSubtitlesTool(t *testing.T) {
	t.Parallel()
	m, _, h := newTestModule(t, nil)
	dir := withYtDlp(t, m, h, "secret%2Cvalue")
	got, err := call(t, h, "bilibili_subtitles", `{"video": "https://b23.tv/AbC1"}`)
	if err != nil {
		t.Fatal(err)
	}
	if s := got.(subtitlesAnswer); s != (subtitlesAnswer{ID: "BV1gm4a6yEor", Part: 1, Language: "zh-CN", Text: "各位好\n今天聊 银河", Chars: 10}) {
		t.Fatalf("part 1: %+v", s)
	}
	if args := noted(t, dir, "args"); strings.Contains(args, "secret") || !strings.Contains(args, "--ignore-config\n--no-cache-dir\n--no-playlist\n--skip-download\n--write-subs\n--sub-langs\nall,-danmaku\n--cookies\n") ||
		!strings.HasSuffix(args, "-J\nhttps://www.bilibili.com/video/BV1gm4a6yEor?p=1") {
		t.Fatalf("arguments:\n%s", args)
	}
	if mode, cookies := noted(t, dir, "mode"), noted(t, dir, "cookies"); mode != "600" || cookies != ".bilibili.com\tTRUE\t/\tTRUE\t0\tSESSDATA\tsecret%2Cvalue" {
		t.Fatalf("cookie file: mode %s, %q", mode, cookies)
	}
	if _, err := os.Stat(noted(t, dir, "jar")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cookie file left behind: %v", err)
	}

	got, err = call(t, h, "bilibili_subtitles", `{"video": "BV1gm4a6yEor", "part": 2}`)
	if s, _ := got.(subtitlesAnswer); err != nil || s.Language != "ai-zh" || !s.Generated || s.Text != "自动生成的第一句" {
		t.Fatalf("part 2: %+v, %v", s, err)
	}
	if _, err := call(t, h, "bilibili_subtitles", `{"video": "BV1gm4a6yEor", "part": 3}`); err == nil || !strings.Contains(err.Error(), "has 2") {
		t.Fatalf("part 3: %v", err)
	}

	m.maxSubtitle = 4
	got, _ = call(t, h, "bilibili_subtitles", `{"video": "BV1gm4a6yEor"}`)
	if s := got.(subtitlesAnswer); s.Text != "各位好\n" || !s.Truncated || s.Chars != 10 {
		t.Fatalf("cut: %+v", s)
	}
}

// A video without subtitles, a login that has ended, yt-dlp failing, and
// yt-dlp taking too long each say so.
func TestSubtitlesWithoutThem(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		sessdata, video, want string
	}{
		{"secret", "av170001", "has no subtitles"},
		{"dead", "BV1gm4a6yEor", errLoginExpired.Error()},
		{"fail", "BV1gm4a6yEor", "yt-dlp: [BiliBili] 1gm4a6yEor: This video may be deleted or geo-restricted."},
		{"slow", "BV1gm4a6yEor", "yt-dlp gave no answer within 200ms"},
	} {
		m, _, h := newTestModule(t, nil)
		withYtDlp(t, m, h, c.sessdata)
		m.subtitles.limit = 200 * time.Millisecond
		if _, err := call(t, h, "bilibili_subtitles", `{"video": "`+c.video+`"}`); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.sessdata, err, c.want)
		}
	}
}

// Without Chrome, a card goes out in words, with the cover, and the record
// says so; no face is fetched for it.
func TestCardInWordsWithoutChrome(t *testing.T) {
	t.Parallel()
	m, fake, h := newTestModule(t, nil)
	inv := &invocation{}
	if err := m.Handle(context.Background(), message("group:G1", "bilibili.com/video/BV1gm4a6yEor"), inv); err != nil {
		t.Fatal(err)
	}
	if msg := inv.sent[0]; !strings.HasPrefix(msg.PlainText(), "🔥让银河燃烧吧🔥\n") || len(msg.Parts) != 2 {
		t.Fatalf("card %q", msg.PlainText())
	}
	if r := h.records[0]; r.Outcome != "ok" || r.Payload.(map[string]any)["in_words"] != true {
		t.Fatalf("record %+v", r)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	for _, r := range fake.requests {
		if strings.Contains(r, "/face.jpg") {
			t.Fatalf("face fetched for words: %s", r)
		}
	}
}
