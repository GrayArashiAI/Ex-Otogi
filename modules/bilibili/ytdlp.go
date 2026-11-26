package bilibili

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Subtitles come through yt-dlp, a program the deployment installs: it
// knows how Bilibili wants its subtitles asked for, logged in, which this
// repository leaves to it. It gets the login as a cookie file only the
// service user can read, removed once it exits, never on its command line.

// ytDlpLimit is how long one run may take; it takes about 2 s.
const ytDlpLimit = time.Minute

// errLoginExpired is a tool's answer when Bilibili gives subtitles only to
// the logged in, though a login was sent.
var errLoginExpired = errors.New("the Bilibili login (sessdata) has stopped working: Bilibili gives subtitles only when logged in; someone needs to set a fresh SESSDATA in the control panel")

// ytDlp runs yt-dlp with a Bilibili login.
type ytDlp struct {
	path     string
	sessdata string
	limit    time.Duration
}

// tracks are a video part's subtitle tracks, SRT by language (zh-CN,
// ai-zh, …), without the danmaku.
func (y ytDlp) tracks(ctx context.Context, bvid string, part int) (map[string]string, error) {
	jar, err := os.CreateTemp("", "otogi-bilibili-*.cookies") // 0600
	if err != nil {
		return nil, fmt.Errorf("bilibili: cookie file: %w", err)
	}
	defer func() { _ = os.Remove(jar.Name()) }()
	_, err = fmt.Fprintf(jar, "# Netscape HTTP Cookie File\n.bilibili.com\tTRUE\t/\tTRUE\t0\tSESSDATA\t%s\n", y.sessdata)
	if err = errors.Join(err, jar.Close()); err != nil {
		return nil, fmt.Errorf("bilibili: cookie file: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, y.limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, y.path, "--ignore-config", "--no-cache-dir", "--no-playlist", "--skip-download",
		"--write-subs", "--sub-langs", "all,-danmaku", "--cookies", jar.Name(), "-J",
		fmt.Sprintf("https://www.bilibili.com/video/%s?p=%d", bvid, part))
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return nil, fmt.Errorf("yt-dlp gave no answer within %s", y.limit)
	case err != nil:
		return nil, fmt.Errorf("yt-dlp: %s", lastError(stderr.String(), err))
	}
	var info struct {
		Subtitles map[string][]struct {
			Ext  string `json:"ext"`
			Data string `json:"data"`
		} `json:"subtitles"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &info); err != nil {
		return nil, fmt.Errorf("yt-dlp: read its answer: %w", err)
	}
	out := map[string]string{}
	for lan, formats := range info.Subtitles {
		for _, f := range formats {
			if lan != "danmaku" && f.Ext == "srt" && f.Data != "" {
				out[lan] = f.Data
			}
		}
	}
	if len(out) == 0 && strings.Contains(stderr.String(), "only available when logged in") {
		return nil, errLoginExpired
	}
	return out, nil
}

// lastError is the last ERROR: line yt-dlp wrote, or err when it wrote none.
func lastError(stderr string, err error) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line, ok := strings.CutPrefix(strings.TrimSpace(lines[i]), "ERROR: "); ok {
			return line
		}
	}
	return err.Error()
}

// srtText is an SRT track's lines, without numbers and times.
func srtText(srt string) []string {
	var out []string
	for _, block := range strings.Split(strings.ReplaceAll(srt, "\r\n", "\n"), "\n\n") {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		// A block is its number, its times, and its text.
		if len(lines) >= 3 && strings.Contains(lines[1], "-->") {
			out = append(out, strings.Join(lines[2:], " "))
		}
	}
	return out
}
