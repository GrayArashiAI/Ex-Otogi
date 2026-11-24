package decision

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/settingstest"
	contract "github.com/TundraWork/Ex-Otogi/pkg/decision"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

type host struct {
	otogi.Host
	mu       sync.Mutex
	records  []otogi.Record
	provided map[string]any
}

func (h *host) Logger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func (h *host) Record(_ context.Context, r otogi.Record) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
}

func (h *host) Provide(name string, service any) error {
	h.provided[name] = service
	return nil
}

func (h *host) Service(name string) (any, bool) {
	s, ok := h.provided[name]
	return s, ok
}

// reply is what Jev answered on 2026-11-23 to two questions.
const reply = `{"model": "jev-1.14.0", "answers": {"wake": {"type": "noul", "noul": 0.92}, "done": {"type": "noul", "noul": 0.48}}, "usage": {"input_tokens": 354, "output_tokens": 36}}`

// started returns a started module whose Jev is the handler, and its host.
func started(t *testing.T, handler http.HandlerFunc) (*Module, *host) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Setenv("TEST_TYPESAFE_KEY", "ts-key")
	m, err := New(Config{Jev: &JevConfig{APIKey: "${TEST_TYPESAFE_KEY}", BaseURL: srv.URL, Timeout: otogi.Duration(time.Second)}}, os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	h := &host{provided: map[string]any{}}
	if err := m.Start(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	return m, h
}

func request() contract.Request {
	return contract.Request{
		State: map[string]any{"history": []map[string]string{{"sender": "Alice", "text": "水月你觉得呢"}}},
		Questions: map[string]contract.Question{
			"wake": {Instructions: "Is the last message addressed to Mizuki?", Criteria: &contract.Criteria{True: "It asks Mizuki", False: "It is to someone else"}},
			"done": {Instructions: "Has the sender finished?"},
		},
	}
}

// The request carries the model, the state as JSON, each question as a
// noul with its criteria when set, and the key as a bearer token.
func TestRequest(t *testing.T) {
	var got map[string]any
	var auth string
	m, _ := started(t, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("request body: %v", err)
		}
		_, _ = io.WriteString(w, reply)
	})
	if _, err := m.Decide(context.Background(), request()); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer ts-key" {
		t.Errorf("Authorization = %q", auth)
	}
	want := `{"model":"jev-latest","questions":{"done":{"instructions":"Has the sender finished?","type":"noul"},"wake":{"criteria":{"false":"It is to someone else","true":"It asks Mizuki"},"instructions":"Is the last message addressed to Mizuki?","type":"noul"}},"state":{"history":[{"sender":"Alice","text":"水月你觉得呢"}]}}`
	if b, _ := json.Marshal(got); string(b) != want {
		t.Errorf("request =\n%s\nwant\n%s", b, want)
	}
}

