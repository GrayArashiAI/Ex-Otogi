package management

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/danielgtaylor/huma/v2"
	"github.com/gorilla/securecookie"
	"golang.org/x/oauth2"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

const (
	sessionCookie  = "otogi_session"
	ceremonyCookie = "otogi_signin"
	sessionAge     = 7 * 24 * time.Hour
	ceremonyAge    = 10 * time.Minute
)

// OIDC is the identity provider the panel signs in with, through the OpenID
// Connect authorization code flow with PKCE.
type OIDC struct {
	// Issuer is the provider's issuer URL, such as https://opus.name.
	Issuer string
	// ClientID and ClientSecret are the panel's client at the provider.
	ClientID, ClientSecret string
	// Allow lists the provider usernames that may use the panel.
	Allow []string
}

// signIn is the panel's sign-in: OpenID Connect against one provider, and
// sessions in sealed cookies.
type signIn struct {
	cfg      OIDC
	callback string // the redirect URI registered at the provider
	home     string // the panel's origin, where signing out lands
	cookies  *securecookie.SecureCookie
	secure   bool // cookies go only over HTTPS
	events   *EventLog

	mu       sync.Mutex
	provider *oidc.Provider // discovered on first use, so a provider outage does not stop the bot
}

// session is what a session cookie holds.
type session struct {
	Subject  string `json:"sub"`
	Username string `json:"username"`
	Name     string `json:"name"`
	IDToken  string `json:"id_token"` // the hint signing out at the provider needs
}

// ceremony is what the cookie between leaving for the provider and coming
// back holds.
type ceremony struct {
	State    string `json:"state"`
	Nonce    string `json:"nonce"`
	Verifier string `json:"verifier"`
	Next     string `json:"next"`
}

// LoadCookieKey reads the key that signs and encrypts session cookies from
// path, generating it on first use.
func LoadCookieKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err == nil && len(key) == 64 {
		return key, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("management: read cookie key: %w", err)
	}
	key = make([]byte, 64)
	_, _ = rand.Read(key) // never fails
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("management: create directory for cookie key: %w", err)
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, fmt.Errorf("management: write cookie key: %w", err)
	}
	return key, nil
}

func newSignIn(cfg OIDC, publicURL string, key []byte, events *EventLog) (*signIn, error) {
	u, err := url.Parse(publicURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("management: public_url %q: want the panel's origin, such as https://otogi.example.com", publicURL)
	}
	if len(key) != 64 {
		return nil, fmt.Errorf("management: the cookie key is %d bytes; want 64", len(key))
	}
	home := u.Scheme + "://" + u.Host
	cookies := securecookie.New(key[:32], key[32:]).MaxAge(int(sessionAge.Seconds())).SetSerializer(securecookie.JSONEncoder{})
	return &signIn{cfg: cfg, callback: home + "/api/auth/callback", home: home, cookies: cookies, secure: u.Scheme == "https", events: events}, nil
}

// oauth returns the provider's endpoints, discovering them once.
func (s *signIn) oauth(ctx context.Context) (*oidc.Provider, *oauth2.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.provider == nil {
		p, err := oidc.NewProvider(ctx, s.cfg.Issuer)
		if err != nil {
			return nil, nil, fmt.Errorf("discover %s: %w", s.cfg.Issuer, err)
		}
		s.provider = p
	}
	return s.provider, &oauth2.Config{
		ClientID: s.cfg.ClientID, ClientSecret: s.cfg.ClientSecret, Endpoint: s.provider.Endpoint(),
		RedirectURL: s.callback, Scopes: []string{oidc.ScopeOpenID, "profile"},
	}, nil
}

func (s *signIn) cookie(name string, value any, age time.Duration) (http.Cookie, error) {
	encoded, err := s.cookies.Encode(name, value)
	if err != nil {
		return http.Cookie{}, fmt.Errorf("seal cookie: %w", err)
	}
	// Lax, not Strict: the provider's redirect back to the callback is a
	// cross-site navigation that must carry the sign-in cookie.
	return http.Cookie{Name: name, Value: encoded, Path: "/", MaxAge: int(age.Seconds()), Secure: s.secure, HttpOnly: true, SameSite: http.SameSiteLaxMode}, nil
}

func (s *signIn) clear(name string) http.Cookie {
	return http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, Secure: s.secure, HttpOnly: true, SameSite: http.SameSiteLaxMode}
}

