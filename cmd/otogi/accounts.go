package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// The sections the process reads when it starts: the platform accounts,
// whose drivers start once, and the process's own limits. Saving one
// validates and stores it; the running process keeps what it started with
// until it is restarted (POST /api/restart).

// accountsSettings is the system:accounts section.
type accountsSettings struct {
	Telegram []telegramSettings `json:"telegram,omitempty" doc:"Telegram user accounts. A new one is logged in on the server, with otogi login and its name, before the restart that starts it."`
	QQ       []qqSettings       `json:"qq,omitempty" doc:"QQ bots, of the QQ Open Platform (q.qq.com)."`
}

type telegramSettings struct {
	Account       string       `json:"account" minLength:"1" doc:"The name the bot knows the account by: its login, its events and chats, and what modules keep for it are all under this name, so it cannot be changed once saved. To replace an account, remove it in one save and add the other in the next."`
	AppID         int          `json:"app_id" minimum:"1" doc:"The application's api_id, from my.telegram.org."`
	AppHash       otogi.Secret `json:"app_hash" doc:"The application's api_hash."`
	Session       string       `json:"session,omitempty" doc:"The session file; unset is telegram/<account>/session.json in the data directory."`
	Database      string       `json:"database,omitempty" doc:"The driver's database; unset is telegram/<account>/state.sqlite in the data directory."`
	UploadThreads int          `json:"upload_threads,omitempty" minimum:"0" default:"8" doc:"How many parts of a file upload at once."`
}

type qqSettings struct {
	Account   string       `json:"account" minLength:"1" doc:"The name the bot knows the account by: its login, its events and chats, and what modules keep for it are all under this name, so it cannot be changed once saved. To replace an account, remove it in one save and add the other in the next."`
	AppID     string       `json:"app_id" minLength:"1" doc:"The bot's AppID."`
	AppSecret otogi.Secret `json:"app_secret" doc:"The bot's AppSecret."`
	Database  string       `json:"database,omitempty" doc:"The driver's database; unset is qq/<account>/state.sqlite in the data directory."`
}

// validate checks what the schema cannot say, and for the file's accounts
// what it does.
func (c accountsSettings) validate() []error {
	var errs []error
	named := func(field, account string, seen map[string]bool) {
		switch {
		case account == "":
			errs = append(errs, fmt.Errorf("%s.account is required", field))
		case seen[account]:
			errs = append(errs, fmt.Errorf("%s.account %q is used twice", field, account))
		}
		seen[account] = true
	}
	seen := map[string]bool{}
	for i, a := range c.Telegram {
		field := fmt.Sprintf("telegram[%d]", i)
		named(field, a.Account, seen)
		if a.AppID <= 0 || a.AppHash == "" {
			errs = append(errs, fmt.Errorf("%s: app_id and app_hash are required", field))
		}
		if a.UploadThreads < 0 {
			errs = append(errs, fmt.Errorf("%s.upload_threads %d: must not be negative", field, a.UploadThreads))
		}
	}
	seen = map[string]bool{}
	for i, a := range c.QQ {
		field := fmt.Sprintf("qq[%d]", i)
		named(field, a.Account, seen)
		if a.AppID == "" || a.AppSecret == "" {
			errs = append(errs, fmt.Errorf("%s: app_id and app_secret are required", field))
		}
	}
	return errs
}

