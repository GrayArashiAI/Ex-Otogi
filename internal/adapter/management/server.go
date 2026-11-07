package management

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// ServerConfig configures the management HTTP API.
type ServerConfig struct {
	// Addr is the listen address, for example "127.0.0.1:8090".
	Addr string
	// Token, if set, is required as a bearer token on /api routes. It must be
	// set when Addr is not a loopback address.
	Token string
	// Events is the event log to query.
	Events *EventLog
	// Status returns the process's health for GET /api/status.
	Status func(ctx context.Context) Status
	// Ready reports whether the process should receive work (GET /readyz).
	Ready func() bool
	// Shutdown starts a graceful shutdown (POST /api/shutdown).
	Shutdown func()
	// Modules serves the module, allowlist, and chat routes; without it
	// they are absent.
	Modules Modules
	// Media fetches media people sent through the platform's driver.
	Media func(context.Context, otogi.Destination, otogi.MediaSource) ([]byte, error)
	// Version is the build, as the OpenAPI document names it.
	Version string
	// PublicURL is the panel's origin, such as https://otogi.example.com,
	// where the identity provider sends browsers back. Empty turns panel
	// sign-in off.
	PublicURL string
	// OIDC is the identity provider the panel signs in with; required with
	// PublicURL.
	OIDC OIDC
	// CookieKey signs and encrypts session cookies (see LoadCookieKey);
	// required with PublicURL.
	CookieKey []byte
	// Panel is the built control panel, served at every path outside /api/.
	Panel fs.FS
	// Logger receives server errors.
	Logger *slog.Logger
}

// Server is a running management API.
type Server struct {
	http *http.Server
	ln   net.Listener
	done chan error
}

// StartServer listens on cfg.Addr and serves the API until Stop.
func StartServer(cfg ServerConfig) (*Server, error) {
	if cfg.Token == "" && !loopback(cfg.Addr) {
		return nil, fmt.Errorf("management: %s is not a loopback address, so management.token is required", cfg.Addr)
	}
	handler, err := newHandler(cfg)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return nil, fmt.Errorf("management: listen: %w", err)
	}
	s := &Server{
		http: &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second},
		ln:   ln,
		done: make(chan error, 1),
	}
	go func() { s.done <- s.http.Serve(ln) }()
	cfg.Logger.Info("management: listening", "addr", ln.Addr().String())
	return s, nil
}

// Addr returns the address the server listens on.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Stop shuts the server down gracefully within ctx.
func (s *Server) Stop(ctx context.Context) error {
	if err := s.http.Shutdown(ctx); err != nil {
		return fmt.Errorf("management: shutdown: %w", err)
	}
	if err := <-s.done; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("management: serve: %w", err)
	}
	return nil
}

func loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func newHandler(cfg ServerConfig) (http.Handler, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "alive"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if cfg.Ready() {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not ready"})
	})
	var signIn *signIn
	open := func(string) bool { return false }
	if cfg.PublicURL != "" {
		var err error
		if signIn, err = newSignIn(cfg.OIDC, cfg.PublicURL, cfg.CookieKey, cfg.Events); err != nil {
			return nil, err
		}
		open = signInRoutes
	}
	apiMux, _ := newAPI(cfg, signIn, false)
	mux.Handle("/api/", guard(cfg.Token, signIn, open, apiMux))
	mux.Handle("/", spa(cfg.Panel))
	return mux, nil
}

// OpenAPI returns the API's OpenAPI document with every route, for clients
// to generate their types from.
func OpenAPI() ([]byte, error) {
	_, api := newAPI(ServerConfig{}, nil, true)
	data, err := json.MarshalIndent(api.OpenAPI(), "", "  ")
	if err != nil {
		return nil, fmt.Errorf("management: encode OpenAPI: %w", err)
	}
	return data, nil
}

// newAPI registers the routes cfg and signIn serve, or with every, all of
// them (for the document only).
func newAPI(cfg ServerConfig, signIn *signIn, every bool) (*http.ServeMux, huma.API) {
	apiMux := http.NewServeMux()
	config := huma.DefaultConfig("Ex-Otogi management API", cmp.Or(cfg.Version, "dev"))
	config.OpenAPIPath, config.SchemasPath, config.DocsPath = "/api/openapi", "/api/schemas", ""
	config.CreateHooks = nil // no $schema links in bodies
	api := humago.New(apiMux, config)
	huma.Register(api, huma.Operation{OperationID: "get-status", Method: http.MethodGet, Path: "/api/status", Summary: "The process's health", Tags: []string{"Status"}},
		func(ctx context.Context, _ *struct{}) (*struct{ Body Status }, error) {
			return &struct{ Body Status }{cfg.Status(ctx)}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "list-events", Method: http.MethodGet, Path: "/api/events", Summary: "Event log records, newest first", Tags: []string{"Event log"}},
		func(ctx context.Context, in *eventsInput) (*struct{ Body Page }, error) {
			if in.Before != 0 && in.After != 0 {
				return nil, huma.Error400BadRequest("before and after are exclusive")
			}
			page, err := cfg.Events.Query(ctx, Query{
				Kind: in.Kind, Component: in.Component, Event: in.Event, Platform: in.Platform, Account: in.Account, Conversation: in.Conversation,
				Outcome: in.Outcome, Text: in.Text, Since: in.Since, Until: in.Until, Before: in.Before, After: in.After, Limit: in.Limit,
			})
			if err != nil {
				return nil, problem(err)
			}
			return &struct{ Body Page }{page}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "get-trace", Method: http.MethodGet, Path: "/api/events/{id}/trace", Summary: "Every record of one event, in order", Tags: []string{"Event log"}},
		func(ctx context.Context, in *struct {
			ID string `path:"id" doc:"The event ID."`
		},
		) (*struct {
			Body struct {
				Records []Entry `json:"records" nullable:"false"`
			}
		}, error,
		) {
			entries, err := cfg.Events.Trace(ctx, in.ID)
			if err != nil {
				return nil, problem(err)
			}
			out := &struct {
				Body struct {
					Records []Entry `json:"records" nullable:"false"`
				}
			}{}
			out.Body.Records = entries
			return out, nil
		})
	huma.Register(api, huma.Operation{OperationID: "shutdown", Method: http.MethodPost, Path: "/api/shutdown", Summary: "Shut the process down gracefully", DefaultStatus: http.StatusAccepted, Tags: []string{"Status"}},
		func(context.Context, *struct{}) (*struct{}, error) {
			cfg.Shutdown()
			return nil, nil
		})
	if cfg.Modules != nil || every {
		registerModules(api, cfg.Modules)
		registerAllowlists(api, cfg.Modules, cfg.Events)
		registerChats(api, cfg.Modules, cfg.Events, cfg.Media)
	}
	if signIn != nil || every {
		registerSignIn(api, signIn)
	}
	return apiMux, api
}

// eventsInput filters and pages event log records.
type eventsInput struct {
	Kind         string    `query:"kind"`
	Component    string    `query:"component"`
	Event        string    `query:"event"`
	Platform     string    `query:"platform"`
	Account      string    `query:"account"`
	Conversation string    `query:"conversation"`
	Outcome      string    `query:"outcome"`
	Text         string    `query:"q" doc:"Text in the payload."`
	Since        time.Time `query:"since"`
	Until        time.Time `query:"until"`
	Before       int64     `query:"before" minimum:"1" doc:"Records older than this record ID."`
	After        int64     `query:"after" minimum:"1" doc:"Records newer than this record ID, to poll."`
	Limit        int       `query:"limit" minimum:"1" maximum:"500" default:"100"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body) // the client is gone if this fails
}
