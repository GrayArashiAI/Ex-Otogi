package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/capture"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/management"
	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/internal/kernel"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// apiHarness serves the management API over the real runtime, with no
// module on, and returns its kernel and a function that makes a request and
// checks the status of the response.
func apiHarness(t *testing.T, log *slog.Logger) (*kernel.Kernel, func(method, path, ifMatch, body string, wantCode int) (http.Header, string)) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	storage, err := modulestore.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	events, err := management.OpenEventLog(ctx, filepath.Join(dir, "eventlog.sqlite"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = events.Close() })
	settings := &moduleSettings{store: storage, kinds: moduleKinds, recorder: events, log: log, system: newSystem(new(slog.LevelVar), events, log), env: moduleEnv{
		llm: func(string) (llm.Provider, error) { return silentModel{}, nil },
	}}
	if err := settings.loadSections(ctx, Config{}); err != nil {
		t.Fatal(err)
	}
	k, err := kernel.New(kernel.Config{Storage: storage, Recorder: events}, nil, nil, capture.New(), log)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = k.Stop(ctx) })
	settings.kernel = k
	server, err := management.StartServer(management.ServerConfig{
		Addr: "127.0.0.1:0", Token: "secret", Events: events, Logger: log, Ready: func() bool { return true }, Shutdown: func() {},
		Status: func(context.Context) management.Status { return management.Status{} }, Modules: newModuleAPI(settings), System: newSystemAPI(settings), Secrets: newModuleAPI(settings),
		Media: func(context.Context, otogi.Destination, otogi.MediaSource) ([]byte, error) {
			return []byte("JPEG"), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Stop(ctx) })
	return k, func(method, path, ifMatch, body string, wantCode int) (http.Header, string) {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, method, "http://"+server.Addr()+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer secret")
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
		return resp.Header, string(data)
	}
}

