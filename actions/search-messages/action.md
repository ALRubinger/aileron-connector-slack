+++
name = "search-messages"
# `version` and the `0.0.0-dev` markers in `source` and the
# `[[requires.connectors]]` block are placeholders. CI substitutes
# them with the real version (from the pushed tag) into a build copy
# of this manifest before signing and packing. Source stays template;
# only the published tarball carries the real version.
version = "0.0.0-dev"
source = "github://ALRubinger/aileron-connector-slack/actions/search-messages@0.0.0-dev"

[[requires.connectors]]
name = "github://ALRubinger/aileron-connector-slack"
version = "0.0.0-dev"
# `hash` is the connector tarball's content-addressed identity per
# ADR-0002. CI substitutes this placeholder with the real hash at
# release time (see .github/workflows/release.yml).
hash = "sha256:bound-at-release"
capabilities = ["search_messages"]

[match]
intent = "search Slack message history matching a query"

[[execute]]
id = "search"
connector = "github://ALRubinger/aileron-connector-slack"
op = "search_messages"

[execute.inputs]
query = "${args.query}"
count = "${args.count}"
sort = "${args.sort}"
page = "${args.page}"

[[inputs]]
name = "query"
type = "string"
description = "Slack search query. Supports Slack's modifiers: from:@user, in:#channel, before:YYYY-MM-DD, after:YYYY-MM-DD, has:link, has:reaction. Example: \"from:@alice deploy in:#release-notes after:2026-04-01\"."
required = true

[[inputs]]
name = "count"
type = "integer"
description = "Results per page. Default 20, max 100."
required = false

[[inputs]]
name = "sort"
type = "string"
description = "\"score\" (relevance, default) or \"timestamp\" (newest first)."
required = false

[[inputs]]
name = "page"
type = "integer"
description = "1-based page index for paging through results. Default 1."
required = false
+++

# Search Slack Messages

Searches the user's Slack message history using Slack's native search
syntax (the same one the search bar in the Slack client accepts).

When it fires:
- "find Bob's message about the deploy from last week"
- "what did the team say about the migration in #infra?"
- "search Slack for links to the design doc"

This action is **read-only** and idempotent.

Output is Slack's raw [search.messages](https://api.slack.com/methods/search.messages)
response envelope. The `messages.matches[]` array carries the hits;
each entry has `text`, `user`, `username`, `channel.name`, `ts`, and
`permalink`. Use `permalink` to jump to the message in Slack;
`channel.id` + `ts` are also accepted by other actions that need a
message reference (e.g. passing `thread_ts` into `send-message` to
reply in thread).

**Requires a user token.** `search.messages` is one of Slack's
user-token-only APIs (workspace search). The manifest declares
`search:read` on the user-scope set, so the OAuth dance issues a user
token (xoxp) that this op uses. If `search:read` is missing from the
grant, the op returns a `missing_scope` error.

The connector runs in the Aileron WASM sandbox with
`[capabilities.network]` restricted to `slack.com:443`, and the OAuth
bearer token is injected host-side at the network boundary — the
connector code never sees the token.
