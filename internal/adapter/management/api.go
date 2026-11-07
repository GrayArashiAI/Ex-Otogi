package management

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Modules is the module runtime as the API sees it; the composition root
// implements it.
type Modules interface {
	// Modules lists every module compiled into the binary, by ID.
	Modules(ctx context.Context) ([]Module, error)
	// Settings returns a module's current revision and the JSON Schema of
	// its settings.
	Settings(ctx context.Context, module string) (Revision, map[string]any, error)
	// SaveSettings applies and stores a new revision of a module's settings,
	// based on the revision base. Nil enabled or settings keep the current.
	SaveSettings(ctx context.Context, module string, base int64, enabled *bool, settings json.RawMessage, by string) (Revision, error)
	// Revisions returns up to limit revisions older than before, newest
	// first.
	Revisions(ctx context.Context, module string, before int64, limit int) ([]Revision, error)
	// Revision returns one revision, or an error wrapping ErrNotFound.
	Revision(ctx context.Context, module string, revision int64) (Revision, error)
	// Allowlists returns every chat on every module's allowlists.
	Allowlists() []AllowedChat
	// SetAllowed adds a chat to a module's allowlist, or removes it.
	SetAllowed(ctx context.Context, module, list, chat string, allowed bool, by string) error
}

// ErrNotFound is what Modules returns for a module or revision that does
// not exist.
var ErrNotFound = errors.New("not found")

// StaleRevisionError is a change based on a revision that is no longer the
// current one.
type StaleRevisionError struct{ Current int64 }

func (e *StaleRevisionError) Error() string {
	return fmt.Sprintf("the settings changed meanwhile: the current revision is %d", e.Current)
}

// Module is a module compiled into the binary.
type Module struct {
	ID         string       `json:"id"`
	Enabled    bool         `json:"enabled" doc:"Whether its current revision turns it on."`
	Running    bool         `json:"running"`
	Revision   int64        `json:"revision" doc:"Its current settings revision; 0 when never saved."`
	Problem    string       `json:"problem,omitempty" doc:"Why its current settings do not build, when they do not; its allowlists are then unknown."`
	Allowlists []Allowlist  `json:"allowlists" nullable:"false"`
	Commands   []Command    `json:"commands" nullable:"false"`
	Counts     ModuleCounts `json:"counts" doc:"Its deliveries since the process started."`
}

// Allowlist is a module's list of the chats it answers in.
type Allowlist struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Chats       int    `json:"chats" doc:"How many chats are on it."`
	About       string `json:"about,omitempty" doc:"The module whose capability the list grants, when that is another module's, such as todou for the chat module's list of where agents may use todou's tools."`
}

// Command is a command a module handles.
type Command struct {
	Name        string `json:"name"`
	Usage       string `json:"usage,omitempty"`
	Description string `json:"description,omitempty"`
}

// ModuleCounts counts a module's deliveries by outcome.
type ModuleCounts struct {
	Completed int64 `json:"completed"`
	Slow      int64 `json:"slow" doc:"Completed, but past half the module's timeout."`
	Failed    int64 `json:"failed"`
	Panicked  int64 `json:"panicked"`
}

// Revision is one saved revision of a module's settings.
type Revision struct {
	Module   string          `json:"module"`
	Revision int64           `json:"revision"`
	Enabled  bool            `json:"enabled"`
	Settings json.RawMessage `json:"settings"`
	By       string          `json:"by,omitempty"`
	At       time.Time       `json:"at,omitzero"`
}

// AllowedChat is a chat on a module's allowlist.
type AllowedChat struct {
	Module string    `json:"module"`
	List   string    `json:"list"`
	Chat   string    `json:"chat"`
	At     time.Time `json:"added_at"`
	By     string    `json:"added_by"`
}

// callerKey keys who is calling, for records of changes.
type callerKey struct{}

// caller is who made a request: until panel sign-in, the management token.
func caller(ctx context.Context) string {
	if by, ok := ctx.Value(callerKey{}).(string); ok {
		return by
	}
	return "management API"
}