// The management API over the real runtime: settings saved and applied,
// allowlists, and the chats the bot has seen with their conversations.
func TestManagementAPI(t *testing.T) {
	ctx := context.Background()
	k, do := apiHarness(t, slog.New(slog.DiscardHandler))
	contains := func(what, body string, want ...string) {
		t.Helper()
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Fatalf("%s: %s lacks %s", what, body, w)
			}
		}
	}

	// Settings: read with their schema, saved against their revision.
	header, body := do("GET", "/api/modules/echo/settings", "", "", 200)
	contains("echo settings", body, `"revision":0`, `"schema":{`)
	if header.Get("ETag") != `"0"` {
		t.Fatalf("ETag %q", header.Get("ETag"))
	}
	_, body = do("PUT", "/api/modules/notes/settings", `"0"`, `{"max_notes": "many"}`, 422)
	contains("invalid notes", body, `"location":"max_notes"`)
	do("PUT", "/api/modules/notes/settings", "", `{}`, 422) // If-Match is required
	header, _ = do("PUT", "/api/modules/notes/settings", `"0"`, `{"max_notes": 5}`, 200)
	if header.Get("ETag") != `"1"` {
		t.Fatalf("ETag after saving %q", header.Get("ETag"))
	}
	do("PUT", "/api/modules/notes/settings", `"0"`, `{"max_notes": 6}`, 412)
	do("PUT", "/api/modules/nope/settings", `"0"`, `{}`, 404)
	do("PATCH", "/api/modules/echo", "", `{"enabled": true}`, 200)
	_, body = do("PATCH", "/api/modules/chat", "", `{"enabled": true}`, 422) // never configured
	contains("chat without settings", body, "profiles")
	do("PUT", "/api/modules/chat/settings", `"0"`, `{"profiles": [{"name": "Luna", "keywords": ["luna"], "provider": "gemini", "model": "m"}]}`, 200)
	_, body = do("PATCH", "/api/modules/chat", "", `{"enabled": true}`, 200)
	contains("chat", body, `"running":true`, `"allowlists":[{"name":"Luna","title":"Luna"`)
	contains("chat ignores bots by default", body, `"ignore_bots":true`, `"handles":true`)
	_, body = do("PATCH", "/api/modules/chat", "", `{"ignore_bots": false}`, 200)
	contains("chat takes bots", body, `"ignore_bots":false`, `"running":true`)
	do("PATCH", "/api/modules/chat", "", `{}`, 422)
	do("PATCH", "/api/modules/nope", "", `{"ignore_bots": false}`, 404)
	_, body = do("GET", "/api/modules/notes/settings/revisions", "", "", 200)
	contains("notes history", body, `"revision":1`, `"by":"management API"`, `"more":false`)
	do("POST", "/api/modules/notes/settings/revisions/1/restore", `"1"`, "", 200)
	do("POST", "/api/modules/notes/settings/revisions/9/restore", `"2"`, "", 404)

	// A person's message, with a photo, and echo's answer.
	photo := otogi.Part{ID: "p1", Kind: otogi.PartMedia, Media: &otogi.Media{Role: otogi.MediaImage, MediaType: "image/jpeg", Source: otogi.MediaSource{Kind: otogi.MediaSourceOpaque}}}
	msg := otogi.TextMessage("hi")
	msg.Parts, msg.Roots = append(msg.Parts, photo), append(msg.Roots, "p1")
	done := make(chan otogi.Outcome, 1)
	err := k.Submit(ctx, otogi.Event{
		ID: "e1", Platform: "telegram", Account: "main", Conversation: otogi.Conversation{ID: "chat:1", Kind: otogi.ConversationGroup, Title: "Friends"},
		Actor: otogi.Actor{ID: "user:5", Name: "Alice"}, Action: otogi.ActionMessageCreated, Direction: otogi.DirectionIncoming, OccurredAt: time.Now(), Message: msg,
	}, func(o otogi.Outcome) { done <- o })
	if err != nil {
		t.Fatal(err)
	}
	<-done

	// Allowlists, with what the directory knows of each chat.
	do("PUT", "/api/modules/chat/allowlists/Luna/chats/telegram:chat:1", "", "", 204)
	do("PUT", "/api/modules/chat/allowlists/Luna/chats/G1", "", "", 400)
	do("PUT", "/api/modules/chat/allowlists/Mizuki/chats/telegram:chat:1", "", "", 404)
	_, body = do("GET", "/api/modules/chat/allowlists/Luna", "", "", 200)
	contains("Luna's chats", body, `"chat":"telegram:chat:1"`, `"added_by":"management API"`, `"title":"Friends"`)

	// Chats, their names, and their conversations.
	_, body = do("GET", "/api/chats", "", "", 200)
	contains("chats", body, `"id":"telegram:chat:1"`, `"lists":[{"module":"chat","list":"Luna"`)
	_, body = do("PATCH", "/api/chats/telegram:chat:1", "", `{"name": "Old friends"}`, 200)
	contains("named chat", body, `"name":"Old friends"`, `"days":[{"day":"`)
	do("GET", "/api/chats/telegram:chat:404", "", "", 404)
	_, body = do("GET", "/api/chats/telegram:chat:1/messages", "", "", 200)
	var page struct {
		Messages []management.ChatMessage `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 2 {
		t.Fatalf("messages %s", body)
	}
	answer, question := page.Messages[0], page.Messages[1]
	if !answer.Bot || answer.Module != "echo" || answer.Message.PlainText() != "echo: hi" || answer.Event != "e1" || len(answer.Records) == 0 || answer.Records[0].Kind != "module.completed" {
		t.Fatalf("the bot's message %+v", answer)
	}
	if question.Bot || question.Actor.Name != "Alice" || question.Message.PlainText() != "hi" {
		t.Fatalf("the person's message %+v", question)
	}
	_, media := do("GET", "/api/chats/telegram:chat:1/media/"+jsonInt(question.ID)+"/p1", "", "", 200)
	if !bytes.Equal([]byte(media), []byte("JPEG")) {
		t.Fatalf("media %q", media)
	}
	do("DELETE", "/api/modules/chat/allowlists/Luna/chats/telegram:chat:1", "", "", 204)

	_, body = do("GET", "/api/openapi.json", "", "", 200)
	contains("OpenAPI document", body, `"openapi":"3.1.0"`, `"/api/chats/{chat}/messages"`)
}

func jsonInt(n int64) string {
	data, _ := json.Marshal(n)
	return string(data)
}

// The secret routes over the real runtime. A secret's value is in no
// response, no record, and no log line, whatever the request looked like.
func TestSecretsAPI(t *testing.T) {
	var log bytes.Buffer
	_, request := apiHarness(t, slog.New(slog.NewTextHandler(&log, &slog.HandlerOptions{Level: slog.LevelDebug})))
	var answers []string
	do := func(method, path, ifMatch, body string, wantCode int) string {
		t.Helper()
		_, answer := request(method, path, ifMatch, body, wantCode)
		answers = append(answers, method+" "+path+": "+answer)
		return answer
	}
	contains := func(what, body string, want ...string) {
		t.Helper()
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Fatalf("%s: %s lacks %s", what, body, w)
			}
		}
	}
	const settings = `{"jev": {"api_key": "${OTOGI_TEST_JEV_KEY}"}}`

	contains("settings referring to a secret set nowhere", do("PUT", "/api/modules/decision/settings", `"0"`, settings, 422), "OTOGI_TEST_JEV_KEY")
	contains("a secret nothing uses yet", do("PUT", "/api/secrets/OTOGI_TEST_JEV_KEY", "", `{"value": "value-first"}`, 200), `"restarted":[]`)
	do("PUT", "/api/modules/decision/settings", `"0"`, settings, 200)
	do("PATCH", "/api/modules/decision", "", `{"enabled": true}`, 200)
	contains("a secret a running module uses", do("PUT", "/api/secrets/OTOGI_TEST_JEV_KEY", "", `{"value": "value-second"}`, 200), `"restarted":["decision"]`)
	contains("secrets", do("GET", "/api/secrets", "", "", 200), `"name":"OTOGI_TEST_JEV_KEY","source":"panel","by":"management API","at":"`, `"references":["decision"]`)

	// Requests that are refused do not get their values back.
	for _, c := range []struct {
		path, body string
		code       int
	}{
		{"/api/secrets/OTOGI_TEST_JEV_KEY", `{"value": ""}`, 422},
		{"/api/secrets/OTOGI_TEST_JEV_KEY", `{"value": ["value-in-a-list"]}`, 422},
		{"/api/secrets/OTOGI_TEST_JEV_KEY", `{"value": "value-with-more", "note": "value-noted"}`, 422},
		{"/api/secrets/OTOGI_TEST_JEV_KEY", `{"value": "value-cut-short`, 400},
		{"/api/secrets/OTOGI_TEST_JEV_KEY", `{"secret": "value-misnamed"}`, 422},
		{"/api/secrets/not-a-name", `{"value": "value-misplaced"}`, 422},
	} {
		do("PUT", c.path, "", c.body, c.code)
	}
	contains("deleting a secret in use", do("DELETE", "/api/secrets/OTOGI_TEST_JEV_KEY", "", "", 409), "decision")
	do("DELETE", "/api/secrets/OTOGI_TEST_NEVER_SET", "", "", 404)
	t.Setenv("OTOGI_TEST_JEV_KEY", "value-from-environment")
	contains("deleting a secret the environment has too", do("DELETE", "/api/secrets/OTOGI_TEST_JEV_KEY", "", "", 200), `"restarted":["decision"]`)
	contains("secrets", do("GET", "/api/secrets", "", "", 200), `{"name":"OTOGI_TEST_JEV_KEY","source":"environment","references":["decision"]}`)

	records := do("GET", "/api/events?kind=secret.changed", "", "", 200)
	contains("records", records, `"component":"secret:OTOGI_TEST_JEV_KEY"`, `"action":"set"`, `"action":"replaced"`, `"action":"deleted"`, `"by":"management API"`, `"restarted":["decision"]`)
	do("GET", "/api/events", "", "", 200)
	do("GET", "/api/modules/decision/settings", "", "", 200)
	do("GET", "/api/modules", "", "", 200)
	contains("the log", log.String(), "OTOGI_TEST_JEV_KEY")
	for _, text := range append(answers, log.String()) {
		if strings.Contains(text, "value-") {
			t.Fatalf("a secret's value is in %s", text)
		}
	}
}

