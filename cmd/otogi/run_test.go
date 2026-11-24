package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestRunStopsCleanlyOnCancel(t *testing.T) {
	cfg, err := loadConfig(writeConfig(t, "data_dir: "+t.TempDir()+"\nmanagement: {addr: \"127.0.0.1:0\"}\n"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	running := make(chan struct{})
	log := slog.New(&signalHandler{Handler: slog.NewTextHandler(io.Discard, nil), message: "otogi: running", seen: running})
	done := make(chan error, 1)
	go func() { done <- run(ctx, cfg, log, new(slog.LevelVar)) }()
	<-running
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not stop")
	}
}

// signalHandler closes seen when a record with message is logged, and
// sends the address the management API says it listens on to addr, if set.
type signalHandler struct {
	slog.Handler
	message string
	seen    chan struct{}
	addr    chan string
	once    sync.Once
}

func (h *signalHandler) Handle(ctx context.Context, r slog.Record) error {
	if h.addr != nil && r.Message == "management: listening" {
		r.Attrs(func(a slog.Attr) bool {
			h.addr <- a.Value.String()
			return false
		})
	}
	if r.Message == h.message {
		h.once.Do(func() { close(h.seen) })
	}
	return h.Handler.Handle(ctx, r)
}

type fakeDriver struct{ applied, fetched int }

func (d *fakeDriver) Apply(context.Context, otogi.Effect) (otogi.EventRef, error) {
	d.applied++
	return otogi.EventRef{}, nil
}

func (d *fakeDriver) FetchMedia(context.Context, otogi.Destination, otogi.MediaSource) ([]byte, error) {
	d.fetched++
	return nil, nil
}

func TestRouterRoutesByPlatformAndAccount(t *testing.T) {
	main, other := &fakeDriver{}, &fakeDriver{}
	r := router{{"telegram", "main"}: main, {"telegram", "other"}: other}
	ctx := context.Background()
	if _, err := r.Apply(ctx, otogi.Effect{Kind: otogi.EffectSend, To: otogi.Destination{Platform: "telegram", Account: "main"}}); err != nil {
		t.Fatal(err)
	}
	// Non-send effects route by the message they apply to.
	if _, err := r.Apply(ctx, otogi.Effect{Kind: otogi.EffectDelete, Ref: otogi.EventRef{Platform: "telegram", Account: "other"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.FetchMedia(ctx, otogi.Destination{Platform: "telegram", Account: "main"}, otogi.MediaSource{}); err != nil {
		t.Fatal(err)
	}
	if main.applied != 1 || other.applied != 1 || main.fetched != 1 {
		t.Fatalf("main = %+v, other = %+v", main, other)
	}
	if _, err := r.Apply(ctx, otogi.Effect{Kind: otogi.EffectSend, To: otogi.Destination{Platform: "discord", Account: "main"}}); !errors.Is(err, otogi.ErrUnavailable) {
		t.Fatalf("unknown destination = %v", err)
	}
}

// The running process reads the general settings where it uses them: a
// saved event log retention shows in the status, and a saved log level is
// the logger's, with no restart.
func TestRunAppliesTheGeneralSettings(t *testing.T) {
	cfg, err := loadConfig(writeConfig(t, "data_dir: "+t.TempDir()+"\nmanagement: {addr: \"127.0.0.1:0\"}\neventlog: {retention: 24h}\n"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	running := make(chan struct{})
	addr := make(chan string, 1)
	level := new(slog.LevelVar)
	log := slog.New(&signalHandler{Handler: slog.NewTextHandler(io.Discard, nil), message: "otogi: running", seen: running, addr: addr})
	done := make(chan error, 1)
	go func() { done <- run(ctx, cfg, log, level) }()
	<-running
	api := "http://" + <-addr
	do := func(method, path, body string) string {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, method, api+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("If-Match", `"1"`)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		data, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("%s %s = %d %s", method, path, resp.StatusCode, data)
		}
		return string(data)
	}
	if got := do("GET", "/api/status", ""); !strings.Contains(got, `"retention_ms":86400000`) || level.Level() != slog.LevelInfo {
		t.Fatalf("with the file's settings: level %s, status %s", level.Level(), got)
	}
	do("PUT", "/api/system/general/settings", `{"eventlog_retention": "48h", "log_level": "debug"}`)
	if got := do("GET", "/api/status", ""); !strings.Contains(got, `"retention_ms":172800000`) || level.Level() != slog.LevelDebug {
		t.Fatalf("after saving: level %s, status %s", level.Level(), got)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run = %v, want nil", err)
	}
}