// who returns the session a cookie holds, while its account is still
// allowed.
func (s *signIn) who(value string) (session, bool) {
	var sess session
	if value == "" || s.cookies.Decode(sessionCookie, value, &sess) != nil {
		return session{}, false
	}
	return sess, slices.Contains(s.cfg.Allow, sess.Username)
}

func (s *signIn) record(ctx context.Context, action string, sess session, err error) {
	payload, outcome := map[string]any{"action": action, "username": sess.Username, "subject": sess.Subject}, "ok"
	if err != nil {
		payload["error"], outcome = err.Error(), "error"
	}
	s.events.Record(ctx, otogi.Record{At: time.Now(), Kind: "panel.session", Component: "management", Outcome: outcome, Payload: payload})
}

func (sess session) display() string {
	if sess.Name != "" && sess.Name != sess.Username {
		return sess.Name + " (" + sess.Username + ")"
	}
	return sess.Username
}

// guard admits a request to /api/ when it has a panel session, or when it
// comes straight to the server (not through a proxy, which sets
// X-Forwarded-For) with the bearer token, or from loopback when there is no
// token. Browsers' cross-origin writes are refused.
func guard(token string, s *signIn, open func(string) bool, next http.Handler) http.Handler {
	want := []byte("Bearer " + token)
	return http.NewCrossOriginProtection().Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if s != nil {
			if c, _ := r.Cookie(sessionCookie); c != nil {
				if sess, ok := s.who(c.Value); ok {
					next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, callerKey{}, sess.display())))
					return
				}
			}
		}
		proxied := r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("Forwarded") != ""
		direct := !proxied && ((token != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) == 1) ||
			(token == "" && loopbackRemote(r.RemoteAddr)))
		if direct || open(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/problem+json")
		writeJSON(w, http.StatusUnauthorized, huma.ErrorModel{Title: "Unauthorized", Status: http.StatusUnauthorized, Detail: "sign in, or send the management token"})
	}))
}

func loopbackRemote(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	ip := net.ParseIP(host)
	return err == nil && ip != nil && ip.IsLoopback()
}

// signInRoutes are open to everyone: they are how one signs in.
func signInRoutes(path string) bool {
	return strings.HasPrefix(path, "/api/auth/")
}

// SessionCookie is a request's session cookie.
type SessionCookie struct {
	Session string `cookie:"otogi_session"`
}

// redirect is a response that sends the browser elsewhere.
type redirect struct {
	Status    int           `json:"-"`
	Location  string        `header:"Location"`
	SetCookie []http.Cookie `header:"Set-Cookie"`
}

// localPath keeps a return path within the panel.
func localPath(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	return next
}

func random() string {
	return oauth2.GenerateVerifier() // 32 random bytes, base64url
}

