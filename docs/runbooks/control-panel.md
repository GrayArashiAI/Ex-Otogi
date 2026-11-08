# Control Panel

The control panel is the bot's own web app: Status, Modules, Chats, and the Event log. The bot serves it with its management API on `management.addr`; a TLS reverse proxy puts it on the web. Sign-in goes through an OpenID Connect provider.

## Set it up

1. Build the panel into the binary: `make web` before `go build` (`make deploy` does both). A binary built without it answers the panel's pages with 503.
2. Set where it is reached, the origin the identity provider sends browsers back to:

   ```yaml
   management:
     addr: 127.0.0.1:8383
     public_url: https://otogi.example.com
   ```

3. Proxy the whole site to `management.addr`, keeping `Host` and setting `X-Forwarded-For`. With nginx:

   ```nginx
   location / {
       proxy_pass http://127.0.0.1:8383;
       proxy_http_version 1.1;
       proxy_set_header Host              $host;
       proxy_set_header X-Forwarded-For   $remote_addr;
       proxy_set_header X-Forwarded-Proto $scheme;
       proxy_read_timeout 180s;
       gzip on;
       gzip_proxied any;
       gzip_types text/css text/javascript application/javascript application/json image/svg+xml;
   }
   ```

   A request through the proxy needs a panel session; the management token works only for requests made straight to `management.addr`.

## Sign-in

The panel signs in through an OpenID Connect provider; it has no accounts or passwords of its own.

1. Register a confidential client at the provider, with the redirect URI `<public_url>/api/auth/callback`, the post-logout redirect URI `<public_url>/sign-in`, and the scopes `openid` and `profile`. With Prohibitorum (opus.name):

   ```sh
   podman exec prohibitorum prohibitorum oidc-client create --client-id ex-otogi --display-name Ex-Otogi \
     --redirect-uri https://otogi.example.com/api/auth/callback \
     --post-logout-redirect-uri https://otogi.example.com/sign-in --scope openid --scope profile
   ```

   It prints the client secret once: put it in `otogi.env` as `OTOGI_OIDC_CLIENT_SECRET`.

2. Configure it, and name who may sign in by their username at the provider:

   ```yaml
   management:
     public_url: https://otogi.example.com
     oidc:
       issuer: https://opus.name
       client_id: ex-otogi
       client_secret: ${OTOGI_OIDC_CLIENT_SECRET}
       allow: [tundra]
   ```

Removing a username from `allow` (and restarting) ends its sessions. To end every session at once, stop the bot, remove `<data_dir>/panel.key`, and start it. Refused sign-ins are in the event log as `panel.session`, saying why.

## Set up modules

Modules come with no settings. In Modules, pick one, fill in its Configuration (each field says what it does), Save, review the changes, and Apply; then turn it on. Saving applies the settings to the running bot at once; if they do not build or the module does not start, nothing changes and the panel says why. History lists every revision with who saved it, and Restore saves an old one again.

BOOTH cards and X cards draw with Censoring's settings: turn Censoring on first (its defaults need nothing filled in). Changing it applies to every card module at once.

A secret, such as a todou token, goes in the environment (`otogi.env`); the setting holds the reference, `${OTOGI_TODOU_TOKEN}`.

Where a module answers: Add chats on its page, picking from the chats the bot has seen, or the module switches on a chat's page in Chats.

## Follow what the bot does

- Status, Working now: what modules are handling at this moment, and for how long.
- Event log, Tasks: one row for each event a module did something about, newest first; a running one has a pulsing dot and a ticking time. Open it for its message and its trace: every record of it in order, with the step still running at the end. While it runs, the trace follows it, and says how long ago the last record came: a model round in a chat reply often takes 10 to 45 seconds.
- Event log, Records: every record as written, for what is not a task (driver states, process events, watcher problems). A record's Event opens its task.

## From a script

On the server, the same API answers without the panel, at `management.addr` (with `Authorization: Bearer <token>` when `management.token` is set):

```sh
curl -s 127.0.0.1:8383/api/modules
curl -s -X PUT -H 'Content-Type: application/json' -H 'If-Match: "3"' \
  -d @settings.json 127.0.0.1:8383/api/modules/chat/settings
curl -s -X PUT 127.0.0.1:8383/api/modules/chat/allowlists/Luna/chats/telegram:channel:123
```

`docs/management-plane.md` lists every route; `otogi openapi` prints the OpenAPI document.

## Back up

`panel.key` seals session cookies (losing it only signs everyone out); `modules.sqlite` holds module settings, their revisions, and allowlists; `eventlog.sqlite` holds the chat directory. Back them up like every database here: with SQLite's backup, never by copying the live file (see `docs/operations.md`).
