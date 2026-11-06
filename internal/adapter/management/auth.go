package management

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/gorilla/securecookie"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/management/panel"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

const (
	sessionCookie  = "otogi_session"
	ceremonyCookie = "otogi_ceremony"
	sessionAge     = 30 * 24 * time.Hour
	ceremonyAge    = 5 * time.Minute
)

// signIn is the panel's sign-in: passkeys through WebAuthn, and sessions
// in sealed cookies.
type signIn struct {
	store    *panel.Store
	webauthn *webauthn.WebAuthn
	cookies  *securecookie.SecureCookie
	secure   bool // cookies go only over HTTPS
	events   *EventLog
}

// session is what a session cookie holds.
type session struct {
	Passkey []byte `json:"passkey"`
}

// ceremony is what the cookie between a ceremony's two steps holds.
type ceremony struct {
	Data       webauthn.SessionData `json:"data"`
	Enrollment string               `json:"enrollment,omitempty"` // the token of a first passkey's link
}

// operator is the one WebAuthn user every passkey belongs to.
type operator struct {
	handle      []byte
	credentials []webauthn.Credential
}

func (o operator) WebAuthnID() []byte                         { return o.handle }
func (o operator) WebAuthnName() string                       { return "operator" }
func (o operator) WebAuthnDisplayName() string                { return "Operator" }
func (o operator) WebAuthnCredentials() []webauthn.Credential { return o.credentials }

func newSignIn(store *panel.Store, publicURL string, events *EventLog) (*signIn, error) {
	u, err := url.Parse(publicURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("management: public_url %q: want the panel's origin, such as https://otogi.example.com", publicURL)
	}
	timeout := webauthn.TimeoutConfig{Enforce: true, Timeout: ceremonyAge, TimeoutUVD: ceremonyAge}
	w, err := webauthn.New(&webauthn.Config{
		RPID: u.Hostname(), RPDisplayName: "Ex-Otogi", RPOrigins: []string{u.Scheme + "://" + u.Host},
		Timeouts: webauthn.TimeoutsConfig{Login: timeout, Registration: timeout},
	})
	if err != nil {
		return nil, fmt.Errorf("management: webauthn: %w", err)
	}
	hash, block := store.CookieKeys()
	cookies := securecookie.New(hash, block).MaxAge(int(sessionAge.Seconds())).SetSerializer(securecookie.JSONEncoder{})
	return &signIn{store: store, webauthn: w, cookies: cookies, secure: u.Scheme == "https", events: events}, nil
}

func (s *signIn) operator(ctx context.Context) (operator, []panel.Passkey, error) {
	keys, err := s.store.Passkeys(ctx)
	if err != nil {
		return operator{}, nil, err
	}
	o := operator{handle: s.store.UserHandle()}
	for _, k := range keys {
		o.credentials = append(o.credentials, k.Credential)
	}
	return o, keys, nil
}

func (s *signIn) cookie(name string, value any, age time.Duration) (http.Cookie, error) {
	encoded, err := s.cookies.Encode(name, value)
	if err != nil {
		return http.Cookie{}, fmt.Errorf("seal cookie: %w", err)
	}
	return http.Cookie{Name: name, Value: encoded, Path: "/", MaxAge: int(age.Seconds()), Secure: s.secure, HttpOnly: true, SameSite: http.SameSiteStrictMode}, nil
}

func (s *signIn) clear(name string) http.Cookie {
	return http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, Secure: s.secure, HttpOnly: true, SameSite: http.SameSiteStrictMode}
}

// who returns the name of the passkey a session cookie was signed in with,
// while that passkey is still registered.
func (s *signIn) who(ctx context.Context, value string) (panel.Passkey, bool) {
	var sess session
	if value == "" || s.cookies.Decode(sessionCookie, value, &sess) != nil {
		return panel.Passkey{}, false
	}
	keys, err := s.store.Passkeys(ctx)
	if err != nil {
		return panel.Passkey{}, false
	}
	i := slices.IndexFunc(keys, func(k panel.Passkey) bool { return bytes.Equal(k.Credential.ID, sess.Passkey) })
	if i < 0 {
		return panel.Passkey{}, false
	}
	return keys[i], true
}

