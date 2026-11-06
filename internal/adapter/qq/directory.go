package qq

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	qqstore "github.com/TundraWork/Ex-Otogi/internal/adapter/qq/store"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

const (
	// lookUpEvery is how often a group's details are looked up again.
	lookUpEvery = 24 * time.Hour
	// retryLookUp is how long a failed lookup waits before the next try.
	retryLookUp = 10 * time.Minute
	// memberPages bounds a member list lookup; QQ gives 30 members a page.
	memberPages = 20
	// codeWhitelisted is QQ's answer to an API it opens only to whitelisted
	// bots, as it does group info and member lists.
	codeWhitelisted = 11253
)

// directory completes events with what is known of their users and groups:
// names the mapper learned from messages, and a group's name, description,
// and member count from QQ's group info API. QQ opens that API (and the
// member list) only to whitelisted bots; for others the first refusal is
// recorded and the API is not asked again until restart.
type directory struct {
	store  *qqstore.Store
	api    *api
	log    *slog.Logger
	record func(kind, outcome string, payload any)
	now    func() time.Time

	mu      sync.Mutex
	refused map[string]bool      // API -> QQ refused it (not whitelisted)
	retry   map[string]time.Time // group -> when to try a failed lookup again
}

// complete fills in the conversation's and the sender's names, and a
// group's details, where the event has none.
func (d *directory) complete(ctx context.Context, e *otogi.Event) error {
	conversation := string(e.Conversation.ID)
	if e.Conversation.Kind == otogi.ConversationGroup {
		if err := d.lookUp(ctx, conversation); err != nil {
			return err
		}
	}
	c, err := d.store.Peer(ctx, conversation)
	if err != nil {
		return err
	}
	if e.Conversation.Title == "" {
		e.Conversation.Title = c.Name
	}
	if e.Conversation.Description == "" {
		e.Conversation.Description = c.Description
	}
	if e.Conversation.Members == 0 {
		e.Conversation.Members = c.Members
	}
	if e.Actor.ID != "" && e.Actor.Name == "" {
		a, err := d.store.Peer(ctx, string(e.Actor.ID))
		if err != nil {
			return err
		}
		e.Actor.Name = a.Name
	}
	return nil
}

// lookUp refreshes a group's details once a day, and learns its members'
// names. Only a store failure is returned: a failed lookup leaves the
// group as it was known, and is recorded.
func (d *directory) lookUp(ctx context.Context, group string) error {
	openid := strings.TrimPrefix(group, "group:")
	known, err := d.store.Peer(ctx, group)
	if err != nil {
		return err
	}
	now := d.now()
	d.mu.Lock()
	skip := d.refused["info"] || now.Sub(known.LookedUp) < lookUpEvery || now.Before(d.retry[group])
	d.mu.Unlock()
	if skip {
		return nil
	}
	lookCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var info struct {
		Name    string `json:"group_name"`
		Memo    string `json:"group_finger_memo"`
		Members int    `json:"group_member_num"`
	}
	if err := d.api.call(lookCtx, http.MethodGet, "/v2/groups/"+url.PathEscape(openid)+"/info", nil, &info); err != nil {
		d.failed("info", group, err)
		return nil
	}
	if err := d.store.SetGroupInfo(ctx, group, qqstore.Peer{Name: info.Name, Description: info.Memo, Members: info.Members, LookedUp: now}); err != nil {
		return err
	}
	learned, err := d.members(lookCtx, ctx, openid)
	if err != nil {
		return err
	}
	d.record("driver.lookup", "ok", map[string]any{"group": group, "name": info.Name, "members": info.Members, "member_names": learned})
	return nil
}

// members learns the names of a group's members from its member list, and
// returns how many it learned.
func (d *directory) members(lookCtx, ctx context.Context, openid string) (int, error) {
	d.mu.Lock()
	refused := d.refused["members"]
	d.mu.Unlock()
	if refused {
		return 0, nil
	}
	learned, cursor := 0, ""
	for range memberPages {
		var page struct {
			Members []struct {
				OpenID   string `json:"member_openid"`
				Username string `json:"username"`
			} `json:"members"`
			Next string `json:"next_cursor"`
		}
		path := "/v2/groups/" + url.PathEscape(openid) + "/members?cursor=" + url.QueryEscape(cursor)
		if err := d.api.call(lookCtx, http.MethodGet, path, nil, &page); err != nil {
			d.failed("members", "group:"+openid, err)
			return learned, nil
		}
		for _, m := range page.Members {
			if m.OpenID != "" && m.Username != "" {
				if err := d.store.SetPeerName(ctx, "user:"+m.OpenID, m.Username); err != nil {
					return learned, err
				}
				learned++
			}
		}
		if page.Next == "" {
			break
		}
		cursor = page.Next
	}
	return learned, nil
}

// failed records a lookup that did not work: QQ refusing a bot that is not
// whitelisted, once, or any other failure, to be tried again later.
func (d *directory) failed(api, group string, err error) {
	var e *apiError
	d.mu.Lock()
	defer d.mu.Unlock()
	if errors.As(err, &e) && e.Code == codeWhitelisted {
		if !d.refused[api] {
			d.refused[api] = true
			d.log.Info("qq: group "+api+" is open only to whitelisted bots; names come from messages alone", "error", err)
			d.record("driver.lookup", "skipped", map[string]any{"api": api, "group": group, "reason": "QQ opens it only to whitelisted bots", "error": err.Error()})
		}
		return
	}
	d.retry[group] = d.now().Add(retryLookUp)
	d.log.Warn("qq: look up group "+api, "group", group, "error", err)
	d.record("driver.lookup", "error", map[string]any{"api": api, "group": group, "error": fmt.Sprint(err)})
}
