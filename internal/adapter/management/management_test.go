package management

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func openLog(t *testing.T) *EventLog {
	t.Helper()
	l, err := OpenEventLog(context.Background(), filepath.Join(t.TempDir(), "eventlog.sqlite"), quiet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

// seed writes n records alternating between two conversations; record i has
// kind "k<i%2>" and payload {"text": "msg <i>"}.
func seed(t *testing.T, l *EventLog, n int, at time.Time) {
	t.Helper()
	for i := range n {
		l.Record(context.Background(), otogi.Record{
			At: at.Add(time.Duration(i) * time.Second), Kind: []string{"k0", "k1"}[i%2], Component: "kernel",
			Event: otogi.EventID("e" + string(rune('a'+i%3))), Conversation: otogi.ConversationID([]string{"c0", "c1"}[i%2]),
			Payload: map[string]string{"text": "msg " + string(rune('A'+i))},
		})
	}
}

func ids(p Page) []int64 {
	out := make([]int64, len(p.Records))
	for i, r := range p.Records {
		out[i] = r.ID
	}
	return out
}

func TestQueryFiltersAndCursors(t *testing.T) {
	l := openLog(t)
	ctx := context.Background()
	base := time.Unix(1000, 0)
	seed(t, l, 6, base)

	latest, err := l.Query(ctx, Query{Limit: 2})
	if err != nil || len(latest.Records) != 2 || latest.Records[0].ID != 6 || !latest.More || latest.OldestRetained != 1 {
		t.Fatalf("latest = %v more=%v oldest=%d err=%v", ids(latest), latest.More, latest.OldestRetained, err)
	}
	older, _ := l.Query(ctx, Query{Before: 5, Limit: 10})
	if got := ids(older); len(got) != 4 || got[0] != 4 || got[3] != 1 || older.More {
		t.Fatalf("older = %v more=%v", got, older.More)
	}
	newer, _ := l.Query(ctx, Query{After: 2, Limit: 2}) // the two right after the cursor, newest first
	if got := ids(newer); len(got) != 2 || got[0] != 4 || got[1] != 3 || !newer.More {
		t.Fatalf("newer = %v more=%v", got, newer.More)
	}
	for name, test := range map[string]struct {
		q    Query
		want int
	}{
		"kind":            {Query{Kind: "k1"}, 3},
		"conversation":    {Query{Conversation: "c0"}, 3},
		"event":           {Query{Event: "ea"}, 2},
		"text":            {Query{Text: "msg C"}, 1},
		"text is literal": {Query{Text: "%"}, 0},
		"since/until":     {Query{Since: base.Add(2 * time.Second), Until: base.Add(4 * time.Second)}, 2},
	} {
		page, err := l.Query(ctx, test.q)
		if err != nil || len(page.Records) != test.want {
			t.Errorf("%s: got %d records (%v), want %d", name, len(page.Records), err, test.want)
		}
	}
	trace, err := l.Trace(ctx, "ea")
	if err != nil || len(trace) != 2 || trace[0].ID > trace[1].ID {
		t.Fatalf("trace = %+v, %v", trace, err)
	}
	var payload map[string]string
	if err := json.Unmarshal(trace[0].Payload, &payload); err != nil || payload["text"] != "msg A" {
		t.Fatalf("payload = %s", trace[0].Payload)
	}
	if err := l.Prune(ctx, base.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if h := l.Health(ctx); !h.OK || h.OldestRetained != 4 {
		t.Fatalf("health after prune = %+v", h)
	}
}

func TestWriteFailureIsReportedInHealth(t *testing.T) {
	l := openLog(t)
	_ = l.Close()
	l.Record(context.Background(), otogi.Record{At: time.Now(), Kind: "k", Component: "kernel"})
	if h := l.Health(context.Background()); h.OK || h.LastError == "" {
		t.Fatalf("health = %+v, want the write error", h)
	}
}

func TestHandler(t *testing.T) {
	l := openLog(t)
	seed(t, l, 3, time.Unix(1000, 0))
	shutdown := make(chan struct{}, 1)
	ready := false
	handler, err := newHandler(ServerConfig{
		Events: l, Token: "secret",
		Status: func(context.Context) Status { return Status{Version: "test"} },
		Running: func() []Running {
			return []Running{{Module: "chat", Origin: "ea", Event: "ea", Started: time.Unix(1000, 0)}}
		},
		Ready:    func() bool { return ready },
		Shutdown: func() { shutdown <- struct{}{} },
	})
	if err != nil {
		t.Fatal(err)
	}
	do := func(method, path, token string) (int, string) {
		req := httptest.NewRequest(method, path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	tests := []struct {
		method, path, token string
		code                int
		contains            string
	}{
		{"GET", "/healthz", "", 200, "alive"},
		{"GET", "/readyz", "", 503, "not ready"},
		{"GET", "/api/status", "", 401, "send the management token"},
		{"GET", "/api/status", "wrong", 401, "send the management token"},
		{"GET", "/api/status", "secret", 200, `"running":[{"module":"chat"`},
		{"GET", "/api/events?kind=k0&limit=1", "secret", 200, `"more":true`},
		{"GET", "/api/events?before=x", "secret", 422, `"location":"query.before"`},
		{"GET", "/api/events?before=2&after=1", "secret", 400, "exclusive"},
		{"GET", "/api/events?since=yesterday", "secret", 422, `"location":"query.since"`},
		{"GET", "/api/events/ea/trace", "secret", 200, `"running":[{"module":"chat"`},
		{"GET", "/api/events/eb/trace", "secret", 200, `"running":[]`},
		{"GET", "/api/tasks?outcome=running", "secret", 200, `"outcome":"running"`},
		{"GET", "/api/tasks?outcome=stuck", "secret", 422, `"location":"query.outcome"`},
		{"POST", "/api/shutdown", "secret", 202, ""},
	}
	for _, test := range tests {
		code, body := do(test.method, test.path, test.token)
		if code != test.code || !strings.Contains(body, test.contains) {
			t.Errorf("%s %s = %d %s; want %d containing %q", test.method, test.path, code, body, test.code, test.contains)
		}
	}
	ready = true
	if code, _ := do("GET", "/readyz", ""); code != 200 {
		t.Errorf("readyz when ready = %d", code)
	}
	select {
	case <-shutdown:
	default:
		t.Error("POST /api/shutdown did not request shutdown")
	}
}

func TestServerRequiresTokenOffLoopback(t *testing.T) {
	cfg := ServerConfig{Addr: "0.0.0.0:0", Logger: quiet, Ready: func() bool { return true }}
	if _, err := StartServer(cfg); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("StartServer without token = %v", err)
	}
	cfg.Addr = "127.0.0.1:0"
	s, err := StartServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get("http://" + s.Addr() + "/healthz")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("GET /healthz = %v, %v", resp, err)
	}
	_ = resp.Body.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

// The startup settings are what the config file sets, with each secret
// reported only as set or not.
func TestStartupSettingsHoldNoSecret(t *testing.T) {
	startup := func(cfg ServerConfig) string {
		t.Helper()
		mux, _ := newAPI(cfg, nil, false)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/system/startup", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("GET /api/system/startup = %d %s", w.Code, w.Body)
		}
		return strings.TrimSpace(w.Body.String())
	}
	got := startup(ServerConfig{
		ConfigFile: "/etc/otogi/otogi.yaml", DataDir: "/var/lib/otogi", Addr: "0.0.0.0:8090", Token: "value-token", PublicURL: "https://otogi.example.com",
		OIDC: OIDC{Issuer: "https://opus.name", ClientID: "otogi", ClientSecret: "value-client-secret", Allow: []string{"tundra"}},
	})
	want := `{"config_file":"/etc/otogi/otogi.yaml","data_dir":"/var/lib/otogi","management":{"addr":"0.0.0.0:8090","token_set":true,"public_url":"https://otogi.example.com","oidc":{"issuer":"https://opus.name","client_id":"otogi","client_secret_set":true,"allow":["tundra"]}}}`
	if got != want {
		t.Fatalf("startup settings:\n%s\nwant\n%s", got, want)
	}
	if got := startup(ServerConfig{Addr: "127.0.0.1:8090"}); !strings.Contains(got, `"token_set":false`) || !strings.Contains(got, `"client_secret_set":false`) || !strings.Contains(got, `"allow":[]`) {
		t.Fatalf("startup settings with nothing set: %s", got)
	}
}