func (s *signIn) record(ctx context.Context, action, passkey string, err error) {
	payload, outcome := map[string]any{"action": action, "passkey": passkey}, "ok"
	if err != nil {
		payload["error"], outcome = err.Error(), "error"
	}
	s.events.Record(ctx, otogi.Record{At: time.Now(), Kind: "panel.session", Component: "management", Outcome: outcome, Payload: payload})
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
			c, _ := r.Cookie(sessionCookie)
			if c != nil {
				if key, ok := s.who(ctx, c.Value); ok {
					next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, callerKey{}, key.Name)))
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
		writeJSON(w, http.StatusUnauthorized, huma.ErrorModel{Title: "Unauthorized", Status: http.StatusUnauthorized, Detail: "sign in with a passkey, or send the management token"})
	}))
}

func loopbackRemote(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	ip := net.ParseIP(host)
	return err == nil && ip != nil && ip.IsLoopback()
}

// signInRoutes are open to everyone: they are how one signs in.
func signInRoutes(path string) bool {
	return path == "/api/auth/session" || path == "/api/auth/logout" ||
		strings.HasPrefix(path, "/api/auth/login/") || strings.HasPrefix(path, "/api/auth/enroll/")
}

// PasskeyInfo is a registered passkey as the panel lists it.
type PasskeyInfo struct {
	ID        string    `json:"id" doc:"The credential ID, base64url."`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UsedAt    time.Time `json:"used_at,omitzero" doc:"When it last signed in."`
	Current   bool      `json:"current" doc:"Whether this session signed in with it."`
}

// SetCookies sets cookies on a response.
type SetCookies struct {
	SetCookie []http.Cookie `header:"Set-Cookie"`
}

type optionsOutput struct {
	SetCookies
	Body json.RawMessage `doc:"The options for navigator.credentials, as go-webauthn writes them."`
}

// SessionCookie is a request's session cookie.
type SessionCookie struct {
	Session string `cookie:"otogi_session"`
}

// CeremonyCookie is a request's cookie between a ceremony's steps.
type CeremonyCookie struct {
	Ceremony string `cookie:"otogi_ceremony"`
}

// newPasskey is a passkey being registered.
type newPasskey struct {
	Name       string          `json:"name" minLength:"1" maxLength:"100" doc:"What to call it, such as Tundra's iPhone."`
	Credential json.RawMessage `json:"credential" doc:"The result of navigator.credentials.create."`
}