// problem turns an error from the runtime into an RFC 9457 response.
func problem(err error) error {
	var stale *StaleRevisionError
	var detailed interface{ ErrorDetails() []error }
	switch {
	case errors.As(err, &stale):
		return huma.Error412PreconditionFailed(err.Error())
	case errors.Is(err, ErrNotFound):
		return huma.Error404NotFound(err.Error())
	case errors.As(err, &detailed):
		return huma.Error422UnprocessableEntity(err.Error(), detailed.ErrorDetails()...)
	case errors.Is(err, otogi.ErrInvalid):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.Is(err, otogi.ErrUnavailable):
		return huma.Error503ServiceUnavailable(err.Error())
	}
	return huma.Error500InternalServerError(err.Error())
}

// etag and revisionOf carry a settings revision in ETag and If-Match.
func etag(revision int64) string { return strconv.Quote(strconv.FormatInt(revision, 10)) }

func revisionOf(ifMatch string) (int64, error) {
	n, err := strconv.ParseInt(strings.Trim(strings.TrimPrefix(ifMatch, "W/"), `"`), 10, 64)
	if err != nil || n < 0 {
		return 0, huma.Error400BadRequest(`If-Match must be the settings' ETag, such as "3"`)
	}
	return n, nil
}

// chatKey splits a chat as /whoami prints it: platform and conversation.
func chatKey(chat string) (platform, conversation string, err error) {
	platform, conversation, ok := strings.Cut(chat, ":")
	if !ok || platform == "" || conversation == "" {
		return "", "", huma.Error400BadRequest(fmt.Sprintf("chat %q: want <platform>:<conversation>, as /whoami prints it", chat))
	}
	return platform, conversation, nil
}

// ModulePath names a module in a route.
type ModulePath struct {
	Module string `path:"module" doc:"The module's ID."`
}

type settingsOutput struct {
	ETag string `header:"ETag"`
	Body struct {
		Revision
		Schema map[string]any `json:"schema" doc:"The JSON Schema of the settings."`
	}
}

type revisionOutput struct {
	ETag string `header:"ETag"`
	Body Revision
}

