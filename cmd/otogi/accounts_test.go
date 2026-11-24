package main

import (
	"bytes"
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

const restartKeysFile = `
shutdown_timeout: 45s
log:
  format: text
kernel:
  workers: 4
  max_in_flight: 64
  handler_timeout: 2m
telegram:
  - account: main
    app_id: 12345
    app_hash: ${OTOGI_TEST_APP_HASH}
    upload_threads: 4
qq:
  - account: my-bot
    app_id: "102000001"
    app_secret: value-literal
`

func debugLog(to *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(to, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// The accounts and the process settings take their first revision from the
// file: a secret the file takes from the environment stays that reference, a
// secret it spells out moves to the secrets, and what the file leaves out is
// left out. Once they have a revision the file's keys are ignored, with the
// warning.
func TestAccountsAndProcessAreImportedFromTheFile(t *testing.T) {
	ctx := context.Background()
	storage := openStore(t)
	t.Setenv("OTOGI_TEST_APP_HASH", "value-from-environment")
	var log bytes.Buffer
	cfg := fileConfig(t, restartKeysFile)
	cfg.DataDir = "/srv/otogi"
	s, rec, _ := sectionsHarness(t, storage, cfg, debugLog(&log))

	accounts, _ := s.current(ctx, accountsSection)
	if accounts.Revision != 1 || accounts.By != "otogi.yaml" || string(accounts.Settings) != `{"telegram":[{"account":"main","app_id":12345,"app_hash":"${OTOGI_TEST_APP_HASH}","upload_threads":4}],"qq":[{"account":"my-bot","app_id":"102000001","app_secret":"${OTOGI_QQ_MY_BOT_APP_SECRET}"}]}` {
		t.Fatalf("the accounts imported: %+v", accounts)
	}
	process, _ := s.current(ctx, processSection)
	if process.Revision != 1 || process.By != "otogi.yaml" || string(process.Settings) != `{"workers":4,"max_in_flight":64,"handler_timeout":"2m","log_format":"text","shutdown_timeout":"45s"}` {
		t.Fatalf("the process settings imported: %+v", process)
	}
	if got := storedSecrets(t, storage); got != "OTOGI_QQ_MY_BOT_APP_SECRET=value-literal by otogi.yaml" {
		t.Fatalf("secrets stored by the import: %s", got)
	}
	if got := secretList(t, s); !strings.Contains(got, `"name":"OTOGI_QQ_MY_BOT_APP_SECRET","source":"panel"`) || !strings.Contains(got, `"name":"OTOGI_TEST_APP_HASH","source":"environment","references":["system:accounts"]`) {
		t.Fatalf("secrets %s", got)
	}

	// What a start builds the drivers and the kernel from.
	telegram, qq, err := s.startAccounts(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(telegram) != 1 || telegram[0] != (TelegramAccount{
		Account: "main", AppID: 12345, AppHash: "value-from-environment", UploadThreads: 4,
		Session: "/srv/otogi/telegram/main/session.json", Database: "/srv/otogi/telegram/main/state.sqlite",
	}) {
		t.Fatalf("telegram accounts to start: %+v", telegram)
	}
	if len(qq) != 1 || qq[0] != (QQAccount{Account: "my-bot", AppID: "102000001", AppSecret: "value-literal", Database: "/srv/otogi/qq/my-bot/state.sqlite"}) {
		t.Fatalf("qq accounts to start: %+v", qq)
	}
	p, err := s.startProcess(ctx, cfg)
	if err != nil || p.Workers != 4 || p.MaxInFlight != 64 || time.Duration(p.HandlerTimeout) != 2*time.Minute || p.LogFormat != "text" || time.Duration(p.ShutdownTimeout) != 45*time.Second {
		t.Fatalf("process settings to start: %+v, %v", p, err)
	}

	recorded, _ := json.Marshal(rec.list)
	for _, want := range []string{`"system:accounts"`, `"system:process"`, `"secret.changed"`, "OTOGI_QQ_MY_BOT_APP_SECRET"} {
		if !strings.Contains(string(recorded), want) {
			t.Fatalf("the import's records lack %s: %s", want, recorded)
		}
	}
	revisions, _ := storage.SettingsRevisions(ctx, accountsSection, 9, 9)
	stored, _ := json.Marshal(revisions)
	for _, text := range []string{string(recorded), log.String(), string(stored), secretList(t, s)} {
		if strings.Contains(text, "value-") {
			t.Fatalf("a secret's value is in %s", text)
		}
	}

	// The second start: the file says other things, which are ignored.
	log.Reset()
	changed := strings.NewReplacer("workers: 4", "workers: 9", "account: main", "account: other", "45s", "50s").Replace(restartKeysFile)
	cfg = fileConfig(t, changed)
	s, rec, _ = sectionsHarness(t, storage, cfg, debugLog(&log))
	if telegram, _, err := s.startAccounts(ctx, cfg); err != nil || len(telegram) != 1 || telegram[0].Account != "main" {
		t.Fatalf("second start: accounts %+v, %v", telegram, err)
	}
	if p, err := s.startProcess(ctx, cfg); err != nil || p.Workers != 4 || time.Duration(p.ShutdownTimeout) != 45*time.Second {
		t.Fatalf("second start: process %+v, %v", p, err)
	}
	if len(rec.list) != 0 || strings.Count(log.String(), "level=WARN") != 1 {
		t.Fatalf("second start: records %+v, log %s", rec.list, log.String())
	}
	for _, key := range []string{"telegram", "qq", "kernel.workers", "kernel.max_in_flight", "kernel.handler_timeout", "log.format", "shutdown_timeout"} {
		if !strings.Contains(log.String(), key) {
			t.Fatalf("the warning does not name %s: %s", key, log.String())
		}
	}
}

// A file with none of the keys leaves the sections unsaved: no accounts,
// and the process defaults.
func TestAFreshInstallHasNoAccounts(t *testing.T) {
	ctx := context.Background()
	cfg := fileConfig(t, "data_dir: /srv/otogi\n")
	s, _, _ := sectionsHarness(t, openStore(t), cfg, slog.New(slog.DiscardHandler))
	for _, id := range []otogi.ModuleID{accountsSection, processSection} {
		if cur, _ := s.current(ctx, id); cur.Revision != 0 {
			t.Fatalf("%s: %+v", id, cur)
		}
	}
	telegram, qq, err := s.startAccounts(ctx, cfg)
	if err != nil || len(telegram) != 0 || len(qq) != 0 {
		t.Fatalf("accounts %+v %+v, %v", telegram, qq, err)
	}
	if p, err := s.startProcess(ctx, cfg); err != nil || p != (processSettings{}) {
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
	cfg := fileConfig(t, "data_dir: "+filepath.Join("/srv", "otogi")+"\nkernel: {workers: 4}\n")
	s, rec, _ := sectionsHarness(t, storage, cfg, slog.New(slog.DiscardHandler))
	sectionsNow := func(s *moduleSettings) string {
		t.Helper()
		all, err := newSystemAPI(s).Sections(ctx)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(all)
		return string(data)
	}
	if got := sectionsNow(s); got != `[{"id":"accounts","revision":0,"applies":"restart","running":0},{"id":"general","revision":0,"applies":"now","running":0},{"id":"llm","revision":0,"applies":"now","running":0},{"id":"process","revision":1,"applies":"restart","running":1}]` {
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
		if _, err := s.save(ctx, processSection, 1, nil, json.RawMessage(refused), "Tundra"); !errors.Is(err, otogi.ErrInvalid) {
			t.Fatalf("%s: %v, want ErrInvalid", name, err)
		}
	}

	if _, err := s.save(ctx, accountsSection, 0, nil, json.RawMessage(`{"telegram": [{"account": "main", "app_id": 7, "app_hash": "${OTOGI_TEST_APP_HASH}", "session": "/elsewhere/session.json"}], "qq": [{"account": "bot", "app_id": "1", "app_secret": "${OTOGI_TEST_APP_HASH}"}]}`), "Tundra"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.save(ctx, processSection, 1, nil, json.RawMessage(`{"workers": 8, "handler_timeout": "90s"}`), "Tundra"); err != nil {
		t.Fatal(err)
	}
	if got := s.kernel.Stats().Workers; got != 32 { // the harness's kernel, which no save may touch
		t.Fatalf("workers of the running kernel: %d", got)
	}
	if got := sectionsNow(s); !strings.Contains(got, `{"id":"accounts","revision":1,"applies":"restart","running":0}`) || !strings.Contains(got, `{"id":"process","revision":2,"applies":"restart","running":1}`) {
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
	s, _, _ = sectionsHarness(t, storage, cfg, slog.New(slog.DiscardHandler))
	if got := sectionsNow(s); !strings.Contains(got, `{"id":"accounts","revision":1,"applies":"restart","running":1}`) || !strings.Contains(got, `{"id":"process","revision":2,"applies":"restart","running":2}`) {
		t.Fatalf("sections after the next start: %s", got)
	}
	telegram, qq, err := s.startAccounts(ctx, cfg)
	if err != nil || len(telegram) != 1 || telegram[0] != (TelegramAccount{
		Account: "main", AppID: 7, AppHash: "value-from-environment", UploadThreads: 8,
		Session: "/elsewhere/session.json", Database: "/srv/otogi/telegram/main/state.sqlite",
	}) {
		t.Fatalf("telegram accounts of the next start: %+v, %v", telegram, err)
	}
	if len(qq) != 1 || qq[0] != (QQAccount{Account: "bot", AppID: "1", AppSecret: "value-from-environment", Database: "/srv/otogi/qq/bot/state.sqlite"}) {
		t.Fatalf("accounts of the next start: %+v, %v", qq, err)
	}
	if p, err := s.startProcess(ctx, cfg); err != nil || p.Workers != 8 || time.Duration(p.HandlerTimeout) != 90*time.Second {
		t.Fatalf("process settings of the next start: %+v, %v", p, err)
	}
}

// Before the first start has taken the accounts from the file, otogi login
// finds them there, and stores nothing.
func TestAccountsOfAFileNotYetImported(t *testing.T) {
	ctx := context.Background()
	storage := openStore(t)
	t.Setenv("OTOGI_TEST_APP_HASH", "value-from-environment")
	cfg := fileConfig(t, restartKeysFile)
	s := &moduleSettings{store: storage, kinds: moduleKinds}
	telegram, qq, err := s.startAccounts(ctx, cfg)
	if err != nil || len(telegram) != 1 || telegram[0].AppHash != "value-from-environment" || len(qq) != 1 || qq[0].AppSecret != "value-literal" {
		t.Fatalf("accounts %+v %+v, %v", telegram, qq, err)
	}
	if cur, _ := s.current(ctx, accountsSection); cur.Revision != 0 || storedSecrets(t, storage) != "" {
		t.Fatalf("reading the accounts stored %+v, secrets %q", cur, storedSecrets(t, storage))
	}
}

func TestAnAccountsNameCannotBeChanged(t *testing.T) {
	ctx := context.Background()
	t.Setenv("OTOGI_TEST_APP_HASH", "value-from-environment")
	s, _, _ := sectionsHarness(t, openStore(t), fileConfig(t, "data_dir: "+t.TempDir()+"\n"), slog.New(slog.DiscardHandler))
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
