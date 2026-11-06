package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/descope/virtualwebauthn"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/management/panel"
)

// browser talks to the panel through a proxy, as a browser behind nginx
// does, and keeps its cookies.
type browser struct {
	t       *testing.T
	handler http.Handler
	cookies map[string]*http.Cookie
}

func (b *browser) do(method, path, body string, wantCode int) string {
	b.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:40000"
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, c := range b.cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	b.handler.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge < 0 {
			delete(b.cookies, c.Name)
		} else {
			b.cookies[c.Name] = c
		}
	}
	if rec.Code != wantCode {
		b.t.Fatalf("%s %s = %d %s, want %d", method, path, rec.Code, rec.Body.String(), wantCode)
	}
	return rec.Body.String()
}

// The first passkey comes from a one-time link, then signs in; a signed-in
// operator adds and removes passkeys; without a session, a proxied request
// gets nothing, and a cross-site write is refused.
func TestPasskeySignIn(t *testing.T) {
	ctx := context.Background()
	store, err := panel.Open(ctx, filepath.Join(t.TempDir(), "panel.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	events := openLog(t)
	handler, err := newHandler(ServerConfig{
		Token: "secret", Events: events, PublicURL: "https://otogi.example.com", Passkeys: store,
		Status: func(context.Context) any { return map[string]string{"version": "test"} }, Ready: func() bool { return true }, Shutdown: func() {},
	})
	if err != nil {
		t.Fatal(err)
	}
	rp := virtualwebauthn.RelyingParty{Name: "Ex-Otogi", ID: "otogi.example.com", Origin: "https://otogi.example.com"}
	phone := virtualwebauthn.NewAuthenticatorWithOptions(virtualwebauthn.AuthenticatorOptions{UserHandle: store.UserHandle()})
	b := &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}

	b.do("GET", "/api/status", "", 401)
	if body := b.do("GET", "/api/auth/session", "", 200); !strings.Contains(body, `"signed_in":false`) {
		t.Fatalf("session before signing in: %s", body)
	}
	// register registers a new credential on phone through a ceremony.
	register := func(beginPath, beginBody, finishPath, name string) {
		t.Helper()
		options, err := virtualwebauthn.ParseAttestationOptions(b.do("POST", beginPath, beginBody, 200))
		if err != nil {
			t.Fatal(err)
		}
		credential := virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2)
		response := virtualwebauthn.CreateAttestationResponse(rp, phone, credential, *options)
		body, _ := json.Marshal(map[string]any{"name": name, "credential": json.RawMessage(response)})
		b.do("POST", finishPath, string(body), 204)
		phone.AddCredential(credential)
	}

	token, err := store.NewEnrollment(ctx, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	b.do("POST", "/api/auth/enroll/begin", `{"token": "forged"}`, 403)
	register("/api/auth/enroll/begin", `{"token": "`+token+`"}`, "/api/auth/enroll/finish", "Tundra's iPhone")
	b.do("GET", "/api/status", "", 200) // registering signed in
	b.do("POST", "/api/auth/enroll/begin", `{"token": "`+token+`"}`, 403)

	b.do("POST", "/api/auth/logout", "", 204)
	b.do("GET", "/api/status", "", 401)
	options, err := virtualwebauthn.ParseAssertionOptions(b.do("POST", "/api/auth/login/begin", "", 200))
	if err != nil {
		t.Fatal(err)
	}
	response := virtualwebauthn.CreateAssertionResponse(rp, phone, phone.Credentials[0], *options)
	if body := b.do("POST", "/api/auth/login/finish", response, 200); !strings.Contains(body, `"passkey":"Tundra's iPhone"`) {
		t.Fatalf("sign-in: %s", body)
	}
	b.do("POST", "/api/auth/login/finish", response, 400) // the ceremony is over

	register("/api/auth/passkeys/begin", "", "/api/auth/passkeys/finish", "Desk")
	var keys []PasskeyInfo
	if err := json.Unmarshal([]byte(b.do("GET", "/api/auth/passkeys", "", 200)), &keys); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0].Name != "Tundra's iPhone" || !keys[0].Current || keys[0].UsedAt.IsZero() || keys[1].Current {
		t.Fatalf("passkeys %+v", keys)
	}
	b.do("DELETE", "/api/auth/passkeys/"+keys[1].ID, "", 204)
	b.do("DELETE", "/api/auth/passkeys/"+keys[0].ID, "", 409) // the last one
	if err := b.cookies[sessionCookie].Valid(); err != nil {
		t.Fatal(err)
	}

	// A write from another site carries the cookie, but is refused.
	req := httptest.NewRequest("POST", "/api/shutdown", nil)
	req.RemoteAddr = "127.0.0.1:40000"
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.AddCookie(b.cookies[sessionCookie])
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site write = %d", rec.Code)
	}
	// The token works straight to the server, not through the proxy.
	req = httptest.NewRequest("GET", "/api/status", nil)
	req.RemoteAddr = "127.0.0.1:40000"
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("token from the server = %d", rec.Code)
	}
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("token through the proxy = %d", rec.Code)
	}

	var actions []string
	page, _ := events.Query(ctx, Query{Kind: "panel.session"})
	for _, r := range page.Records {
		var p map[string]any
		_ = json.Unmarshal(r.Payload, &p)
		actions = append(actions, p["action"].(string)+":"+r.Outcome)
	}
	if got := strings.Join(actions, " "); got != "remove:ok register:ok sign_in:ok sign_out:ok register:ok" {
		t.Fatalf("records %s", got)
	}
}