func registerModules(api huma.API, modules Modules) {
	find := func(ctx context.Context, id string) (Module, error) {
		all, err := modules.Modules(ctx)
		if err != nil {
			return Module{}, problem(err)
		}
		i := slices.IndexFunc(all, func(m Module) bool { return m.ID == id })
		if i < 0 {
			return Module{}, huma.Error404NotFound("no module " + id)
		}
		return all[i], nil
	}
	huma.Register(api, huma.Operation{OperationID: "list-modules", Method: http.MethodGet, Path: "/api/modules", Summary: "Every module compiled in", Tags: []string{"Modules"}},
		func(ctx context.Context, _ *struct{}) (*struct{ Body []Module }, error) {
			all, err := modules.Modules(ctx)
			if err != nil {
				return nil, problem(err)
			}
			return &struct{ Body []Module }{all}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "get-module", Method: http.MethodGet, Path: "/api/modules/{module}", Summary: "One module", Tags: []string{"Modules"}},
		func(ctx context.Context, in *ModulePath) (*struct{ Body Module }, error) {
			m, err := find(ctx, in.Module)
			return &struct{ Body Module }{m}, err
		})
	huma.Register(api, huma.Operation{OperationID: "switch-module", Method: http.MethodPatch, Path: "/api/modules/{module}", Summary: "Turn a module on or off",
		Description: "Saves a revision that changes only whether the module runs, and applies it.", Tags: []string{"Modules"}},
		func(ctx context.Context, in *struct {
			ModulePath
			IfMatch string `header:"If-Match" doc:"The ETag of the settings this changes; the current revision when absent."`
			Body    struct {
				Enabled bool `json:"enabled"`
			}
		},
		) (*struct{ Body Module }, error) {
			base, err := baseRevision(ctx, modules, in.Module, in.IfMatch)
			if err != nil {
				return nil, err
			}
			if _, err := modules.SaveSettings(ctx, in.Module, base, &in.Body.Enabled, nil, caller(ctx)); err != nil {
				return nil, problem(err)
			}
			m, err := find(ctx, in.Module)
			return &struct{ Body Module }{m}, err
		})
	huma.Register(api, huma.Operation{OperationID: "get-settings", Method: http.MethodGet, Path: "/api/modules/{module}/settings", Summary: "A module's current settings and their schema", Tags: []string{"Settings"}},
		func(ctx context.Context, in *ModulePath) (*settingsOutput, error) {
			r, schema, err := modules.Settings(ctx, in.Module)
			if err != nil {
				return nil, problem(err)
			}
			out := &settingsOutput{ETag: etag(r.Revision)}
			out.Body.Revision, out.Body.Schema = r, schema
			return out, nil
		})
	huma.Register(api, huma.Operation{OperationID: "put-settings", Method: http.MethodPut, Path: "/api/modules/{module}/settings", Summary: "Save and apply new settings",
		Description: "Validates the settings against their schema, builds the module from them, swaps it in while it runs, and stores the revision. Nothing is stored if any step fails.", Tags: []string{"Settings"}},
		func(ctx context.Context, in *struct {
			ModulePath
			IfMatch string `header:"If-Match" required:"true" doc:"The ETag of the settings this replaces."`
			Body    json.RawMessage
		},
		) (*revisionOutput, error) {
			base, err := revisionOf(in.IfMatch)
			if err != nil {
				return nil, err
			}
			r, err := modules.SaveSettings(ctx, in.Module, base, nil, in.Body, caller(ctx))
			if err != nil {
				return nil, problem(err)
			}
			return &revisionOutput{ETag: etag(r.Revision), Body: r}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "list-revisions", Method: http.MethodGet, Path: "/api/modules/{module}/settings/revisions", Summary: "A module's settings history, newest first", Tags: []string{"Settings"}},
		func(ctx context.Context, in *struct {
			ModulePath
			Before int64 `query:"before" minimum:"1" doc:"Revisions older than this one."`
			Limit  int   `query:"limit" minimum:"1" maximum:"100" default:"20"`
		},
		) (*struct {
			Body struct {
				Revisions []Revision `json:"revisions"`
				More      bool       `json:"more"`
			}
		}, error,
		) {
			before := in.Before
			if before == 0 {
				before = 1<<63 - 1
			}
			revisions, err := modules.Revisions(ctx, in.Module, before, in.Limit+1)
			if err != nil {
				return nil, problem(err)
			}
			out := &struct {
				Body struct {
					Revisions []Revision `json:"revisions"`
					More      bool       `json:"more"`
				}
			}{}
			out.Body.More = len(revisions) > in.Limit
			out.Body.Revisions = revisions[:min(len(revisions), in.Limit)]
			return out, nil
		})
	huma.Register(api, huma.Operation{OperationID: "restore-revision", Method: http.MethodPost, Path: "/api/modules/{module}/settings/revisions/{revision}/restore", Summary: "Restore earlier settings",
		Description: "Saves the settings of an earlier revision as a new revision and applies them. Whether the module runs is left as it is.", Tags: []string{"Settings"}},
		func(ctx context.Context, in *struct {
			ModulePath
			Revision int64  `path:"revision"`
			IfMatch  string `header:"If-Match" required:"true" doc:"The ETag of the settings this replaces."`
		},
		) (*revisionOutput, error) {
			base, err := revisionOf(in.IfMatch)
			if err != nil {
				return nil, err
			}
			old, err := modules.Revision(ctx, in.Module, in.Revision)
			if err != nil {
				return nil, problem(err)
			}
			r, err := modules.SaveSettings(ctx, in.Module, base, nil, old.Settings, caller(ctx))
			if err != nil {
				return nil, problem(err)
			}
			return &revisionOutput{ETag: etag(r.Revision), Body: r}, nil
		})
}

// baseRevision is the revision a change is based on: If-Match, or else the
// current one.
func baseRevision(ctx context.Context, modules Modules, module, ifMatch string) (int64, error) {
	if ifMatch != "" {
		return revisionOf(ifMatch)
	}
	r, _, err := modules.Settings(ctx, module)
	if err != nil {
		return 0, problem(err)
	}
	return r.Revision, nil
}

