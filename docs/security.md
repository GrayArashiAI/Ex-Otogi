# Security

Ex-Otogi runs on a host the operator controls, for one operator. Security covers the things that realistically go wrong: leaked credentials, an exposed management API, and hostile or oversized platform input.

## Credentials

- Telegram application ID and hash, the account phone number, and the 2FA password come from the config file or environment variables, never from command-line arguments.
- The gotd session file is equivalent to a logged-in account. Keep it, together with the SQLite databases, in a data directory readable only by the service user.
- Credentials are not written to logs or the event log. They are configuration, not event data, so nothing needs redacting: just do not log them.
- The language model API keys come from the config file, normally as `${OTOGI_GEMINI_API_KEY}` and, for each `llm.openai` service, a variable of its own such as `${OTOGI_DEEPSEEK_API_KEY}`. Each is sent only to its provider's configured base URL.
- The decision module's TypeSafe API key is a setting saved as a reference, normally `${OTOGI_TYPESAFE_API_KEY}`, and is sent only to its `base_url`.
- Do not commit credentials. `.env.example` shows the variable names.

## Language model data

Calls to Gemini send the conversation, images included, to Google (or to the configured gateway). Calls are stateless (`store=false`), so Google keeps no interaction for later turns, but its terms still apply: on the free tier Google may use prompts and replies to improve its products, with human review, and apps used by people in the EEA, Switzerland, or the UK must use the paid tier. Google's terms require users to be 18 or older. These are the operator's decisions; see the [Gemini API terms](https://ai.google.dev/gemini-api/terms).

Calls to an `llm.openai` service send the conversation, images included, to that service. With DeepSeek's server tools on, DeepSeek also searches the web and reads the pages the conversation links on its own servers, and a page it read comes back signed and is sent back with later turns. Each service's terms and data handling apply, and are the operator's to check; see, for DeepSeek, its [terms of use](https://cdn.deepseek.com/policies/en-US/deepseek-terms-of-use.html) and [privacy policy](https://cdn.deepseek.com/policies/en-US/deepseek-privacy-policy.html).

Requests to the decision service send what the asking module puts in their state, such as the recent chat with its senders' names, to TypeSafe (or to the configured `base_url`). TypeSafe's terms and data handling apply, and are the operator's to check; see its [terms](https://typesafe.ai/legal/terms) and [privacy policy](https://typesafe.ai/legal/privacy-policy). The event log keeps each request's state in full, like the chat it came from.

The media module sends the images, animations (as frames), and videos of every chat on its `describe` list to the providers of its steps, whether or not an agent ever answers there, under the terms above: on Gemini's free tier, Google may use them to improve its products. Its database keeps the descriptions, with the text on pictures and what videos say, until the media goes unseen for 30 days, and its `media.described` records keep them in the event log.

## Management plane access

- The management HTTP server binds to `127.0.0.1` by default.
- When bound to any other address, it requires a bearer token from the configuration. Use TLS or a reverse proxy when it is reachable over a network.
- The control panel signs in only through an OpenID Connect provider (`management.oidc`): the authorization code flow with PKCE (S256), a `state` and a `nonce` checked on return, and the ID token verified against the provider's keys, issuer, and client ID (go-oidc). The panel keeps no credentials of its own.
- Who may use the panel is `management.oidc.allow`: the provider's usernames. An account the provider vouches for but the list does not name gets no session; a session ends as soon as its username leaves the list. The provider can restrict the client too.
- A session is a cookie sealed (signed and encrypted) with a key generated into `<data_dir>/panel.key`: `Secure`, `HttpOnly`, `SameSite=Lax` (the provider's redirect back is a cross-site navigation), valid 7 days. Signing out ends it and the provider's session. Cross-origin writes from browsers are refused (`http.CrossOriginProtection`).
- Behind the reverse proxy, every API request needs a session: a request carrying `X-Forwarded-For` is never admitted by the token. The token serves scripts on the server, calling the server directly.
- Each sign-in, refused sign-in (with why), and sign-out is recorded as `panel.session` with the account's username and subject; no token is logged.

## Event data

The event log and inbox store full message content by design, because operators need it to debug the bot. Protect these files like the session file. There is no redaction or minimization layer.

## Platform input

Platform payloads are untrusted. Drivers enforce limits only where the platform itself does not and where a real resource problem exists: maximum media download size, maximum stored payload size, and timeouts on every network call. Parsers return errors on malformed input; they never panic.

## Rendering

The bilibili module draws cards with chrome-headless-shell running without its sandbox, which a service user cannot have on Ubuntu 24.04. Chrome loads only the module's own template, with every value escaped by `html/template` and every image inlined, and it gets no network: all host names resolve to nothing. The sandbox would guard against hostile web content, which this page cannot contain.

## Dependencies

Prefer mature, actively maintained, widely used libraries over our own implementations; size alone is not a reason to avoid one. Run `govulncheck` when upgrading dependencies or before tagging a release, and fix reachable vulnerabilities.