func registerSignIn(api huma.API, s *signIn) {
	tags := []string{"Sign-in"}
	type sessionOutput struct {
		Body struct {
			SignedIn bool   `json:"signed_in"`
			Username string `json:"username,omitempty"`
			Name     string `json:"name,omitempty"`
			Provider string `json:"provider" doc:"The identity provider's issuer, which the sign-in page names."`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "get-session", Method: http.MethodGet, Path: "/api/auth/session", Summary: "Whether this browser is signed in", Tags: tags},
		func(_ context.Context, in *SessionCookie) (*sessionOutput, error) {
			out := &sessionOutput{}
			out.Body.Provider = s.cfg.Issuer
			if sess, ok := s.who(in.Session); ok {
				out.Body.SignedIn, out.Body.Username, out.Body.Name = true, sess.Username, sess.Name
			}
			return out, nil
		})
	huma.Register(api, huma.Operation{OperationID: "sign-in", Method: http.MethodGet, Path: "/api/auth/login", Summary: "Sign in at the identity provider",
		Description: "Sends the browser to the provider, which sends it back to /api/auth/callback.", DefaultStatus: http.StatusFound, Tags: tags},
		func(ctx context.Context, in *struct {
			Next string `query:"next" doc:"The panel path to return to."`
		},
		) (*redirect, error) {
			_, cfg, err := s.oauth(ctx)
			if err != nil {
				return nil, huma.Error502BadGateway("the identity provider is unreachable: " + err.Error())
			}
			c := ceremony{State: random(), Nonce: random(), Verifier: oauth2.GenerateVerifier(), Next: localPath(in.Next)}
			cookie, err := s.cookie(ceremonyCookie, c, ceremonyAge)
			if err != nil {
				return nil, huma.Error500InternalServerError(err.Error())
			}
			to := cfg.AuthCodeURL(c.State, oidc.Nonce(c.Nonce), oauth2.S256ChallengeOption(c.Verifier))
			return &redirect{Status: http.StatusFound, Location: to, SetCookie: []http.Cookie{cookie}}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "sign-in-callback", Method: http.MethodGet, Path: "/api/auth/callback", Summary: "Come back from the identity provider",
		Description: "Checks the provider's answer and starts a session; a failure returns to the sign-in page saying why.", DefaultStatus: http.StatusFound, Tags: tags},
		func(ctx context.Context, in *struct {
			Ceremony    string `cookie:"otogi_signin"`
			Code        string `query:"code"`
			State       string `query:"state"`
			Error       string `query:"error"`
			Description string `query:"error_description"`
		},
		) (*redirect, error) {
			fail := func(reason string, sess session, err error) *redirect {
				s.record(ctx, "denied", sess, err)
				return &redirect{Status: http.StatusFound, Location: "/sign-in?error=" + url.QueryEscape(reason), SetCookie: []http.Cookie{s.clear(ceremonyCookie)}}
			}
			var c ceremony
			if in.Ceremony == "" || s.cookies.Decode(ceremonyCookie, in.Ceremony, &c) != nil || subtle.ConstantTimeCompare([]byte(c.State), []byte(in.State)) != 1 {
				return fail("The sign-in expired or was started in another browser. Try again.", session{}, errors.New("no matching sign-in under way")), nil
			}
			if in.Error != "" {
				return fail("The identity provider said: "+strings.TrimSpace(in.Error+" "+in.Description), session{}, errors.New(in.Error)), nil
			}
			provider, cfg, err := s.oauth(ctx)
			if err != nil {
				return fail("The identity provider is unreachable.", session{}, err), nil
			}
			token, err := cfg.Exchange(ctx, in.Code, oauth2.VerifierOption(c.Verifier))
			if err != nil {
				return fail("The identity provider did not accept the sign-in.", session{}, err), nil
			}
			raw, _ := token.Extra("id_token").(string)
			idToken, err := provider.Verifier(&oidc.Config{ClientID: s.cfg.ClientID}).Verify(ctx, raw)
			if err == nil && idToken.Nonce != c.Nonce {
				err = errors.New("the ID token's nonce does not match")
			}
			if err != nil {
				return fail("The identity provider's answer did not check out.", session{}, err), nil
			}
			var claims struct {
				Username    string `json:"username"`
				DisplayName string `json:"displayName"`
			}
			if err := idToken.Claims(&claims); err != nil {
				return fail("The identity provider's answer did not check out.", session{}, err), nil
			}
			sess := session{Subject: idToken.Subject, Username: claims.Username, Name: claims.DisplayName, IDToken: raw}
			if !slices.Contains(s.cfg.Allow, sess.Username) {
				return fail(fmt.Sprintf("The account %q may not use this panel.", sess.Username), sess, errors.New("not in management.oidc.allow")), nil
			}
			cookie, err := s.cookie(sessionCookie, sess, sessionAge)
			if err != nil {
				return nil, huma.Error500InternalServerError(err.Error())
			}
			s.record(ctx, "sign_in", sess, nil)
			return &redirect{Status: http.StatusFound, Location: c.Next, SetCookie: []http.Cookie{cookie, s.clear(ceremonyCookie)}}, nil
		})
	type signOutOutput struct {
		SetCookie []http.Cookie `header:"Set-Cookie"`
		Body      struct {
			Next string `json:"next" doc:"Where to go to sign out at the identity provider too."`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "sign-out", Method: http.MethodPost, Path: "/api/auth/logout", Summary: "Sign out, here and at the identity provider", Tags: tags},
		func(ctx context.Context, in *SessionCookie) (*signOutOutput, error) {
			out := &signOutOutput{SetCookie: []http.Cookie{s.clear(sessionCookie)}}
			out.Body.Next = "/sign-in"
			var sess session
			if in.Session != "" && s.cookies.Decode(sessionCookie, in.Session, &sess) == nil {
				s.record(ctx, "sign_out", sess, nil)
				if provider, _, err := s.oauth(ctx); err == nil {
					var meta struct {
						EndSession string `json:"end_session_endpoint"`
					}
					if provider.Claims(&meta) == nil && meta.EndSession != "" {
						q := url.Values{"id_token_hint": {sess.IDToken}, "post_logout_redirect_uri": {s.home + "/sign-in"}}
						out.Body.Next = meta.EndSession + "?" + q.Encode()
					}
				}
			}
			return out, nil
		})
}