// resolve returns the accounts as their drivers take them: each with its
// secret's value, and with the files it does not place itself under dataDir.
func (c accountsSettings) resolve(dataDir string, secret func(name string) (string, bool)) ([]TelegramAccount, []QQAccount, error) {
	if errs := c.validate(); len(errs) > 0 {
		return nil, nil, fmt.Errorf("%w: %w", errors.Join(errs...), otogi.ErrInvalid)
	}
	var telegram []TelegramAccount
	for i, a := range c.Telegram {
		hash, err := a.AppHash.Resolve(secret)
		if err != nil {
			return nil, nil, fmt.Errorf("telegram[%d].app_hash: %w", i, err)
		}
		dir := filepath.Join(dataDir, "telegram", a.Account)
		telegram = append(telegram, TelegramAccount{
			Account: a.Account, AppID: a.AppID, AppHash: hash, UploadThreads: cmp.Or(a.UploadThreads, 8),
			Session: cmp.Or(a.Session, filepath.Join(dir, "session.json")), Database: cmp.Or(a.Database, filepath.Join(dir, "state.sqlite")),
		})
	}
	var qq []QQAccount
	for i, a := range c.QQ {
		appSecret, err := a.AppSecret.Resolve(secret)
		if err != nil {
			return nil, nil, fmt.Errorf("qq[%d].app_secret: %w", i, err)
		}
		qq = append(qq, QQAccount{
			Account: a.Account, AppID: a.AppID, AppSecret: appSecret,
			Database: cmp.Or(a.Database, filepath.Join(dataDir, "qq", a.Account, "state.sqlite")),
		})
	}
	return telegram, qq, nil
}

// keepingNames makes a section of accounts refuse a save in which a platform
// both loses an account and gains one: the list has nothing but the name to
// tell its entries apart, so that is what a changed name looks like, and a
// changed name is an account with no login and none of the old one's data.
// An operator renamed two accounts on a deployment that way, and the bot
// did not start again until the settings were reverted on the server.
func keepingNames(sec section) section {
	sec.change = func(was, now []byte) error {
		// Settings that do not decode are refused where they are checked.
		before, _ := decodeSettings[accountsSettings](was)
		after, _ := decodeSettings[accountsSettings](now)
		return errors.Join(
			renamed("telegram", names(before.Telegram, func(a telegramSettings) string { return a.Account }), names(after.Telegram, func(a telegramSettings) string { return a.Account })),
			renamed("qq", names(before.QQ, func(a qqSettings) string { return a.Account }), names(after.QQ, func(a qqSettings) string { return a.Account })),
		)
	}
	return sec
}

func names[T any](accounts []T, name func(T) string) []string {
	out := make([]string, len(accounts))
	for i, a := range accounts {
		out[i] = name(a)
	}
	return out
}

// renamed is the error for a platform whose accounts lose a name and gain
// another in one save.
func renamed(platform string, before, after []string) error {
	var gone, come []string
	for _, name := range before {
		if !slices.Contains(after, name) {
			gone = append(gone, name)
		}
	}
	for _, name := range after {
		if !slices.Contains(before, name) {
			come = append(come, name)
		}
	}
	if len(gone) == 0 || len(come) == 0 {
		return nil
	}
	return fmt.Errorf("%s: an account's name cannot be changed, and this save has %s for %s: its login, events, chats, and what modules keep for it are under the name, so another name is another account, which has none of them. To replace an account, remove it in one save and add the other in the next: %w",
		platform, strings.Join(come, ", "), strings.Join(gone, ", "), otogi.ErrInvalid)
}

// checkAccounts refuses accounts a start could not build the drivers from.
// Whether the platform accepts the credentials only the start finds out.
func checkAccounts(_ *system, c accountsSettings, secret func(string) (string, bool)) (func(), error) {
	_, _, err := c.resolve("", secret)
	return func() {}, err
}

// processSettings is the system:process section.
type processSettings struct {
	Workers         int            `json:"workers,omitempty" minimum:"0" default:"32" doc:"How many deliveries to modules run at once."`
	MaxInFlight     int            `json:"max_in_flight,omitempty" minimum:"0" default:"1024" doc:"How many events may be admitted and not yet finished; the drivers wait while that many are."`
	HandlerTimeout  otogi.Duration `json:"handler_timeout,omitempty" default:"10m" doc:"How long one delivery may take, for a module that does not set its own."`
	LogFormat       string         `json:"log_format,omitempty" enum:"json,text" default:"json" doc:"The format of the process log's lines."`
	ShutdownTimeout otogi.Duration `json:"shutdown_timeout,omitempty" default:"30s" doc:"How long a stop waits for the events in flight to finish."`
}

// checkProcess has nothing to refuse that the schema does not.
func checkProcess(*system, processSettings, func(string) (string, bool)) (func(), error) {
	return func() {}, nil
}

