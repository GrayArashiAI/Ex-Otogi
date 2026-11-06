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

// The management API over the real runtime: settings saved and applied,
// allowlists, and the chats the bot has seen with their conversations.
func TestManagementAPI(t *testing.T) {
	ctx := context.Background()
	dir, quiet := t.TempDir(), slog.New(slog.DiscardHandler)
	storage, err := modulestore.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	events, err := management.OpenEventLog(ctx, filepath.Join(dir, "eventlog.sqlite"), quiet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = events.Close() })
	settings := &moduleSettings{store: storage, kinds: moduleKinds, recorder: events, log: quiet, env: moduleEnv{
		llm: func(string) (llm.Provider, error) { return silentModel{}, nil },
	}}
	k, err := kernel.New(kernel.Config{Storage: storage, Recorder: events}, nil, nil, capture.New(), quiet)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = k.Stop(ctx) })
	settings.kernel = k
	server, err := management.StartServer(management.ServerConfig{
		Addr: "127.0.0.1:0", Token: "secret", Events: events, Logger: quiet, Ready: func() bool { return true }, Shutdown: func() {},
		Status: func(context.Context) any { return nil }, Modules: moduleAPI{settings},
		Media: func(context.Context, otogi.Destination, otogi.MediaSource) ([]byte, error) {
			return []byte("JPEG"), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Stop(ctx) })

	do := func(method, path, ifMatch, body string, wantCode int) (http.Header, string) {
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
	_, body = do("GET", "/api/modules/notes/settings/revisions", "", "", 200)
	contains("notes history", body, `"revision":1`, `"by":"management API"`, `"more":false`)
	do("POST", "/api/modules/notes/settings/revisions/1/restore", `"1"`, "", 200)
	do("POST", "/api/modules/notes/settings/revisions/9/restore", `"2"`, "", 404)

	// A person's message, with a photo, and echo's answer.
	photo := otogi.Part{ID: "p1", Kind: otogi.PartMedia, Media: &otogi.Media{Role: otogi.MediaImage, MediaType: "image/jpeg", Source: otogi.MediaSource{Kind: otogi.MediaSourceOpaque}}}
	msg := otogi.TextMessage("hi")
	msg.Parts, msg.Roots = append(msg.Parts, photo), append(msg.Roots, "p1")
	done := make(chan otogi.Outcome, 1)
	err = k.Submit(ctx, otogi.Event{
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