// Answers are the probabilities of yes; a question Jev did not answer is
// missing.
func TestAnswers(t *testing.T) {
	m, _ := started(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"model": "jev-1.14.0", "answers": {"wake": {"type": "noul", "noul": 0.92}}, "usage": {"input_tokens": 300}}`)
	})
	got, err := m.Decide(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["wake"] != 0.92 {
		t.Errorf("answers = %v, want only wake 0.92", got)
	}
}

func TestErrors(t *testing.T) {
	for _, c := range []struct {
		name   string
		status int
		body   string
		state  any
		want   error
		says   string
	}{
		{name: "state that is not JSON", state: func() {}, want: otogi.ErrInvalid, says: "state"},
		{name: "401", status: 401, body: `{"detail": {"error_type": "authentication_error", "message": "Cannot authenticate with the server."}}`, want: otogi.ErrInvalid, says: "HTTP 401: Cannot authenticate with the server."},
		{name: "400", status: 400, body: `{"detail": {"error_type": "api_usage_error", "message": "Invalid request."}}`, want: otogi.ErrInvalid, says: "HTTP 400: Invalid request."},
		{name: "429", status: 429, body: `{}`, want: otogi.ErrUnavailable, says: "HTTP 429"},
		{name: "500", status: 500, body: `oops`, want: otogi.ErrUnavailable, says: "HTTP 500"},
		{name: "not JSON", status: 200, body: `<html>`, want: otogi.ErrUnavailable, says: "reply"},
		{name: "no answers", status: 200, body: `{"model": "jev-1.14.0"}`, want: otogi.ErrUnavailable, says: "no answers"},
		{name: "answer of the wrong type", status: 200, body: `{"answers": {"wake": {"type": "noul", "noul": "high"}}}`, want: otogi.ErrUnavailable, says: "reply"},
	} {
		t.Run(c.name, func(t *testing.T) {
			m, h := started(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(c.status)
				_, _ = io.WriteString(w, c.body)
			})
			req := request()
			if c.state != nil {
				req.State = c.state
			}
			_, err := m.Decide(context.Background(), req)
			if !errors.Is(err, c.want) || !strings.Contains(err.Error(), c.says) {
				t.Errorf("err = %v, want %v saying %q", err, c.want, c.says)
			}
			if len(h.records) != 1 || h.records[0].Kind != "decision.failed" || h.records[0].Outcome != "error" {
				t.Errorf("records = %+v, want one decision.failed", h.records)
			}
		})
	}
}

// A request that outlasts the timeout is unavailable, not retried.
func TestTimeout(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	m, _ := started(t, func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
		<-release
	})
	// Registered after started's, so it runs first and the server can close.
	t.Cleanup(func() { close(release) })
	m.jev.timeout = 50 * time.Millisecond
	if _, err := m.Decide(context.Background(), request()); !errors.Is(err, otogi.ErrUnavailable) || calls.Load() != 1 {
		t.Errorf("err = %v after %d calls, want ErrUnavailable after 1", err, calls.Load())
	}
}

// Each request is recorded with its outcome, duration, the asking module,
// the model that answered, what was asked, and Jev's reply as it came.
func TestRecord(t *testing.T) {
	m, h := started(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("x-typesafe-request-id", "req-1")
		_, _ = io.WriteString(w, reply)
	})
	ctx := otogi.ContextWithWork(context.Background(), otogi.Work{Module: "chat", Origin: "e1", Event: "e1"})
	if _, err := m.Decide(ctx, request()); err != nil {
		t.Fatal(err)
	}
	if len(h.records) != 1 {
		t.Fatalf("records = %+v, want one", h.records)
	}
	r := h.records[0]
	if r.Kind != "decision.completed" || r.Outcome != "ok" || r.Duration <= 0 {
		t.Errorf("record = %s %s %v", r.Kind, r.Outcome, r.Duration)
	}
	b, err := json.Marshal(r.Payload)
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Module    string                     `json:"module"`
		Model     string                     `json:"model"`
		State     map[string]any             `json:"state"`
		Questions map[string]json.RawMessage `json:"questions"`
		Answers   map[string]float64         `json:"answers"`
		Usage     jevUsage                   `json:"usage"`
		RequestID string                     `json:"request_id"`
		Reply     json.RawMessage            `json:"reply"`
	}
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	if p.Module != "chat" || p.Model != "jev-1.14.0" || p.State["history"] == nil || len(p.Questions) != 2 ||
		p.Answers["wake"] != 0.92 || p.Usage.InputTokens != 354 || p.RequestID != "req-1" || !strings.Contains(string(p.Reply), `"noul":0.92`) {
		t.Errorf("payload = %s", b)
	}
}

// A reply that is not JSON is kept as text, so the record shows what came.
func TestRecordKeepsAReplyThatIsNotJSON(t *testing.T) {
	m, h := started(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(502)
		_, _ = io.WriteString(w, "Bad Gateway")
	})
	if _, err := m.Decide(context.Background(), request()); err == nil {
		t.Fatal("no error")
	}
	p := h.records[0].Payload.(map[string]any)
	if p["reply"] != "Bad Gateway" || p["status"] != 502 {
		t.Errorf("payload = %v", p)
	}
}

// Start provides the service under its name.
func TestService(t *testing.T) {
	m, h := started(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, reply) })
	if d := m.Descriptor(); d.ID != "decision" || len(d.Provides) != 1 || d.Provides[0] != contract.Service {
		t.Errorf("descriptor = %+v", d)
	}
	d, err := otogi.Lookup[contract.Decider](h, contract.Service)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := d.Decide(context.Background(), request()); err != nil || got["done"] != 0.48 {
		t.Errorf("Decide = %v, %v", got, err)
	}
}

// The defaults the settings form shows are the defaults New applies.
func TestDefaults(t *testing.T) {
	t.Setenv("TEST_TYPESAFE_KEY", "ts-key")
	typed := settingstest.WithDefaults(t, JevConfig{APIKey: "${TEST_TYPESAFE_KEY}"})
	a, err := New(Config{Jev: &typed}, os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(Config{Jev: &JevConfig{APIKey: "${TEST_TYPESAFE_KEY}"}}, os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	if a.jev.url != b.jev.url || a.jev.model != b.jev.model || a.jev.timeout != b.jev.timeout {
		t.Errorf("form defaults %+v, applied defaults %+v", *a.jev, *b.jev)
	}
}

// An API key whose secret is not set stops the module.
func TestMissingKey(t *testing.T) {
	if _, err := New(Config{Jev: &JevConfig{APIKey: "${TEST_TYPESAFE_KEY_UNSET}"}}, os.LookupEnv); !errors.Is(err, otogi.ErrInvalid) || !strings.Contains(err.Error(), "api_key") {
		t.Errorf("err = %v", err)
	}
}
