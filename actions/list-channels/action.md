+++
name = "list-channels"
# `version` and the `0.0.0-dev` markers in `source` and the
# `[[requires.connectors]]` block are placeholders. CI substitutes
# them with the real version (from the pushed tag) into a build copy
# of this manifest before signing and packing. Source stays template;
# only the published tarball carries the real version.
version = "0.0.0-dev"
source = "github://ALRubinger/aileron-connector-slack/actions/list-channels@0.0.0-dev"

[[requires.connectors]]
name = "github://ALRubinger/aileron-connector-slack"
version = "0.0.0-dev"
# `hash` is the connector tarball's content-addressed identity per
# ADR-0002. CI substitutes this placeholder with the real hash at
# release time (see .github/workflows/release.yml).
hash = "sha256:bound-at-release"
capabilities = ["list_channels"]

[match]
intent = "list the Slack channels the user has access to"

[[execute]]
id = "list"
connector = "github://ALRubinger/aileron-connector-slack"
op = "list_channels"

[execute.inputs]
limit = "${args.limit}"
types = "${args.types}"
cursor = "${args.cursor}"
exclude_archived = "${args.exclude_archived}"

[[inputs]]
name = "limit"
type = "integer"
description = "How many channels to return per page. Default 100, max 1000."
required = false

[[inputs]]
name = "types"
type = "string"
description = "Comma-separated channel types: \"public_channel\", \"private_channel\", \"mpim\", \"im\". Default \"public_channel\"."
required = false

[[inputs]]
name = "cursor"
type = "string"
description = "Pagination cursor from a previous call's response_metadata.next_cursor. Omit for the first page."
required = false

[[inputs]]
name = "exclude_archived"
type = "boolean"
description = "Whether to skip archived channels. Default true."
required = false
+++

# List Slack Channels

Returns the list of Slack channels the user has access to in the
workspace, with each entry's `id`, `name`, `topic`, `purpose`,
`is_member`, and so on.

When it fires:
- "what Slack channels are open to me?"
- "find the deploys channel"
- "is there a #release-notes channel?"

This action is **read-only** and idempotent. The agent can call it
repeatedly without side effects; Slack returns the same response for
the same query (modulo new channel creation).

Output is Slack's raw [conversations.list](https://api.slack.com/methods/conversations.list)
response envelope: `channels[]` plus
`response_metadata.next_cursor` for paging. By default only public
channels are returned; pass `types="public_channel,private_channel"`
to include private channels the user belongs to (requires
`groups:read` scope, not granted at v0.0.x).

The connector runs in the Aileron WASM sandbox with
`[capabilities.network]` restricted to `slack.com:443`, and the OAuth
bearer token is injected host-side at the network boundary — the
connector code never sees the token.
