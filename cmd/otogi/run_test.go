package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
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
	go func() {
		_, err := run(ctx, cfg, func(string) *slog.Logger { return log }, new(slog.LevelVar))
		done <- err
	}()
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
	go func() {
		_, err := run(ctx, cfg, func(string) *slog.Logger { return log }, level)
		done <- err
	}()
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

// started is a process started by run, and how it ended.
type started struct {
	api     string
	format  string // the log format run asked for
	restart bool
	err     error
	done    chan struct{}
}

// startRun runs the process with cfg until it says it is running.
func startRun(t *testing.T, cfg Config) *started {
	t.Helper()
	p := &started{done: make(chan struct{})}
	running := make(chan struct{})
	addr := make(chan string, 1)
	log := slog.New(&signalHandler{Handler: slog.NewTextHandler(io.Discard, nil), message: "otogi: running", seen: running, addr: addr})
	go func() {
		defer close(p.done)
		p.restart, p.err = run(context.Background(), cfg, func(format string) *slog.Logger {
			p.format = format
			return log
		}, new(slog.LevelVar))
	}()
	select {
	case <-running:
	case <-p.done:
		t.Fatalf("run ended before it was running: %v", p.err)
	}
	p.api = "http://" + <-addr
	return p
}

// do makes a request to the process's management API.
func (p *started) do(t *testing.T, method, path, ifMatch, body string, wantCode int) string {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), method, p.api+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantCode {
		t.Fatalf("%s %s = %d %s, want %d", method, path, resp.StatusCode, data, wantCode)
	}
	return string(data)
}

// The process settings saved while the process runs change nothing in it,
// and the API says a restart is pending. POST /api/restart stops it as a
// requested restart, and the next start runs with them; POST /api/shutdown
// stops it as a stop.
func TestRestartAppliesTheProcessSettings(t *testing.T) {
	cfg, err := loadConfig(writeConfig(t, "data_dir: "+t.TempDir()+"\nmanagement: {addr: \"127.0.0.1:0\"}\nkernel: {workers: 4}\nlog: {format: text}\n"))
	if err != nil {
		t.Fatal(err)
	}
	p := startRun(t, cfg)
	if got := p.do(t, "GET", "/api/status", "", "", 200); p.format != "text" || !strings.Contains(got, `"workers":4`) || !strings.Contains(got, `"handler_timeout_ms":600000`) {
		t.Fatalf("with the file's settings: log format %q, status %s", p.format, got)
	}
	p.do(t, "PUT", "/api/system/process/settings", `"1"`, `{"workers": 6, "handler_timeout": "90s", "log_format": "json"}`, 200)
	if got := p.do(t, "GET", "/api/status", "", "", 200); !strings.Contains(got, `"workers":4`) || !strings.Contains(got, `"handler_timeout_ms":600000`) {
		t.Fatalf("after saving, the running process: %s", got)
	}
	if got := p.do(t, "GET", "/api/system", "", "", 200); !strings.Contains(got, `{"id":"process","revision":2,"applies":"restart","running":1}`) {
		t.Fatalf("after saving, the sections: %s", got)
	}
	p.do(t, "POST", "/api/restart", "", "", 202)
	<-p.done
	if !p.restart || p.err != nil {
		t.Fatalf("after POST /api/restart: restart %t, %v", p.restart, p.err)
	}

	p = startRun(t, cfg)
	if got := p.do(t, "GET", "/api/status", "", "", 200); p.format != "json" || !strings.Contains(got, `"workers":6`) || !strings.Contains(got, `"handler_timeout_ms":90000`) {
		t.Fatalf("after the restart: log format %q, status %s", p.format, got)
	}
	if got := p.do(t, "GET", "/api/system", "", "", 200); !strings.Contains(got, `{"id":"process","revision":2,"applies":"restart","running":2}`) {
		t.Fatalf("after the restart, the sections: %s", got)
	}
	states := p.do(t, "GET", "/api/events?kind=process.state", "", "", 200)
	if strings.Count(states, `"restart_requested_by":"management API"`) != 2 || !strings.Contains(states, `"state":"stopped"`) {
		t.Fatalf("the restart's records: %s", states)
	}
	p.do(t, "POST", "/api/shutdown", "", "", 202)
	<-p.done
	if p.restart || p.err != nil {
		t.Fatalf("after POST /api/shutdown: restart %t, %v", p.restart, p.err)
	}
}

// The exit code is the supervisor's signal: 75 after POST /api/restart,
// which its "restart on non-zero exit" answers, and 0 after POST
// /api/shutdown, which stays stopped.
func TestExitCodes(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	config := writeConfig(t, "data_dir: "+t.TempDir()+"\nmanagement: {addr: \""+addr+"\"}\n")
	for path, want := range map[string]int{"/api/restart": 75, "/api/shutdown": 0} {
		code := make(chan int, 1)
		go func() { code <- mainExit([]string{"-config", config}) }()
		deadline := time.Now().Add(10 * time.Second)
		for {
			req, _ := http.NewRequestWithContext(context.Background(), "POST", "http://"+addr+path, nil)
			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode != 202 {
					t.Fatalf("POST %s = %d", path, resp.StatusCode)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("POST %s: %v", path, err)
			}
			time.Sleep(20 * time.Millisecond)
		}
		if got := <-code; got != want {
			t.Fatalf("exit code after POST %s = %d, want %d", path, got, want)
		}
	}
}