// The section routes over the real runtime: the sections, their settings
// with a schema, saved against their revision, their history, and an
// earlier revision restored.
func TestSystemAPI(t *testing.T) {
	_, do := apiHarness(t, slog.New(slog.DiscardHandler))
	contains := func(what, body string, want ...string) {
		t.Helper()
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Fatalf("%s: %s lacks %s", what, body, w)
			}
		}
	}

	_, body := do("GET", "/api/system", "", "", 200)
	contains("sections", body, `{"id":"general","revision":0}`, `{"id":"llm","revision":0}`)
	header, body := do("GET", "/api/system/general/settings", "", "", 200)
	contains("general settings", body, `"module":"system:general"`, `"revision":0`, `"settings":{}`, `"schema":{`, `"log_level"`, `"x-order":["operators","command_prefixes","eventlog_retention","log_level"]`)
	if header.Get("ETag") != `"0"` {
		t.Fatalf("ETag %q", header.Get("ETag"))
	}
	_, body = do("GET", "/api/system/llm/settings", "", "", 200)
	contains("language model settings", body, `"format":"secret"`, `"$defs"`)

	do("PUT", "/api/system/general/settings", "", `{}`, 422) // If-Match is required
	_, body = do("PUT", "/api/system/general/settings", `"0"`, `{"log_level": "loud", "eventlog_retention": "soon"}`, 422)
	contains("invalid general settings", body, `"location":"log_level"`, `"location":"eventlog_retention"`)
	_, body = do("PUT", "/api/system/llm/settings", `"0"`, `{"gemini": {"api_key": "value-pasted"}}`, 422)
	contains("a pasted key", body, `"location":"gemini.api_key"`)
	if strings.Contains(body, "value-") {
		t.Fatalf("a refused key is in the answer: %s", body)
	}
	header, body = do("PUT", "/api/system/general/settings", `"0"`, `{"log_level": "debug"}`, 200)
	contains("saved", body, `"module":"system:general"`, `"revision":1`, `"settings":{"log_level":"debug"}`, `"by":"management API"`)
	if header.Get("ETag") != `"1"` {
		t.Fatalf("ETag after saving %q", header.Get("ETag"))
	}
	do("PUT", "/api/system/general/settings", `"0"`, `{"log_level": "warn"}`, 412)
	do("PUT", "/api/system/general/settings", `"1"`, `{"log_level": "warn"}`, 200)
	_, body = do("GET", "/api/system/general/settings/revisions", "", "", 200)
	contains("history", body, `"revision":2`, `"revision":1`, `"more":false`)
	do("POST", "/api/system/general/settings/revisions/1/restore", `"1"`, "", 412)
	header, body = do("POST", "/api/system/general/settings/revisions/1/restore", `"2"`, "", 200)
	contains("restored", body, `"revision":3`, `"settings":{"log_level":"debug"}`)
	if header.Get("ETag") != `"3"` {
		t.Fatalf("ETag after restoring %q", header.Get("ETag"))
	}
	do("POST", "/api/system/general/settings/revisions/9/restore", `"3"`, "", 404)
	_, body = do("GET", "/api/system", "", "", 200)
	contains("sections", body, `{"id":"general","revision":3}`)

	// Sections are not modules, and modules are not sections.
	for _, path := range []string{"/api/system/accounts/settings", "/api/system/echo/settings", "/api/modules/system:general/settings", "/api/system/echo/settings/revisions", "/api/modules/system:general/settings/revisions"} {
		do("GET", path, "", "", 404)
	}
	do("PUT", "/api/modules/system:general/settings", `"3"`, `{}`, 404)
	do("POST", "/api/modules/system:general/settings/revisions/1/restore", `"3"`, "", 404)
	_, body = do("GET", "/api/modules", "", "", 200)
	if strings.Contains(body, "system:") {
		t.Fatalf("a section is listed as a module: %s", body)
	}

	_, body = do("GET", "/api/events?kind=system.reconfigured", "", "", 200)
	contains("records", body, `"component":"system:general"`, `"revision":3`, `"by":"management API"`, `"restarted":[]`)
	_, body = do("GET", "/api/openapi.json", "", "", 200)
	contains("OpenAPI document", body, `"/api/system/{section}/settings/revisions/{revision}/restore"`)
}
