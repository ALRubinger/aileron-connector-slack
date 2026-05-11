+++
name = "send-message"
# `version` and the `0.0.0-dev` markers in `source` and the
# `[[requires.connectors]]` block are placeholders. CI substitutes
# them with the real version (from the pushed tag) into a build copy
# of this manifest before signing and packing. Source stays template;
# only the published tarball carries the real version.
version = "0.0.0-dev"
source = "github://ALRubinger/aileron-connector-slack/actions/send-message@0.0.0-dev"

[[requires.connectors]]
name = "github://ALRubinger/aileron-connector-slack"
version = "0.0.0-dev"
# `hash` is the connector tarball's content-addressed identity per
# ADR-0002. CI substitutes this placeholder with the real hash at
# release time (see .github/workflows/release.yml).
hash = "sha256:bound-at-release"
capabilities = ["send_message"]

[match]
intent = "send a Slack message to a channel on behalf of the user"

[[execute]]
id = "send"
connector = "github://ALRubinger/aileron-connector-slack"
op = "send_message"
idempotent = false

[execute.inputs]
channel = "${args.channel}"
text = "${args.text}"
thread_ts = "${args.thread_ts}"

# Per-call approval gate. The runtime asks the user via the
# launch-comms channel before Slack receives the post (per ADR-0009 —
# agent in trust path for irreversible actions). On approval the
# connector runs; on denial the connector is never invoked, no
# message leaves, and the runtime audit-logs the deny. send_message
# is gated because dispatched Slack messages are not recoverable —
# Slack supports a brief edit window but no retraction, and threads /
# notifications fire on the original post.
[approval]
required = true

[[inputs]]
name = "channel"
type = "string"
description = "Channel id (e.g. \"C0123456\") or channel name (e.g. \"#general\"). DM ids (Dxxxx) and group ids (Gxxxx) are also accepted by Slack."
required = true

[[inputs]]
name = "text"
type = "string"
description = "The message body to post. The user will be asked to approve this exact text before Slack receives it."
required = true

[[inputs]]
name = "thread_ts"
type = "string"
description = "Optional parent message timestamp (e.g. \"1683750000.000100\") to reply in thread. Omit to post a top-level message."
required = false
+++

# Send a Slack Message

Posts a message to a Slack channel as the authorizing user.

When it fires:
- "tell #deploys that the rollout finished"
- "DM Alice that I'll be five minutes late"
- "reply in the thread that I've taken a look"

This action is **gated on per-call user approval**. When the agent
calls `send_message`, the Aileron runtime pauses the call and asks
the user to approve via the launch-comms channel (CLI prompt or the
webapp `/approvals` surface). Slack is not contacted until approval
is granted. On denial the call returns an error to the agent and is
recorded in the audit log; no message is posted.

This action writes to your Slack (posts a message). It is **not
idempotent** — invoking it twice posts two messages. The runtime's
retry layer is configured to honor that and will not double-post on
transient failure.

The post is made with the user's OAuth token, so the message appears
authored by the user (not as an "Aileron bot"). Channel ids and
channel names are both accepted. To reply inside an existing thread,
pass `thread_ts` from a `search-messages` or other prior result.

The connector runs in the Aileron WASM sandbox with
`[capabilities.network]` restricted to `slack.com:443`, and the OAuth
bearer token is injected host-side at the network boundary — the
connector code never sees the token. See ADR-0005 (sandbox +
credential mediation) and ADR-0009 (user channel — agent in trust
path) in the Aileron docs.
