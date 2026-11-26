package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// With no revision of the accounts or the process settings, a start runs no
// accounts, with the process defaults.
func TestAFreshInstallHasNoAccounts(t *testing.T) {
	ctx := context.Background()
	s, _, _ := sectionsHarness(t, openStore(t), slog.New(slog.DiscardHandler))
	for _, id := range []otogi.ModuleID{accountsSection, processSection} {
		if cur, _ := s.current(ctx, id); cur.Revision != 0 {
			t.Fatalf("%s: %+v", id, cur)
		}
	}
	telegram, qq, err := s.startAccounts(ctx, "/srv/otogi")
	if err != nil || len(telegram) != 0 || len(qq) != 0 {
		t.Fatalf("accounts %+v %+v, %v", telegram, qq, err)
	}
	if p, err := s.startProcess(ctx); err != nil || p != (processSettings{}) {
		t.Fatalf("process %+v, %v", p, err)
	}
}

// Saving the accounts or the process settings validates and stores them,
// and changes nothing the process runs with: the sections say a restart is
// pending. The next start runs with what was saved.
func TestAccountsAndProcessApplyOnTheNextStart(t *testing.T) {
	ctx := context.Background()
	storage := openStore(t)
	t.Setenv("OTOGI_TEST_APP_HASH", "value-from-environment")
	dataDir := filepath.Join("/srv", "otogi")
	s, rec, _ := sectionsHarness(t, storage, slog.New(slog.DiscardHandler))
	sectionsNow := func(s *moduleSettings) string {
		t.Helper()
		all, err := newSystemAPI(s).Sections(ctx)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(all)
		return string(data)
	}
	if got := sectionsNow(s); got != `[{"id":"accounts","revision":0,"applies":"restart","running":0},{"id":"general","revision":0,"applies":"now","running":0},{"id":"llm","revision":0,"applies":"now","running":0},{"id":"process","revision":0,"applies":"restart","running":0}]` {
		t.Fatalf("sections after the start: %s", got)
	}

	for name, refused := range map[string]string{
		"an account twice":        `{"telegram": [{"account": "a", "app_id": 1, "app_hash": "${OTOGI_TEST_APP_HASH}"}, {"account": "a", "app_id": 2, "app_hash": "${OTOGI_TEST_APP_HASH}"}]}`,
		"no account name":         `{"qq": [{"account": "", "app_id": "1", "app_secret": "${OTOGI_TEST_APP_HASH}"}]}`,
		"a secret written out":    `{"qq": [{"account": "a", "app_id": "1", "app_secret": "value-pasted"}]}`,
		"a secret set nowhere":    `{"qq": [{"account": "a", "app_id": "1", "app_secret": "${OTOGI_TEST_UNSET}"}]}`,
		"no app id":               `{"telegram": [{"account": "a", "app_hash": "${OTOGI_TEST_APP_HASH}"}]}`,
		"negative upload threads": `{"telegram": [{"account": "a", "app_id": 1, "app_hash": "${OTOGI_TEST_APP_HASH}", "upload_threads": -1}]}`,
	} {
		if _, err := s.save(ctx, accountsSection, 0, nil, json.RawMessage(refused), "Tundra"); !errors.Is(err, otogi.ErrInvalid) || strings.Contains(err.Error(), "value-") {
			t.Fatalf("%s: %v, want ErrInvalid without the value", name, err)
		}
	}
	for name, refused := range map[string]string{
		"negative workers": `{"workers": -1}`,
		"a log format":     `{"log_format": "xml"}`,
		"a duration":       `{"shutdown_timeout": "soon"}`,
	} {
		if _, err := s.save(ctx, processSection, 0, nil, json.RawMessage(refused), "Tundra"); !errors.Is(err, otogi.ErrInvalid) {
			t.Fatalf("%s: %v, want ErrInvalid", name, err)
		}
	}

	if _, err := s.save(ctx, accountsSection, 0, nil, json.RawMessage(`{"telegram": [{"account": "main", "app_id": 7, "app_hash": "${OTOGI_TEST_APP_HASH}", "session": "/elsewhere/session.json"}], "qq": [{"account": "bot", "app_id": "1", "app_secret": "${OTOGI_TEST_APP_HASH}"}]}`), "Tundra"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.save(ctx, processSection, 0, nil, json.RawMessage(`{"workers": 8, "handler_timeout": "90s"}`), "Tundra"); err != nil {
		t.Fatal(err)
	}
	if got := s.kernel.Stats().Workers; got != 32 { // the harness's kernel, which no save may touch
		t.Fatalf("workers of the running kernel: %d", got)
	}
	if got := sectionsNow(s); !strings.Contains(got, `{"id":"accounts","revision":1,"applies":"restart","running":0}`) || !strings.Contains(got, `{"id":"process","revision":1,"applies":"restart","running":0}`) {
		t.Fatalf("sections after saving: %s", got)
	}
	var saved []string
	for _, r := range rec.list {
		if payload := r.Payload.(map[string]any); r.Kind == "system.reconfigured" && payload["by"] == "Tundra" {
			saved = append(saved, r.Component)
		}
	}
	if strings.Join(saved, " ") != "system:accounts system:process" {
		t.Fatalf("records of the saves: %q", saved)
	}

	// The next start.
	s, _, _ = sectionsHarness(t, storage, slog.New(slog.DiscardHandler))
	if got := sectionsNow(s); !strings.Contains(got, `{"id":"accounts","revision":1,"applies":"restart","running":1}`) || !strings.Contains(got, `{"id":"process","revision":1,"applies":"restart","running":1}`) {
		t.Fatalf("sections after the next start: %s", got)
	}
	telegram, qq, err := s.startAccounts(ctx, dataDir)
	if err != nil || len(telegram) != 1 || telegram[0] != (TelegramAccount{
		Account: "main", AppID: 7, AppHash: "value-from-environment", UploadThreads: 8,
		Session: "/elsewhere/session.json", Database: "/srv/otogi/telegram/main/state.sqlite",
	}) {
		t.Fatalf("telegram accounts of the next start: %+v, %v", telegram, err)
	}
	if len(qq) != 1 || qq[0] != (QQAccount{Account: "bot", AppID: "1", AppSecret: "value-from-environment", Database: "/srv/otogi/qq/bot/state.sqlite"}) {
		t.Fatalf("accounts of the next start: %+v, %v", qq, err)
	}
	if p, err := s.startProcess(ctx); err != nil || p.Workers != 8 || time.Duration(p.HandlerTimeout) != 90*time.Second {
		t.Fatalf("process settings of the next start: %+v, %v", p, err)
	}
}

func TestAnAccountsNameCannotBeChanged(t *testing.T) {
	ctx := context.Background()
	t.Setenv("OTOGI_TEST_APP_HASH", "value-from-environment")
	s, _, _ := sectionsHarness(t, openStore(t), slog.New(slog.DiscardHandler))
	telegram := func(name string, threads int) string {
		return fmt.Sprintf(`{"account": %q, "app_id": 7, "app_hash": "${OTOGI_TEST_APP_HASH}", "upload_threads": %d}`, name, threads)
	}
	qq := func(name string) string {
		return fmt.Sprintf(`{"account": %q, "app_id": "1", "app_secret": "${OTOGI_TEST_APP_HASH}"}`, name)
	}
	save := func(base int64, telegrams, qqs string) error {
		t.Helper()
		_, err := s.save(ctx, accountsSection, base, nil, json.RawMessage(`{"telegram": [`+telegrams+`], "qq": [`+qqs+`]}`), "Tundra")
		return err
	}
	revision := func() int64 {
		t.Helper()
		cur, err := s.current(ctx, accountsSection)
		if err != nil {
			t.Fatal(err)
		}
		return cur.Revision
	}
	if err := save(0, telegram("main", 8), qq("otogi")); err != nil {
		t.Fatal(err)
	}

	// The names an operator typed over: each platform loses an account and
	// gains one in the same save.
	for name, renamed := range map[string][2]string{
		"both":     {telegram("FUSHI", 8), qq("Mizuki")},
		"telegram": {telegram("FUSHI", 8), qq("otogi")},
		"qq":       {telegram("main", 8), qq("Mizuki")},
	} {
		err := save(1, renamed[0], renamed[1])
		if !errors.Is(err, otogi.ErrInvalid) || !strings.Contains(err.Error(), "cannot be changed") {
			t.Fatalf("%s renamed: %v, want ErrInvalid saying the name cannot be changed", name, err)
		}
		if got := revision(); got != 1 {
			t.Fatalf("%s renamed: revision %d, want 1 still", name, got)
		}
	}

	// What is not a change of name: another field, an account more, an
	// account fewer, and one platform's account going while another's comes.
	if err := save(1, telegram("main", 4), qq("otogi")); err != nil {
		t.Fatalf("another field changed: %v", err)
	}
	if err := save(2, telegram("main", 4)+","+telegram("second", 8), qq("otogi")); err != nil {
		t.Fatalf("an account added: %v", err)
	}
	if err := save(3, telegram("main", 4)+","+telegram("second", 8)+","+telegram("third", 8), ""); err != nil {
		t.Fatalf("a QQ account removed and a Telegram one added: %v", err)
	}
	// Replacing an account takes two saves: the old one goes, then the new one comes.
	if err := save(4, telegram("main", 4), ""); err != nil {
		t.Fatalf("accounts removed: %v", err)
	}
	if err := save(5, telegram("main", 4)+","+telegram("FUSHI", 8), ""); err != nil {
		t.Fatalf("an account added after the removal: %v", err)
	}
}