// ListedChat is a chat on an allowlist, with what the directory knows of it.
type ListedChat struct {
	AllowedChat
	Directory *Chat `json:"directory,omitempty" doc:"The chat as the bot has seen it; absent for a chat it has not."`
}

// ListPath names a module's allowlist in a route.
type ListPath struct {
	ModulePath
	List string `path:"list" doc:"The allowlist's name."`
}

// ListedChatPath names a chat on an allowlist in a route.
type ListedChatPath struct {
	ListPath
	Chat string `path:"chat" doc:"The chat as /whoami prints it, such as telegram:channel:123."`
}

func registerAllowlists(api huma.API, modules Modules, events *EventLog) {
	// list checks that the module declares the list.
	list := func(ctx context.Context, in ListPath) error {
		all, err := modules.Modules(ctx)
		if err != nil {
			return problem(err)
		}
		for _, m := range all {
			if m.ID == in.Module && slices.ContainsFunc(m.Allowlists, func(l Allowlist) bool { return l.Name == in.List }) {
				return nil
			}
		}
		return huma.Error404NotFound(fmt.Sprintf("module %s has no allowlist %s", in.Module, in.List))
	}
	huma.Register(api, huma.Operation{OperationID: "get-allowlist", Method: http.MethodGet, Path: "/api/modules/{module}/allowlists/{list}", Summary: "The chats on an allowlist", Tags: []string{"Allowlists"}},
		func(ctx context.Context, in *ListPath) (*struct {
			Body struct {
				Chats []ListedChat `json:"chats" nullable:"false"`
			}
		}, error,
		) {
			if err := list(ctx, *in); err != nil {
				return nil, err
			}
			chats, err := events.Chats(ctx)
			if err != nil {
				return nil, problem(err)
			}
			out := &struct {
				Body struct {
					Chats []ListedChat `json:"chats" nullable:"false"`
				}
			}{}
			out.Body.Chats = []ListedChat{}
			for _, a := range modules.Allowlists() {
				if a.Module != in.Module || a.List != in.List {
					continue
				}
				listed := ListedChat{AllowedChat: a}
				if i := slices.IndexFunc(chats, func(c Chat) bool { return c.Key() == a.Chat }); i >= 0 {
					c := chats[i]
					c.LastMessage = nil
					listed.Directory = &c
				}
				out.Body.Chats = append(out.Body.Chats, listed)
			}
			return out, nil
		})
	set := func(allowed bool) func(context.Context, *ListedChatPath) (*struct{}, error) {
		return func(ctx context.Context, in *ListedChatPath) (*struct{}, error) {
			if err := list(ctx, in.ListPath); err != nil {
				return nil, err
			}
			if _, _, err := chatKey(in.Chat); err != nil {
				return nil, err
			}
			if err := modules.SetAllowed(ctx, in.Module, in.List, in.Chat, allowed, caller(ctx)); err != nil {
				return nil, problem(err)
			}
			return nil, nil
		}
	}
	huma.Register(api, huma.Operation{OperationID: "allow-chat", Method: http.MethodPut, Path: "/api/modules/{module}/allowlists/{list}/chats/{chat}", Summary: "Add a chat to an allowlist",
		DefaultStatus: http.StatusNoContent, Tags: []string{"Allowlists"}}, set(true))
	huma.Register(api, huma.Operation{OperationID: "disallow-chat", Method: http.MethodDelete, Path: "/api/modules/{module}/allowlists/{list}/chats/{chat}", Summary: "Remove a chat from an allowlist",
		DefaultStatus: http.StatusNoContent, Tags: []string{"Allowlists"}}, set(false))
}

// SeenChat is a chat the bot has seen, with the allowlists it is on.
type SeenChat struct {
	Chat
	ID    string        `json:"id" doc:"The chat as /whoami prints it."`
	Lists []AllowedChat `json:"lists" nullable:"false" doc:"The allowlists it is on."`
}

// ChatPath names a chat in a route.
type ChatPath struct {
	Chat string `path:"chat" doc:"The chat as /whoami prints it, such as telegram:channel:123."`
}

