package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"

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
	AppID         int          `json:"app_id" minimum:"1" format:"id" doc:"The application's api_id, from my.telegram.org."`
	AppHash       otogi.Secret `json:"app_hash" doc:"The application's api_hash."`
	Session       string       `json:"session,omitempty" placeholder:"telegram/{account}/session.json in the data directory" doc:"The session file; unset is telegram/<account>/session.json in the data directory."`
	Database      string       `json:"database,omitempty" placeholder:"telegram/{account}/state.sqlite in the data directory" doc:"The driver's database; unset is telegram/<account>/state.sqlite in the data directory."`
	UploadThreads int          `json:"upload_threads,omitempty" minimum:"0" default:"8" doc:"How many parts of a file upload at once."`
}

type qqSettings struct {
	Account   string       `json:"account" minLength:"1" doc:"The name the bot knows the account by: its login, its events and chats, and what modules keep for it are all under this name, so it cannot be changed once saved. To replace an account, remove it in one save and add the other in the next."`
	AppID     string       `json:"app_id" minLength:"1" doc:"The bot's AppID."`
	AppSecret otogi.Secret `json:"app_secret" doc:"The bot's AppSecret."`
	Database  string       `json:"database,omitempty" placeholder:"qq/{account}/state.sqlite in the data directory" doc:"The driver's database; unset is qq/<account>/state.sqlite in the data directory."`
}

// validate checks what the schema cannot say, and for the file's accounts
// what it does.
func (c accountsSettings) validate() []error {
	var errs []error
	refuse := func(path, reason string) {
		errs = append(errs, &otogi.SettingError{Path: path, Err: errors.New(reason)})
	}
	named := func(field, account string, seen map[string]bool) {
		switch {
		case account == "":
			refuse(field+".account", "required")
		case seen[account]:
			refuse(field+".account", strconv.Quote(account)+" is used twice")
		}
		seen[account] = true
	}
	seen := map[string]bool{}
	for i, a := range c.Telegram {
		field := fmt.Sprintf("telegram[%d]", i)
		named(field, a.Account, seen)
		if a.AppID <= 0 {
			refuse(field+".app_id", "required")
		}
		if a.AppHash == "" {
			refuse(field+".app_hash", "required")
		}
		if a.UploadThreads < 0 {
			refuse(field+".upload_threads", strconv.Itoa(a.UploadThreads)+": must not be negative")
		}
	}
	seen = map[string]bool{}
	for i, a := range c.QQ {
		field := fmt.Sprintf("qq[%d]", i)
		named(field, a.Account, seen)
		if a.AppID == "" {
			refuse(field+".app_id", "required")
		}
		if a.AppSecret == "" {
			refuse(field+".app_secret", "required")
		}
	}
	return errs
}

// TelegramAccount is one Telegram user account as its driver takes it:
// see accountsSettings.resolve.
type TelegramAccount struct {
	Account  string
	AppID    int
	AppHash  string
	Session  string
	Database string
	// UploadThreads is how many parts of a file upload at once; default 8.
	UploadThreads int
}

// QQAccount is one QQ bot (QQ Open Platform, q.qq.com) as its driver takes
// it.
type QQAccount struct {
	Account   string
	AppID     string
	AppSecret string
	Database  string
}

// resolve returns the accounts as their drivers take them: each with its
// secret's value, and with the files it does not place itself under dataDir.
func (c accountsSettings) resolve(dataDir string, secret func(name string) (string, bool)) ([]TelegramAccount, []QQAccount, error) {
	if err := errors.Join(c.validate()...); err != nil {
		return nil, nil, err
	}
	var telegram []TelegramAccount
	for i, a := range c.Telegram {
		hash, err := a.AppHash.Resolve(secret)
		if err != nil {
			return nil, nil, &otogi.SettingError{Path: fmt.Sprintf("telegram[%d].app_hash", i), Err: err}
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
			return nil, nil, &otogi.SettingError{Path: fmt.Sprintf("qq[%d].app_secret", i), Err: err}
		}
		qq = append(qq, QQAccount{
			Account: a.Account, AppID: a.AppID, AppSecret: appSecret,
			Database: cmp.Or(a.Database, filepath.Join(dataDir, "qq", a.Account, "state.sqlite")),
		})
	}
	return telegram, qq, nil
}

// accountNames are the lists of accounts, whose names cannot change. An
// operator renamed two accounts on a deployment, and the bot did not start
// again until the settings were reverted on the server.
var accountNames = []keyedList{
	{list: "telegram", key: "account", item: "an account", why: accountKept},
	{list: "qq", key: "account", item: "an account", why: accountKept},
}

const accountKept = "its login, events, chats, and what modules keep for it are under the name, so another name is another account, which has none of them"

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

// startAccounts returns the accounts a start runs, for the drivers and for
// otogi login: those of the section's current revision.
func (s *moduleSettings) startAccounts(ctx context.Context, dataDir string) ([]TelegramAccount, []QQAccount, error) {
	cur, err := s.current(ctx, accountsSection)
	if err != nil {
		return nil, nil, err
	}
	stored, err := s.storedSecrets(ctx)
	if err != nil {
		return nil, nil, err
	}
	c, err := decodeSettings[accountsSettings](cur.Settings)
	if err != nil {
		return nil, nil, fmt.Errorf("%s, settings revision %d: %w", accountsSection, cur.Revision, err)
	}
	telegram, qq, err := c.resolve(dataDir, s.environmentWith(stored).secret)
	if err != nil {
		return nil, nil, fmt.Errorf("%s, settings revision %d: %w", accountsSection, cur.Revision, err)
	}
	return telegram, qq, nil
}

// startProcess returns the process settings a start runs with. It needs no
// log, which is made from them.
func (s *moduleSettings) startProcess(ctx context.Context) (processSettings, error) {
	cur, err := s.current(ctx, processSection)
	if err != nil {
		return processSettings{}, err
	}
	c, err := decodeSettings[processSettings](cur.Settings)
	if err != nil {
		return c, fmt.Errorf("%s, settings revision %d: %w", processSection, cur.Revision, err)
	}
	return c, nil
}