// accounts is the file's accounts as settings, each secret as the value the
// file has for it.
func (c Config) accounts() accountsSettings {
	var out accountsSettings
	for _, a := range c.Telegram {
		out.Telegram = append(out.Telegram, telegramSettings{
			Account: a.Account, AppID: a.AppID, AppHash: otogi.Secret(a.AppHash), Session: a.Session, Database: a.Database, UploadThreads: a.UploadThreads,
		})
	}
	for _, a := range c.QQ {
		out.QQ = append(out.QQ, qqSettings{Account: a.Account, AppID: a.AppID, AppSecret: otogi.Secret(a.AppSecret), Database: a.Database})
	}
	return out
}

// notInAName is what an account's name may have that a secret's may not.
var notInAName = regexp.MustCompile(`[^A-Za-z0-9_]`)

// accountsFromFile keeps each secret as the file writes it, as llmFromFile
// does. An app_id the file takes from the environment is stored as its
// value: it is no secret.
func accountsFromFile(cfg Config) ([]string, any, map[string]string) {
	var keys []string
	if len(cfg.Telegram) > 0 {
		keys = append(keys, "telegram")
	}
	if len(cfg.QQ) > 0 {
		keys = append(keys, "qq")
	}
	settings, written, literal := cfg.accounts(), cfg.moved.accounts, map[string]string{}
	secretOf := func(platform, account, field, written string, value otogi.Secret) otogi.Secret {
		name := "OTOGI_" + platform + "_" + strings.ToUpper(notInAName.ReplaceAllString(account, "_")) + "_" + field
		return fileSecret(written, name, string(value), literal)
	}
	for i := range settings.Telegram {
		a := &settings.Telegram[i]
		a.AppHash = secretOf("TELEGRAM", a.Account, "APP_HASH", written.Telegram[i].AppHash, a.AppHash)
	}
	for i := range settings.QQ {
		a := &settings.QQ[i]
		a.AppSecret = secretOf("QQ", a.Account, "APP_SECRET", written.QQ[i].AppSecret, a.AppSecret)
	}
	return keys, settings, literal
}

func processFromFile(cfg Config) ([]string, any, map[string]string) {
	return cfg.moved.process, processSettings{
		Workers: cfg.Kernel.Workers, MaxInFlight: cfg.Kernel.MaxInFlight, HandlerTimeout: otogi.Duration(cfg.Kernel.HandlerTimeout),
		LogFormat: cfg.Log.Format, ShutdownTimeout: otogi.Duration(cfg.ShutdownTimeout),
	}, nil
}

// startAccounts returns the accounts a start runs, for the drivers and for
// otogi login: those of the section's starting revision.
func (s *moduleSettings) startAccounts(ctx context.Context, cfg Config) ([]TelegramAccount, []QQAccount, error) {
	start, err := s.starting(ctx, cfg, accountsSection)
	if err != nil {
		return nil, nil, err
	}
	stored, err := s.storedSecrets(ctx)
	if err != nil {
		return nil, nil, err
	}
	maps.Copy(stored, start.literal)
	c, err := decodeSettings[accountsSettings](start.Settings)
	if err != nil {
		return nil, nil, fmt.Errorf("%s, settings revision %d: %w", accountsSection, start.Revision, err)
	}
	telegram, qq, err := c.resolve(cfg.DataDir, s.environmentWith(stored).secret)
	if err != nil {
		return nil, nil, fmt.Errorf("%s, settings revision %d: %w", accountsSection, start.Revision, err)
	}
	return telegram, qq, nil
}

// startProcess returns the process settings a start runs with. It needs no
// log, which is made from them.
func (s *moduleSettings) startProcess(ctx context.Context, cfg Config) (processSettings, error) {
	start, err := s.starting(ctx, cfg, processSection)
	if err != nil {
		return processSettings{}, err
	}
	c, err := decodeSettings[processSettings](start.Settings)
	if err != nil {
		return c, fmt.Errorf("%s, settings revision %d: %w", processSection, start.Revision, err)
	}
	return c, nil
}