func registerChats(api huma.API, modules Modules, events *EventLog, media func(context.Context, otogi.Destination, otogi.MediaSource) ([]byte, error)) {
	seen := func(ctx context.Context) ([]SeenChat, error) {
		chats, err := events.Chats(ctx)
		if err != nil {
			return nil, problem(err)
		}
		lists := modules.Allowlists()
		out := make([]SeenChat, len(chats))
		for i, c := range chats {
			out[i] = SeenChat{Chat: c, ID: c.Key(), Lists: []AllowedChat{}}
			for _, a := range lists {
				if a.Chat == c.Key() {
					out[i].Lists = append(out[i].Lists, a)
				}
			}
		}
		return out, nil
	}
	huma.Register(api, huma.Operation{OperationID: "list-chats", Method: http.MethodGet, Path: "/api/chats", Summary: "Every chat the bot has seen, most recently seen first", Tags: []string{"Chats"}},
		func(ctx context.Context, _ *struct{}) (*struct{ Body []SeenChat }, error) {
			chats, err := seen(ctx)
			return &struct{ Body []SeenChat }{chats}, err
		})
	one := func(ctx context.Context, chat string) (SeenChat, error) {
		if _, _, err := chatKey(chat); err != nil {
			return SeenChat{}, err
		}
		chats, err := seen(ctx)
		if err != nil {
			return SeenChat{}, err
		}
		i := slices.IndexFunc(chats, func(c SeenChat) bool { return c.ID == chat })
		if i < 0 {
			return SeenChat{}, huma.Error404NotFound("the bot has not seen chat " + chat)
		}
		return chats[i], nil
	}
	type chatOutput struct {
		Body struct {
			SeenChat
			Days       []ChatDay `json:"days" doc:"New messages per day over the last 7 days, days without any left out."`
			PeopleSeen []Person  `json:"people_seen" nullable:"false" doc:"The people seen sending messages in it, most recently seen first, up to 100."`
		}
	}
	withDays := func(ctx context.Context, c SeenChat) (*chatOutput, error) {
		days, err := events.ChatDays(ctx, c.Platform, c.Conversation, time.Now().AddDate(0, 0, -6))
		if err != nil {
			return nil, problem(err)
		}
		people, err := events.ChatPeople(ctx, c.Platform, c.Conversation, 100)
		if err != nil {
			return nil, problem(err)
		}
		out := &chatOutput{}
		out.Body.SeenChat, out.Body.Days, out.Body.PeopleSeen = c, days, people
		return out, nil
	}
	huma.Register(api, huma.Operation{OperationID: "get-chat", Method: http.MethodGet, Path: "/api/chats/{chat}", Summary: "One chat", Tags: []string{"Chats"}},
		func(ctx context.Context, in *ChatPath) (*chatOutput, error) {
			c, err := one(ctx, in.Chat)
			if err != nil {
				return nil, err
			}
			return withDays(ctx, c)
		})
	huma.Register(api, huma.Operation{OperationID: "name-chat", Method: http.MethodPatch, Path: "/api/chats/{chat}", Summary: "Name a chat",
		Description: "The name wins over the platform's title; an empty name clears it.", Tags: []string{"Chats"}},
		func(ctx context.Context, in *struct {
			ChatPath
			Body struct {
				Name string `json:"name" maxLength:"200"`
			}
		},
		) (*chatOutput, error) {
			platform, conversation, err := chatKey(in.Chat)
			if err != nil {
				return nil, err
			}
			found, err := events.NameChat(ctx, platform, conversation, strings.TrimSpace(in.Body.Name))
			if err != nil {
				return nil, problem(err)
			}
			if !found {
				return nil, huma.Error404NotFound("the bot has not seen chat " + in.Chat)
			}
			c, err := one(ctx, in.Chat)
			if err != nil {
				return nil, err
			}
			return withDays(ctx, c)
		})
	huma.Register(api, huma.Operation{OperationID: "list-chat-messages", Method: http.MethodGet, Path: "/api/chats/{chat}/messages", Summary: "A chat's conversation, newest first",
		Description: "People's messages and the bot's, as long as the event log keeps them. Each of the bot's names the module that sent it and that module's records for the event it answered.", Tags: []string{"Chats"}},
		func(ctx context.Context, in *struct {
			ChatPath
			Before int64 `query:"before" minimum:"1" doc:"Messages older than this message ID."`
			Limit  int   `query:"limit" minimum:"1" maximum:"500" default:"50"`
		},
		) (*struct {
			Body struct {
				Messages []ChatMessage `json:"messages"`
				More     bool          `json:"more"`
			}
		}, error,
		) {
			platform, conversation, err := chatKey(in.Chat)
			if err != nil {
				return nil, err
			}
			messages, more, err := events.ChatMessages(ctx, platform, conversation, in.Before, in.Limit)
			if err != nil {
				return nil, problem(err)
			}
			out := &struct {
				Body struct {
					Messages []ChatMessage `json:"messages"`
					More     bool          `json:"more"`
				}
			}{}
			out.Body.Messages, out.Body.More = messages, more
			return out, nil
		})
	huma.Register(api, huma.Operation{OperationID: "get-chat-media", Method: http.MethodGet, Path: "/api/chats/{chat}/media/{message}/{part}", Summary: "An image or file in a chat's conversation",
		Description: "What the bot sent is served from the event log; what people sent is fetched through the platform, so it is gone once the platform drops it.", Tags: []string{"Chats"}},
		func(ctx context.Context, in *struct {
			ChatPath
			Message int64  `path:"message" doc:"The message ID."`
			Part    string `path:"part" doc:"The media part's ID."`
		},
		) (*huma.StreamResponse, error) {
			platform, conversation, err := chatKey(in.Chat)
			if err != nil {
				return nil, err
			}
			from, m, found, err := events.ChatMedia(ctx, platform, conversation, in.Message, otogi.PartID(in.Part))
			if err != nil {
				return nil, problem(err)
			}
			if !found {
				return nil, huma.Error404NotFound("no such media in chat " + in.Chat)
			}
			data := m.Source.Inline
			if data == nil {
				if data, err = media(ctx, from, m.Source); err != nil {
					return nil, huma.Error502BadGateway("fetch the media from the platform: " + err.Error())
				}
			}
			return &huma.StreamResponse{Body: func(hc huma.Context) {
				hc.SetHeader("Content-Type", cmp.Or(m.MediaType, "application/octet-stream"))
				hc.SetHeader("Cache-Control", "private, max-age=86400")
				_, _ = hc.BodyWriter().Write(data) // the client is gone if this fails
			}}, nil
		})
}

