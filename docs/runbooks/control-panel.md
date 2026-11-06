# Control Panel

The control panel is the bot's own web app: Status, Modules, Chats, and the Event log. The bot serves it with its management API on `management.addr`; a TLS reverse proxy puts it on the web. Sign-in is passkeys only.

## Set it up

1. Build the panel into the binary: `make web` before `go build` (`make deploy` does both). A binary built without it answers the panel's pages with 503.
2. Set where it is reached, the origin passkeys are bound to:

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

## Register a passkey

On the server, as the service's user (it writes `<data_dir>/panel.sqlite`):

```sh
sudo -u www-data env $(xargs < otogi.env) ./otogi -config otogi.yaml passkey enroll
```

Open the printed link within 10 minutes, on the device that keeps the passkey, and name it. The link works once. Once signed in, add passkeys for other devices from the account menu, under Passkeys, and remove lost ones there; the last one cannot be removed.

Lost every passkey: run `passkey enroll` again on the server. To end every session at once, stop the bot, remove `panel.sqlite` (passkeys, links, and the cookie key go with it), start it, and enroll again.

## Set up modules

Modules come with no settings. In Modules, pick one, fill in its Configuration (each field says what it does), Save, review the changes, and Apply; then turn it on. Saving applies the settings to the running bot at once; if they do not build or the module does not start, nothing changes and the panel says why. History lists every revision with who saved it, and Restore saves an old one again.

A secret, such as a todou token, goes in the environment (`otogi.env`); the setting holds the reference, `${OTOGI_TODOU_TOKEN}`.

Where a module answers: Add chats on its page, picking from the chats the bot has seen, or the module switches on a chat's page in Chats.

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

`panel.sqlite` holds the passkeys and the cookie key; `modules.sqlite` holds module settings, their revisions, and allowlists; `eventlog.sqlite` holds the chat directory. Back them up like every database here: with SQLite's backup, never by copying the live file (see `docs/operations.md`).
