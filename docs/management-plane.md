# Management Plane

## Purpose

The management plane is how an operator checks, queries, and sets up a running instance. It is a JSON-over-HTTP API served by the bot process; the control panel is its client.

It is an adapter: it reads the event log and component status, and changes module settings and allowlists, through their APIs, never their tables or internals.

## Access

`management.addr` defaults to `127.0.0.1:8090`; an empty value disables the API. On a non-loopback address, `management.token` is required and sent as `Authorization: Bearer <token>` on `/api/` routes; `/healthz` and `/readyz` stay open for supervisors.

With `management.public_url`, the control panel signs in with passkeys (see `docs/security.md`), and an `/api/` request is admitted with a session cookie, or with the token when it comes straight to the server rather than through the proxy. Changes are then recorded with the passkey's name. The sign-in routes are open:

| Route | Purpose |
| --- | --- |
| `GET /api/auth/session` | Whether this browser is signed in, and with which passkey |
| `POST /api/auth/login/begin`, `POST /api/auth/login/finish` | Sign in: the options for `navigator.credentials.get`, then its result |
| `POST /api/auth/logout` | Sign out |
| `POST /api/auth/enroll/begin`, `POST /api/auth/enroll/finish` | Register a first passkey from an enrollment link's token, and sign in |
| `GET /api/auth/passkeys` | The passkeys (signed in) |
| `POST /api/auth/passkeys/begin`, `POST /api/auth/passkeys/finish` | Register another passkey (signed in) |
| `DELETE /api/auth/passkeys/{id}` | Remove one, except the last (signed in) |

## Routes

The API is built with [huma](https://huma.rocks): `GET /api/openapi.json` (or `.yaml`) is its OpenAPI 3.1 document, from which clients generate their types.

| Route | Purpose |
| --- | --- |
| `GET /healthz` | Liveness |
| `GET /readyz` | 200 when every driver is ready, else 503 |
| `GET /api/status` | Version, uptime, kernel stats with each module's runs since start (completed, slow, failed, panicked), driver states with pending/failed inbox rows and gaps, event log health |
| `GET /api/events` | Records, newest first. Filters: `kind`, `component`, `event`, `platform`, `account`, `conversation`, `outcome`, `q` (text in payload), `since`, `until` (RFC 3339). `before=<id>` pages older; `after=<id>` polls newer. `limit` is at most 500 (default 100). The response has `records`, `more`, and `oldest_retained`. |
| `GET /api/events/{id}/trace` | All records of one event, in order |
| `POST /api/shutdown` | Graceful shutdown |
| `GET /api/modules` | Every module compiled in: whether it is on and running, its settings revision, its allowlists with their chat counts, its commands, and its runs since start. A module whose saved settings do not build says why in `problem`. |
| `GET /api/modules/{module}` | One module, as above |
| `PATCH /api/modules/{module}` | Turn it on or off (`{"enabled": true}`): a settings revision like any other, applied at once |
| `GET /api/modules/{module}/settings` | The current revision (number, on or off, settings, who saved it and when) and the settings' JSON Schema, whose `doc` texts describe each field. The `ETag` is the revision. |
| `PUT /api/modules/{module}/settings` | Save new settings and apply them. `If-Match` must carry the `ETag` they replace; a stale one is 412. Settings that break the schema are 422, field by field. The module is built from them and swapped in while it runs, with the modules using its services; nothing is stored if that fails. |
| `GET /api/modules/{module}/settings/revisions` | The settings' history, newest first: `before=<revision>`, `limit` |
| `POST /api/modules/{module}/settings/revisions/{revision}/restore` | Save an earlier revision's settings as a new revision (`If-Match` as for `PUT`); whether the module runs stays as it is |
| `GET /api/modules/{module}/allowlists/{list}` | The chats on an allowlist, with who added each and when, and what the chat directory knows of it |
| `PUT`, `DELETE /api/modules/{module}/allowlists/{list}/chats/{chat}` | Add a chat to an allowlist, or remove it; recorded as `allowlist.changed` |
| `GET /api/chats` | Every chat the bot has seen, most recently seen first: kind, title, name, first and last seen, latest message, and the allowlists it is on |
| `GET /api/chats/{chat}` | One chat, with its new messages per day over the last 7 days |
| `PATCH /api/chats/{chat}` | Name it (`{"name": "..."}`); the name wins over the platform's title, and an empty one clears it |
| `GET /api/chats/{chat}/messages` | Its conversation, newest first (`before=<message id>`, `limit`): people's messages from the events received, and the bot's from what it sent, each naming its module and that module's records for the event it answered (`chat.reply` with the model, `module.completed` with the duration) |
| `GET /api/chats/{chat}/media/{message}/{part}` | An image or file in the conversation: what the bot sent comes from the event log; what people sent is fetched through the platform, so it is gone once the platform drops it |

A chat is named as `/whoami` prints it, `<platform>:<conversation>` (`telegram:channel:123`). Changes are recorded as made by the passkey that signed in, or by `management API` when the token was used.

## Capabilities

**Health**
- liveness: the process is running;
- readiness: required components are ready.

Both are cheap, need no auth, and are meant for supervisors.

**Status**
- version, build, config file path, uptime;
- each component's lifecycle state, last error, and when it changed;
- kernel queue depth, durable inbox backlog per driver, recorded gaps;
- event log health and the oldest record still retained.

**Event log query**
- newest-first by default, with cursors to page older or poll newer;
- filters: time range, kind, component, outcome, platform, account, conversation, event ID, and text search over payloads;
- responses contain full records, including content.

**Event trace**
- every record correlated with one event ID (received, each module outcome, each send) in order.

**Lifecycle**
- request a graceful shutdown.

Configuration reload, backup triggers, and other commands are added when operators need them.

## API conventions

- Resources and HTTP verbs: `GET` reads, `PUT` and `PATCH` change, `DELETE` removes, `POST` runs a command.
- Writes that can collide carry the revision they change in `If-Match`.
- Cursors are record or revision IDs and stable: paging never skips or repeats records.
- Errors are RFC 9457 problem details: `title`, `status`, a human `detail`, and for invalid input `errors` naming each failing field.
- Times are RFC 3339; durations in records are milliseconds.
- Page size has a maximum, and larger requests are clamped.
- The API is unstable until the control panel exists. Breaking changes update in-tree clients in the same change.
