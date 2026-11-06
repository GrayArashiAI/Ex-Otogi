# Operations

## Deployment

One `otogi` process plus its data directory on one host, run under a supervisor (systemd, Docker, or similar) that restarts it on non-zero exit. The data directory is on local disk; SQLite WAL does not work over network filesystems.

## Configuration

- One YAML file (`otogi.yaml`; see [`otogi.example.yaml`](../otogi.example.yaml)) holds all settings, with one section per Telegram account, language model provider, and module.
- `llm.gemini` configures the Gemini provider: `api_key` (use `${OTOGI_GEMINI_API_KEY}`) and an optional `base_url`, the API endpoint with or without its version (default `https://generativelanguage.googleapis.com/v1beta`; a proxy or gateway that speaks the Gemini Interactions API works too). Modules name the provider and pin their own model. Live tests against the API: `OTOGI_GEMINI_API_KEY=... go test -run Live -v ./internal/adapter/gemini`.
- `${VAR}` in any value is read from the environment, which is how secrets such as the app hash stay out of the file. An unset variable is an error. Use block style for such values; in flow style (`{...}`) quote them.
- `-config PATH` selects the file (default `otogi.yaml`); the first argument selects the command: `run` (default), `validate`, `login ACCOUNT`, or `smoke`.
- The whole config is validated at startup before anything starts. Errors name the field or the line. Unknown fields are errors, so typos surface.
- `operators` lists the platform-qualified actor IDs (`telegram:user:123`, as `/whoami` prints them) allowed to use operator commands. The bot's own messages never are: the account is the bot's. Operator commands are `/status` and `/todou link`. Where modules answer is chosen in the control panel.
- Modules are not in the config file. Which modules run and each module's settings are runtime data in `modules.sqlite`, changed through the management API: `GET /api/modules/{module}/settings` returns the current revision and the settings' JSON Schema, whose `doc` texts describe every field; `PUT` saves a new revision, which is applied at once (see `docs/management-plane.md`). A secret, such as a todou token, is never stored: the settings hold a reference to an environment variable, `${NAME}`, read when the module is built. Each saved revision is recorded as `module.reconfigured`, and any earlier one can be restored.
- QQ sends names only now and then: a group member's nickname with some messages, none in private chats, and no group names. The QQ driver keeps every name a message shows. QQ's group info and member list APIs (group name, description, member count, member nicknames) are open only to whitelisted bots: the driver asks once a day per group, and when QQ refuses (code 11253), it records that once as `driver.lookup` and stops asking until restart. Name a QQ group or private chat in the panel to give it a name everywhere, agents included.
- Allowlists say where modules answer: each chat agent, `bilibili`, and `twitter` has one. They are runtime data in `modules.sqlite`, not config, changed in the control panel or through the management API; each change is recorded as `allowlist.changed`.
- The `bilibili` and `twitter` modules answer Bilibili video links (`bilibili.com/video/…`, `b23.tv/…`) and X posts (`x.com/…/status/…`, `twitter.com`, and the fxtwitter/vxtwitter/fixupx mirrors) in the chats on their allowlists with a card image drawn by Chrome; `twitter` also takes a `timezone` (IANA name) for its times, and `translate` (`provider`, `model`, `thinking`): a language model then reads each post with text, and when it is not in Chinese the card is followed by a second one translated into Simplified Chinese, marked with who translated it from what. Both need `chrome`, the path of a pinned [chrome-headless-shell](https://googlechromelabs.github.io/chrome-for-testing/) (`linux64` build, unzipped anywhere readable by the service user), its shared libraries, and fonts. On Ubuntu 24.04: `apt-get install --no-install-recommends libnspr4 libnss3 libatk1.0-0t64 libatk-bridge2.0-0t64 libxcomposite1 libxdamage1 libxfixes3 libxrandr2 libgbm1 libasound2t64 libatspi2.0-0t64 fonts-noto-cjk fonts-noto-color-emoji fonts-inter` (Inter sets Latin text; the CJK font would draw punctuation such as ’ full width), then `ldd chrome-headless-shell | grep 'not found'` should print nothing. Chrome starts for each card (about 0.6 s and 100 MB) and exits; nothing runs between cards. To upgrade it, unpack a newer build beside the old one and point `chrome` at it. X posts come from FxTwitter's public API, or from X's embed endpoint when FxTwitter fails; both depend on X tolerating unauthenticated reads, and a post neither returns gets no card (recorded as `twitter.card`, outcome error).
- `otogi validate` checks a config file without starting drivers or touching data.
- The control panel is served by the bot at every path outside `/api/`, from the build embedded in the binary (`web/`), when `management.public_url` names where it is reached. Put a TLS reverse proxy in front that passes the whole site to `management.addr`, keeping `Host` and setting `X-Forwarded-For` (nginx: `proxy_pass http://127.0.0.1:8383; proxy_set_header Host $host; proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;`). To register the first passkey, run `otogi passkey enroll` as the service's user and open the link it prints within 10 minutes.
- Changing configuration means restarting. Live reload is added only if a concrete need appears.

## Data directory

```text
<data>/
  eventlog.sqlite                 # event log, and the directory of chats seen
  panel.sqlite                    # control panel sign-in: passkeys and the cookie key
  modules.sqlite                  # module key/value state and timers
  modules/<id>.sqlite             # a module's private database, if it uses one
  telegram/<account>/session.json # gotd session (login)
  telegram/<account>/state.sqlite # inbox, update state, send records
```

Each Telegram account has its own session and database. The database records which account it belongs to, and startup refuses a database that belongs to another account, which prevents mixing up accounts.

## SQLite

- WAL mode, `synchronous=NORMAL`, a busy timeout, and foreign keys on, all set when opened.
- Each owning component embeds its migrations and applies them forward-only at startup. A database newer than the binary is refused.
- Never copy a live database file; use the backup methods below.

## Backup and restore

**Backup**
- Bot stopped: copy the data directory.
- Bot running: for each database, run `sqlite3 <db> ".backup <dest>"` (or `VACUUM INTO`), and copy the session files.

**Restore**
1. Stop the bot.
2. Put the files back in the data directory.
3. Start the bot.

Telegram fetches any updates newer than the restored state. Encryption and off-host storage of backups are up to the operator.

## Upgrades

1. Back up.
2. Replace the binary and restart. Migrations run at startup.
3. To roll back, restore the backup taken before the upgrade if the new binary migrated a schema; otherwise just swap the binary back.

Upgrading gotd also changes the Telegram API layer. Follow the gotd upgrade steps in the [Telegram runbook](runbooks/telegram-mtproto.md#gotd-upgrade).

## Signals and exit codes

- SIGINT or SIGTERM starts graceful shutdown (see `runtime.md`); a second signal forces exit.
- Exit 0 means a clean stop. Non-zero means a startup failure, a component failure, or a forced stop, and the reason is printed on stdout.

## Runbooks

- [`runbooks/core-runtime-smoke.md`](runbooks/core-runtime-smoke.md): build, quality gates, offline smoke.
- [`runbooks/telegram-mtproto.md`](runbooks/telegram-mtproto.md): Telegram storage, login, backup, recovery, live smoke, gotd upgrade.
