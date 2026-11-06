package qq

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	qqstore "github.com/TundraWork/Ex-Otogi/internal/adapter/qq/store"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

func newDirectory(t *testing.T, f *fakeQQ, rec *recorder) (*directory, *qqstore.Store) {
	t.Helper()
	s, err := qqstore.Open(context.Background(), filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return &directory{
		store: s, api: &api{base: f.srv.URL, appID: "1001", secret: "secret", http: http.DefaultClient, now: time.Now},
		log: quiet, record: func(kind, outcome string, payload any) {
			rec.Record(context.Background(), otogi.Record{Kind: kind + ":" + outcome, Payload: payload})
		},
		now: time.Now, refused: map[string]bool{}, retry: map[string]time.Time{},
	}, s
}

func groupEvent(actor string) otogi.Event {
	return otogi.Event{Conversation: otogi.Conversation{ID: "group:G1", Kind: otogi.ConversationGroup}, Actor: otogi.Actor{ID: otogi.ActorID("user:" + actor)}}
}

// A whitelisted bot learns a group's details and its members' names, once a
// day; events then carry them.
func TestDirectoryLooksUpGroups(t *testing.T) {
	f := newFakeQQ(t)
	f.handle = func(w http.ResponseWriter, r *http.Request, _ string) bool {
		switch {
		case r.URL.Path == "/v2/groups/G1/info":
			_, _ = io.WriteString(w, `{"group_openid":"G1","group_name":"茶话会","group_finger_memo":"喝茶聊天","group_member_num":42}`)
		case r.URL.Path == "/v2/groups/G1/members" && r.URL.Query().Get("cursor") == "":
			_, _ = io.WriteString(w, `{"members":[{"member_openid":"M1","username":"Alice"}],"next_cursor":"c2"}`)
		case r.URL.Path == "/v2/groups/G1/members":
			_, _ = io.WriteString(w, `{"members":[{"member_openid":"M2","username":"Bob"}],"next_cursor":""}`)
		default:
			return false
		}
		return true
	}
	rec := &recorder{}
	d, _ := newDirectory(t, f, rec)
	for range 2 {
		e := groupEvent("M2")
		if err := d.complete(context.Background(), &e); err != nil {
			t.Fatal(err)
		}
		if e.Conversation.Title != "茶话会" || e.Conversation.Description != "喝茶聊天" || e.Conversation.Members != 42 || e.Actor.Name != "Bob" {
			t.Fatalf("event %+v", e)
		}
	}
	lookups := 0
	for _, r := range f.requests {
		if strings.Contains(r, "/info") {
			lookups++
		}
	}
	if lookups != 1 || !slices.Contains(rec.kinds(), "driver.lookup:ok") {
		t.Fatalf("%d lookups, records %v", lookups, rec.kinds())
	}
}

// QQ refuses bots that are not whitelisted: that is recorded once, and the
// API is not asked again; names from messages still fill events.
func TestDirectoryWithoutWhitelist(t *testing.T) {
	f := newFakeQQ(t)
	f.handle = func(w http.ResponseWriter, r *http.Request, _ string) bool {
		if !strings.HasPrefix(r.URL.Path, "/v2/groups/") {
			return false
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"err_code":11253,"message":"该接口仅白名单机器人可用"}`)
		return true
	}
	rec := &recorder{}
	d, s := newDirectory(t, f, rec)
	if err := s.SetPeerName(context.Background(), "user:M1", "Alice"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		e := groupEvent("M1")
		if err := d.complete(context.Background(), &e); err != nil {
			t.Fatal(err)
		}
		if e.Conversation.Title != "" || e.Actor.Name != "Alice" {
			t.Fatalf("event %+v", e)
		}
	}
	asked := 0
	for _, r := range f.requests {
		if strings.Contains(r, "/v2/groups/") {
			asked++
		}
	}
	if asked != 1 || !slices.Equal(rec.kinds(), []string{"driver.lookup:skipped"}) {
		t.Fatalf("asked %d times, records %v", asked, rec.kinds())
	}
}
