package management

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// provider is an OpenID Connect provider: discovery, keys, and a token
// endpoint that checks PKCE and signs ID tokens for one account.
type provider struct {
	t        *testing.T
	srv      *httptest.Server
	key      *rsa.PrivateKey
	username string
	nonce    string // from the last authorization request
	verifier string // the code_challenge of the last authorization request
}

func newProvider(t *testing.T, username string) *provider {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &provider{t: t, key: key, username: username}
	p.srv = httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *provider) serve(w http.ResponseWriter, r *http.Request) {
	iss := p.srv.URL
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": iss, "authorization_endpoint": iss + "/authorize", "token_endpoint": iss + "/token",
			"jwks_uri": iss + "/jwks", "end_session_endpoint": iss + "/logout", "id_token_signing_alg_values_supported": []string{"RS256"},
		})
	case "/jwks":
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &p.key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	case "/token":
		_ = r.ParseForm()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if r.Form.Get("code") != "good-code" || base64.RawURLEncoding.EncodeToString(sum[:]) != p.verifier {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		signer, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: p.key, KeyID: "k1"}}, nil)
		claims := map[string]any{
			"iss": iss, "sub": "1", "aud": "ex-otogi", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
			"nonce": p.nonce, "username": p.username, "displayName": "Tundra",
		}
		token, _ := jwt.Signed(signer).Claims(claims).Serialize()
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": token, "expires_in": 3600})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// browser talks to the panel through a proxy, as a browser behind nginx
// does, and keeps its cookies.
type browser struct {
	t       *testing.T
	handler http.Handler
	cookies map[string]*http.Cookie
}

func (b *browser) do(method, path string, wantCode int) *httptest.ResponseRecorder {
	b.t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = "127.0.0.1:40000"
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
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
	return rec
}

// signIn goes to the provider and comes back with code, as the provider's
// redirect would.
func (b *browser) signIn(p *provider, code string) *httptest.ResponseRecorder {
	b.t.Helper()
	to, err := url.Parse(b.do("GET", "/api/auth/login?next=/chats", http.StatusFound).Header().Get("Location"))
	if err != nil {
		b.t.Fatal(err)
	}
	q := to.Query()
	if to.Path != "/authorize" || q.Get("code_challenge_method") != "S256" || q.Get("client_id") != "ex-otogi" || q.Get("redirect_uri") != "https://otogi.example.com/api/auth/callback" {
		b.t.Fatalf("authorization request %s", to)
	}
	p.nonce, p.verifier = q.Get("nonce"), q.Get("code_challenge")
	return b.do("GET", "/api/auth/callback?code="+code+"&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
}

func newSignInHandler(t *testing.T, p *provider) (http.Handler, *EventLog) {
	t.Helper()
	events := openLog(t)
	key := make([]byte, 64)
	_, _ = rand.Read(key)
	handler, err := newHandler(ServerConfig{
		Token: "secret", Events: events, PublicURL: "https://otogi.example.com", CookieKey: key,
		OIDC:   OIDC{Issuer: p.srv.URL, ClientID: "ex-otogi", ClientSecret: "s3cret", Allow: []string{"tundra"}},
		Status: func(context.Context) Status { return Status{Version: "test"} }, Ready: func() bool { return true }, Shutdown: func() {},
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler, events
}

// Signing in goes through the provider and back; an allowed account gets a
// session, which signing out ends here and at the provider. A proxied
// request without a session gets nothing, and a cross-site write is
// refused.
func TestOIDCSignIn(t *testing.T) {
	p := newProvider(t, "tundra")
	handler, events := newSignInHandler(t, p)
	b := &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}

	b.do("GET", "/api/status", http.StatusUnauthorized)
	if back := b.signIn(p, "bad-code").Header().Get("Location"); !strings.HasPrefix(back, "/sign-in?error=") {
		t.Fatalf("a refused code went to %q", back)
	}
	if back := b.signIn(p, "good-code").Header().Get("Location"); back != "/chats" {
		t.Fatalf("signing in went to %q", back)
	}
	b.do("GET", "/api/status", http.StatusOK)
	if body := b.do("GET", "/api/auth/session", http.StatusOK).Body.String(); !strings.Contains(body, `"username":"tundra"`) {
		t.Fatalf("session %s", body)
	}
	b.do("GET", "/api/auth/callback?code=good-code&state=replayed", http.StatusFound) // the sign-in is over

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

	var out struct {
		Next string `json:"next"`
	}
	_ = json.Unmarshal(b.do("POST", "/api/auth/logout", http.StatusOK).Body.Bytes(), &out)
	if !strings.HasPrefix(out.Next, p.srv.URL+"/logout?") || !strings.Contains(out.Next, "id_token_hint=") || !strings.Contains(out.Next, url.QueryEscape("https://otogi.example.com/sign-in")) {
		t.Fatalf("sign-out goes to %q", out.Next)
	}
	b.do("GET", "/api/status", http.StatusUnauthorized)

	var actions []string
	page, _ := events.Query(context.Background(), Query{Kind: "panel.session"})
	for _, r := range page.Records {
		var payload map[string]any
		_ = json.Unmarshal(r.Payload, &payload)
		actions = append(actions, payload["action"].(string)+":"+r.Outcome)
	}
	if got := strings.Join(actions, " "); got != "sign_out:ok denied:error sign_in:ok denied:error" {
		t.Fatalf("records %s", got)
	}
}

// An account the provider vouches for, but not on the allow list, gets no
// session; the token works only straight to the server.
func TestOIDCAllowListAndToken(t *testing.T) {
	p := newProvider(t, "mallory")
	handler, _ := newSignInHandler(t, p)
	b := &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	if back := b.signIn(p, "good-code").Header().Get("Location"); !strings.Contains(back, url.QueryEscape(`"mallory"`)) {
		t.Fatalf("a disallowed account went to %q", back)
	}
	b.do("GET", "/api/status", http.StatusUnauthorized)

	req := httptest.NewRequest("GET", "/api/status", nil)
	req.RemoteAddr = "127.0.0.1:40000"
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
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
}