// Status is the process's health.
type Status struct {
	Version   string         `json:"version"`
	StartedAt time.Time      `json:"started_at"`
	Ready     bool           `json:"ready" doc:"Whether every driver is ready."`
	Drivers   []DriverStatus `json:"drivers" nullable:"false"`
	Kernel    KernelStatus   `json:"kernel"`
	EventLog  EventLogStatus `json:"event_log"`
	Running   []Running      `json:"running" nullable:"false" doc:"Deliveries modules are handling now, longest running first."`
}

// DriverStatus is one platform account's driver.
type DriverStatus struct {
	Platform     string    `json:"platform"`
	Account      string    `json:"account"`
	Ready        bool      `json:"ready"`
	Error        string    `json:"error,omitempty"`
	LastEvent    time.Time `json:"last_event,omitzero" doc:"When the event log last received an event from it."`
	PendingInbox int       `json:"pending_inbox" doc:"Updates stored and not yet handled."`
	FailedInbox  int       `json:"failed_inbox" doc:"Updates that could not be handled."`
	Gaps         int       `json:"gaps" doc:"Times the platform said updates were missed."`
	Details      any       `json:"details" doc:"Everything the driver reports, as it reports it."`
}

// KernelStatus is the kernel's load.
type KernelStatus struct {
	State       string `json:"state"`
	InFlight    int    `json:"in_flight" doc:"Events admitted and not yet finished."`
	MaxInFlight int    `json:"max_in_flight"`
	Workers     int    `json:"workers"`
}

// EventLogStatus is the event log's health and retention.
type EventLogStatus struct {
	Health
	RetentionMS int64 `json:"retention_ms"`
}