func registerSignIn(api huma.API, s *signIn) {
	tags := []string{"Sign-in"}
	type sessionOutput struct {
		Body struct {
			SignedIn bool   `json:"signed_in"`
			Passkey  string `json:"passkey,omitempty" doc:"The passkey this session signed in with."`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "get-session", Method: http.MethodGet, Path: "/api/auth/session", Summary: "Whether this browser is signed in", Tags: tags},
		func(ctx context.Context, in *SessionCookie) (*sessionOutput, error) {
			out := &sessionOutput{}
			if key, ok := s.who(ctx, in.Session); ok {
				out.Body.SignedIn, out.Body.Passkey = true, key.Name
			}
			return out, nil
		})
	begin := func(ctx context.Context, c ceremony, options any) (*optionsOutput, error) {
		cookie, err := s.cookie(ceremonyCookie, c, ceremonyAge)
		if err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}
		body, err := json.Marshal(options)
		if err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}
		return &optionsOutput{SetCookies: SetCookies{SetCookie: []http.Cookie{cookie}}, Body: body}, nil
	}
	readCeremony := func(value string) (ceremony, error) {
		var c ceremony
		if value == "" || s.cookies.Decode(ceremonyCookie, value, &c) != nil {
			return ceremony{}, huma.Error400BadRequest("no passkey ceremony is under way in this browser; start again")
		}
		return c, nil
	}
	signedIn := func(ctx context.Context, key []byte, clearCeremony bool) ([]http.Cookie, error) {
		cookie, err := s.cookie(sessionCookie, session{Passkey: key}, sessionAge)
		if err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}
		cookies := []http.Cookie{cookie}
		if clearCeremony {
			cookies = append(cookies, s.clear(ceremonyCookie))
		}
		return cookies, nil
	}
	// register finishes a registration ceremony and stores the passkey.
	register := func(ctx context.Context, c ceremony, in newPasskey) ([]http.Cookie, error) {
		user, _, err := s.operator(ctx)
		if err != nil {
			return nil, problem(err)
		}
		parsed, err := protocol.ParseCredentialCreationResponseBytes(in.Credential)
		if err != nil {
			s.record(ctx, "register", in.Name, err)
			return nil, huma.Error400BadRequest("the passkey's answer is malformed: " + err.Error())
		}
		credential, err := s.webauthn.CreateCredential(user, c.Data, parsed)
		if err != nil {
			s.record(ctx, "register", in.Name, err)
			return nil, huma.Error400BadRequest("the passkey was not accepted: " + err.Error())
		}
		if err := s.store.AddPasskey(ctx, in.Name, *credential); err != nil {
			return nil, problem(err)
		}
		s.record(ctx, "register", in.Name, nil)
		return signedIn(ctx, credential.ID, true)
	}
	registration := func(ctx context.Context, c ceremony) (*optionsOutput, error) {
		user, keys, err := s.operator(ctx)
		if err != nil {
			return nil, problem(err)
		}
		var exclude []protocol.CredentialDescriptor
		for _, k := range keys {
			exclude = append(exclude, k.Credential.Descriptor())
		}
		options, data, err := s.webauthn.BeginRegistration(user,
			webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired), webauthn.WithExclusions(exclude))
		if err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}
		c.Data = *data
		return begin(ctx, c, options)
	}

	huma.Register(api, huma.Operation{OperationID: "begin-sign-in", Method: http.MethodPost, Path: "/api/auth/login/begin", Summary: "Start signing in with a passkey", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*optionsOutput, error) {
			options, data, err := s.webauthn.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationPreferred))
			if err != nil {
				return nil, huma.Error500InternalServerError(err.Error())
			}
			return begin(ctx, ceremony{Data: *data}, options)
		})
	type signedInOutput struct {
		SetCookies
		Body struct {
			Passkey string `json:"passkey"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "finish-sign-in", Method: http.MethodPost, Path: "/api/auth/login/finish", Summary: "Sign in with the passkey's answer", Tags: tags},
		func(ctx context.Context, in *struct {
			CeremonyCookie
			Body json.RawMessage `doc:"The result of navigator.credentials.get."`
		},
		) (*signedInOutput, error) {
			c, err := readCeremony(in.Ceremony)
			if err != nil {
				return nil, err
			}
			parsed, err := protocol.ParseCredentialRequestResponseBytes(in.Body)
			if err != nil {
				s.record(ctx, "sign_in", "", err)
				return nil, huma.Error400BadRequest("the passkey's answer is malformed: " + err.Error())
			}
			user, keys, err := s.operator(ctx)
			if err != nil {
				return nil, problem(err)
			}
			_, credential, err := s.webauthn.ValidatePasskeyLogin(func(_, handle []byte) (webauthn.User, error) {
				if !bytes.Equal(handle, user.handle) {
					return nil, fmt.Errorf("a passkey of another site's user")
				}
				return user, nil
			}, c.Data, parsed)
			if err != nil {
				s.record(ctx, "sign_in", "", err)
				return nil, huma.Error401Unauthorized("the passkey was not accepted: " + err.Error())
			}
			if err := s.store.UsePasskey(ctx, *credential); err != nil {
				return nil, problem(err)
			}
			name := keys[slices.IndexFunc(keys, func(k panel.Passkey) bool { return bytes.Equal(k.Credential.ID, credential.ID) })].Name
			s.record(ctx, "sign_in", name, nil)
			cookies, err := signedIn(ctx, credential.ID, true)
			if err != nil {
				return nil, err
			}
			out := &signedInOutput{SetCookies: SetCookies{SetCookie: cookies}}
			out.Body.Passkey = name
			return out, nil
		})
	huma.Register(api, huma.Operation{OperationID: "sign-out", Method: http.MethodPost, Path: "/api/auth/logout", Summary: "Sign out", DefaultStatus: http.StatusNoContent, Tags: tags},
		func(ctx context.Context, in *SessionCookie) (*SetCookies, error) {
			if key, ok := s.who(ctx, in.Session); ok {
				s.record(ctx, "sign_out", key.Name, nil)
			}
			return &SetCookies{SetCookie: []http.Cookie{s.clear(sessionCookie)}}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "begin-enrollment", Method: http.MethodPost, Path: "/api/auth/enroll/begin", Summary: "Start registering a passkey from an enrollment link",
		Description: "The link comes from otogi passkey enroll on the server and works once, for 10 minutes.", Tags: tags},
		func(ctx context.Context, in *struct {
			Body struct {
				Token string `json:"token" minLength:"1"`
			}
		},
		) (*optionsOutput, error) {
			valid, err := s.store.Enrollment(ctx, in.Body.Token, false)
			if err != nil {
				return nil, problem(err)
			}
			if !valid {
				return nil, huma.Error403Forbidden("this enrollment link is used or expired; make another with otogi passkey enroll")
			}
			return registration(ctx, ceremony{Enrollment: in.Body.Token})
		})
	huma.Register(api, huma.Operation{OperationID: "finish-enrollment", Method: http.MethodPost, Path: "/api/auth/enroll/finish", Summary: "Register the passkey and sign in", Tags: tags},
		func(ctx context.Context, in *struct {
			CeremonyCookie
			Body newPasskey
		},
		) (*SetCookies, error) {
			c, err := readCeremony(in.Ceremony)
			if err != nil {
				return nil, err
			}
			if c.Enrollment == "" {
				return nil, huma.Error400BadRequest("this ceremony did not start from an enrollment link")
			}
			valid, err := s.store.Enrollment(ctx, c.Enrollment, true)
			if err != nil {
				return nil, problem(err)
			}
			if !valid {
				return nil, huma.Error403Forbidden("this enrollment link is used or expired; make another with otogi passkey enroll")
			}
			cookies, err := register(ctx, c, in.Body)
			return &SetCookies{SetCookie: cookies}, err
		})
	huma.Register(api, huma.Operation{OperationID: "list-passkeys", Method: http.MethodGet, Path: "/api/auth/passkeys", Summary: "The operator's passkeys", Tags: tags},
		func(ctx context.Context, in *SessionCookie) (*struct{ Body []PasskeyInfo }, error) {
			current, _ := s.who(ctx, in.Session)
			_, keys, err := s.operator(ctx)
			if err != nil {
				return nil, problem(err)
			}
			out := &struct{ Body []PasskeyInfo }{[]PasskeyInfo{}}
			for _, k := range keys {
				out.Body = append(out.Body, PasskeyInfo{
					ID: base64.RawURLEncoding.EncodeToString(k.Credential.ID), Name: k.Name, CreatedAt: k.CreatedAt, UsedAt: k.UsedAt,
					Current: bytes.Equal(k.Credential.ID, current.Credential.ID),
				})
			}
			return out, nil
		})
	huma.Register(api, huma.Operation{OperationID: "begin-passkey", Method: http.MethodPost, Path: "/api/auth/passkeys/begin", Summary: "Start registering another passkey", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*optionsOutput, error) {
			return registration(ctx, ceremony{})
		})
	huma.Register(api, huma.Operation{OperationID: "finish-passkey", Method: http.MethodPost, Path: "/api/auth/passkeys/finish", Summary: "Register another passkey", Tags: tags},
		func(ctx context.Context, in *struct {
			CeremonyCookie
			Body newPasskey
		},
		) (*SetCookies, error) {
			c, err := readCeremony(in.Ceremony)
			if err != nil {
				return nil, err
			}
			if c.Enrollment != "" {
				return nil, huma.Error400BadRequest("this ceremony started from an enrollment link")
			}
			cookies, err := register(ctx, c, in.Body)
			if err != nil {
				return nil, err
			}
			return &SetCookies{SetCookie: cookies[1:]}, nil // the session stays the one signed in
		})
	huma.Register(api, huma.Operation{OperationID: "remove-passkey", Method: http.MethodDelete, Path: "/api/auth/passkeys/{id}", Summary: "Remove a passkey",
		Description: "The last passkey cannot be removed: nobody could sign in.", DefaultStatus: http.StatusNoContent, Tags: tags},
		func(ctx context.Context, in *struct {
			ID string `path:"id" doc:"The credential ID, base64url."`
		},
		) (*struct{}, error) {
			id, err := base64.RawURLEncoding.DecodeString(in.ID)
			if err != nil {
				return nil, huma.Error400BadRequest("id must be base64url")
			}
			_, keys, err := s.operator(ctx)
			if err != nil {
				return nil, problem(err)
			}
			i := slices.IndexFunc(keys, func(k panel.Passkey) bool { return bytes.Equal(k.Credential.ID, id) })
			switch {
			case i < 0:
				return nil, huma.Error404NotFound("no such passkey")
			case len(keys) == 1:
				return nil, huma.Error409Conflict("this is the last passkey: register another before removing it")
			}
			if _, err := s.store.RemovePasskey(ctx, id); err != nil {
				return nil, problem(err)
			}
			s.record(ctx, "remove", keys[i].Name, nil)
			return nil, nil
		})
}
