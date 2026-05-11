# aileron-connector-slack

Slack connector for [Aileron](https://docs.withaileron.ai). Gives your
agent send and read access to your Slack workspace via Slack's public
HTTP API, with the OAuth token held by Aileron's local vault and
injected host-side at the network boundary — the connector code never
sees the token bytes.

## How it works

```
agent ──→ Aileron daemon ──HTTPS──→ slack.com/api/*
                          (OAuth token injected host-side)
```

This connector is a v1-style native HTTP connector (the same shape as
[aileron-connector-google](https://github.com/ALRubinger/aileron-connector-google)).
Aileron's runtime drives the Slack OAuth flow at `aileron binding
setup` time, stores the resulting token in the local encrypted vault,
and sets `Authorization: Bearer <token>` on outbound requests at the
sandbox network boundary. The WASM connector inside the sandbox marks
each request with `credential: "oauth2"` and never holds the token.

## Install

```sh
# Trust this publisher once per machine (fetches keys/publisher.pub
# from this repo's default branch).
aileron keyring trust github://ALRubinger/aileron-connector-slack

# Install the connector at a specific tag.
aileron connector install github://ALRubinger/aileron-connector-slack@0.0.1

# Install the actions you want exposed to the agent.
aileron action add github://ALRubinger/aileron-connector-slack/actions/list-channels@0.0.1
aileron action add github://ALRubinger/aileron-connector-slack/actions/search-messages@0.0.1
aileron action add github://ALRubinger/aileron-connector-slack/actions/send-message@0.0.1

# Run the Slack OAuth dance — opens a browser, you consent in your
# workspace, the resulting user token (xoxp) lands in the local vault.
aileron binding setup github://ALRubinger/aileron-connector-slack
```

Then launch:

```sh
aileron launch claude
```

The agent now sees three Slack actions over MCP.

## Operations

| Op | Endpoint | Idempotent | Approval gate |
|---|---|---|---|
| `list_channels` | `GET slack.com/api/conversations.list` | yes | no |
| `search_messages` | `GET slack.com/api/search.messages` | yes | no |
| `send_message` | `POST slack.com/api/chat.postMessage` | **no** | **yes** |

`send_message` is gated by per-call user approval ([ADR-0009](https://docs.withaileron.ai/adr/0009-user-channel))
and is not idempotent. The Aileron runtime asks the user via the
launch-comms channel before Slack receives the post; on denial nothing
is sent.

## OAuth scopes

The connector requests the following user-token scopes at consent
time. Slack splits scopes by token type; everything here is on the
user-scope set so a single OAuth grant covers all three actions.

| Scope | Used by |
|---|---|
| `chat:write` | `send-message` — posts appear as the authorizing user |
| `channels:read` | `list-channels` |
| `users:read` | resolving user ids → display names in `search-messages` hits |
| `search:read` | `search-messages` (user-token-only API) |

`search.messages` is the load-bearing scope decision: it requires a
user token. Requesting `search:read` causes the OAuth dance to issue
both a bot and user token; Aileron's runtime uses the user token for
all three ops. The result: `send-message` posts as the user (not as
an "Aileron app" bot), which is the desired behavior for the Monday
catch-up demo.

## Error classes

The connector emits structured errors per [ADR-0010](https://docs.withaileron.ai/adr/0010-failure-handling).
Slack returns most logical failures as HTTP 200 with `{"ok": false,
"error": "<code>"}`; the connector translates both transport-level
and in-body errors to the same class set.

| Class | When |
|---|---|
| `unauthorized` | HTTP 401, or `ok=false` with error `not_authed` / `invalid_auth` / `token_revoked` / `account_inactive` / `token_expired`. Re-run `aileron binding setup`. |
| `missing_scope` | `ok=false` with error `missing_scope` or `not_allowed_token_type`. The OAuth grant is missing a scope the action requires. Re-run `aileron binding setup` to grant it. |
| `rate_limited` | HTTP 429 or `ok=false` with error `ratelimited`. |
| `external_api_error` | Any other Slack-reported error. |
| `connector_runtime_error` | Malformed input, unparseable response, or a missing required arg. |

## Build from source

```sh
task build       # produces connector.wasm
task test        # unit tests + wasip1 build smoke test
task pack        # builds the local tarball (offline signing path)
task pack:hash   # prints the canonical-hash the release pipeline computes
```

The release pipeline is composite actions from
[aileron-actions](https://github.com/ALRubinger/aileron-actions); each
`uses:` in `.github/workflows/release.yml` is SHA-pinned for
supply-chain trust. Pushing a `vX.Y.Z` tag triggers the full
build → substitute-client-secret → sign → publish chain.

## Demo path

```sh
# Trust + install (one-time per machine):
aileron keyring trust github://ALRubinger/aileron-connector-slack
aileron connector install github://ALRubinger/aileron-connector-slack@0.0.1
aileron action add github://ALRubinger/aileron-connector-slack/actions/send-message@0.0.1
aileron binding setup github://ALRubinger/aileron-connector-slack

# Launch and prompt:
aileron launch claude
# > "post a test message in #general"
# Aileron prompts you for approval — approve, message lands.
```

## Capability declarations

The connector manifest declares:

- **`[capabilities.network] hosts = ["slack.com:443"]`** — only
  Slack's public API root. Anything else is denied at the sandbox
  boundary.
- **`[capabilities.credential] kind = "oauth2"`** — the user's Slack
  OAuth token. The runtime injects it as
  `Authorization: Bearer <token>` host-side; the connector never sees
  the bytes.

## Slack app setup (publisher side)

This connector ships with a Slack OAuth app (`client_id`) registered
by the publisher; users do not register their own apps. Per ADR-0006
the runtime drives the OAuth dance via PKCE.

### `client_secret` is bound at release time

Slack rejects committed `xoxe`/`xoxs` style client secrets through
its own secret scanner, and GitHub's secret scanner forwards detected
Slack secrets to Slack for auto-rotation. Per ADR-0002, the value
ships in the connector binary the same way `gcloud` and `gh` ship
their bundled secrets, but it is **never committed to this source
repo**.

The committed source manifest carries
`client_secret = "bound-at-release"` as a placeholder. The release
workflow substitutes it from the `SLACK_OAUTH_CLIENT_SECRET`
repository secret before signing and packing — same template
pattern as the connector content hash and the version. The bound
value lives only in the signed connector tarball.

Publisher one-time setup:

1. https://api.slack.com/apps → Create New App → From scratch.
   Name: **Aileron Connector — Slack**. Pick a workspace for
   development.
2. OAuth & Permissions → Scopes → User Token Scopes: add
   `chat:write`, `channels:read`, `users:read`, `search:read`.
3. OAuth & Permissions → Redirect URLs: add the redirect URL
   Aileron's local OAuth flow listens on
   (`http://localhost:PORT/callback` — see ADR-0006).
4. Basic Information → App Credentials → copy the **Client ID** into
   `connector/manifest.toml` (replace `REPLACE_WITH_SLACK_CLIENT_ID`).
   Commit that change.
5. Copy the **Client Secret** to repo Settings → Secrets and
   variables → Actions → New repository secret. Name:
   `SLACK_OAUTH_CLIENT_SECRET`. Paste the value. Save.
6. Manage Distribution → enable public distribution (or keep in
   development for the v2 demo; analogous to Google's "Testing"
   publishing status).

If the secret rotates (manually or because Slack detected an exposed
value), update the same Actions secret; the next `vX.Y.Z` push picks
up the new value automatically.

### Distribution mode for v2

The Slack app stays in **Distribution mode** (the post-development
state for apps that allow installs by users outside the development
workspace) for the v2 demo. Slack's review process is lighter than
Google's — for the scope set this connector requests, the path is:

1. Build the app in a development workspace.
2. Toggle "Distribute App" → "Activate Public Distribution".
3. The app is then installable into any workspace whose admin allows
   it (most workspaces allow user-installable apps by default; some
   gate behind admin approval).

OAuth verification submission for any scope changes Slack flags as
sensitive is tracked separately and is out of scope for v0.0.x —
the v2 launch needs the demo path, not the marketplace listing.

## Trusting this publisher

To install connectors from this repo, add the public key from
`keys/publisher.pub` to your local keyring. See
[`keys/README.md`](keys/README.md) for the exact procedure.

Without the public key in the keyring, `aileron connector install`
fails closed — see ADR-0004's verification rules in the Aileron docs.

## See also

- [ADR-0002: Connector Model](https://docs.withaileron.ai/adr/0002-connector-model)
- [ADR-0005: Sandbox + Credential Mediation](https://docs.withaileron.ai/adr/0005-credential-mediation)
- [ADR-0006: OAuth Flow](https://docs.withaileron.ai/adr/0006-oauth-flow)
- [ADR-0009: User Channel and Approval Surfaces](https://docs.withaileron.ai/adr/0009-user-channel)
- [aileron-connector-google](https://github.com/ALRubinger/aileron-connector-google) — the v1 reference connector this one is modeled on
- [Slack API: chat.postMessage](https://api.slack.com/methods/chat.postMessage)
- [Slack API: conversations.list](https://api.slack.com/methods/conversations.list)
- [Slack API: search.messages](https://api.slack.com/methods/search.messages)

## License

Apache 2.0. See [LICENSE](LICENSE).
