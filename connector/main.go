// Package main is the WASM source for aileron-connector-slack. It
// targets Go's native WASI Preview 1 (`GOOS=wasip1 GOARCH=wasm`) and
// calls into Aileron's host-import ABI for outbound HTTP and
// credential mediation.
//
// Build:
//
//	cd connector && GOOS=wasip1 GOARCH=wasm go build -trimpath \
//	  -ldflags="-s -w" -o ../connector.wasm .
//
// Or via Taskfile from the repo root:
//
//	task build
//
// I/O contract (stdin → stdout JSON):
//
//	{"op": "list_channels", "args": {"limit": 100, "types": "public_channel"}}
//	  → {"output": {"channels": [...]}}
//
//	{"op": "search_messages", "args": {"query": "from:@me deploy", "count": 20}}
//	  → {"output": {"messages": {...}}}
//
//	{"op": "send_message",
//	 "args": {"channel": "C0123456", "text": "Deploy is done."}}
//	  → {"output": {"ok": true, "channel": "...", "ts": "...", ...}}
//
//	{"error": {"class": "...", "message": "..."}}  on failure
//
// All outbound HTTP targets `slack.com:443`. The user's Slack OAuth
// token is bound as an oauth2 credential; the runtime sets
// `Authorization: Bearer <token>` host-side when an outbound request
// marks itself as `credential: "oauth2"`. The connector never sees
// the token bytes.
//
// Slack's public API has an unusual convention worth flagging: most
// endpoints return HTTP 200 even on logical failure, with the real
// status in `{"ok": false, "error": "<slack_error_code>"}` in the
// response body. okStatus + decodeOK handle both the transport-level
// status and the in-body `ok` flag so callers see a uniform
// success/failure signal.
//
// Errors:
//   - unauthorized: Slack returned 401, or the body says
//     `not_authed` / `invalid_auth` / `token_revoked` / `account_inactive`.
//     The bound token is rejected. Run
//     `aileron binding setup github://ALRubinger/aileron-connector-slack`
//     to re-consent.
//   - rate_limited: Slack returned 429 or the body says `ratelimited`.
//   - missing_scope: Slack returned `missing_scope` or `not_allowed_token_type`.
//     The OAuth grant is missing a scope the action requires. The
//     manifest declares the full scope set up front; this usually
//     means the user denied a scope at consent or the workspace admin
//     restricted it.
//   - external_api_error: any other Slack-reported error.
//   - connector_runtime_error: malformed input, unparseable response,
//     missing required arg.
//
// Idempotency: the read ops (list_channels, search_messages) are
// idempotent by their HTTP shape (GET-equivalents). send_message is
// NOT idempotent — calling it twice posts two messages. The action
// manifest for send-message MUST set [[execute]].idempotent = false
// so the runtime's retry layer (ADR-0010) does not double-post, and
// MUST gate on per-call user approval ([approval].required = true)
// since dispatched messages are not recoverable.
//
//go:build wasip1

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"unsafe"
)

//go:wasmimport aileron_host log
//go:noescape
func hostLog(levelPtr unsafe.Pointer, levelLen uint32, msgPtr unsafe.Pointer, msgLen uint32)

//go:wasmimport aileron_host http_request
//go:noescape
func hostHTTPRequest(reqPtr unsafe.Pointer, reqLen uint32) int32

//go:wasmimport aileron_host http_response_size
//go:noescape
func hostHTTPResponseSize() int32

//go:wasmimport aileron_host http_response_status
//go:noescape
func hostHTTPResponseStatus() int32

//go:wasmimport aileron_host http_response_read
//go:noescape
func hostHTTPResponseRead(dstPtr unsafe.Pointer, dstLen uint32) int32

// _emptyPtrSentinel keeps the address of an empty byte slice valid;
// Go can't take the address of an empty slice's first element directly.
var _emptyPtrSentinel = [1]byte{}

func ptr(b []byte) unsafe.Pointer {
	if len(b) == 0 {
		return unsafe.Pointer(&_emptyPtrSentinel[0])
	}
	return unsafe.Pointer(&b[0])
}

func aileronLog(level, message string) {
	lb := []byte(level)
	mb := []byte(message)
	hostLog(ptr(lb), uint32(len(lb)), ptr(mb), uint32(len(mb)))
}

// slackBase is the Slack public API root. Matches the
// [capabilities.network] declaration in manifest.toml; changing one
// without the other is a release-blocking validation error caught
// by the runtime gate.
const slackBase = "https://slack.com"

type input struct {
	Op   string         `json:"op"`
	Args map[string]any `json:"args"`
}

type output struct {
	Output map[string]any `json:"output,omitempty"`
	Error  *outputError   `json:"error,omitempty"`
}

type outputError struct {
	Class   string `json:"class"`
	Message string `json:"message"`
}

func main() {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		writeError("connector_runtime_error", "read_stdin: "+err.Error())
		os.Exit(1)
	}
	var in input
	if err := json.Unmarshal(raw, &in); err != nil {
		writeError("connector_runtime_error", "parse_input: "+err.Error())
		os.Exit(1)
	}

	switch in.Op {
	case "list_channels":
		listChannels(in.Args)
	case "search_messages":
		searchMessages(in.Args)
	case "send_message":
		sendMessage(in.Args)
	default:
		writeError("connector_runtime_error", "unknown op: "+in.Op)
		os.Exit(1)
	}
}

// listChannels calls Slack's conversations.list endpoint.
//
//	GET https://slack.com/api/conversations.list?limit={n}&types={t}&cursor={c}&exclude_archived=true
//
// Args:
//
//	limit              (number, optional) — page size; default 100, max 1000.
//	types              (string, optional) — comma-separated list of
//	                   "public_channel", "private_channel", "mpim",
//	                   "im"; default "public_channel".
//	cursor             (string, optional) — pagination cursor returned
//	                   from a previous call.
//	exclude_archived   (bool,   optional) — defaults to true (skip
//	                   archived channels) so the agent gets useful
//	                   targets only.
//
// Output: Slack's conversations.list response, including
// `channels[]` and `response_metadata.next_cursor` for pagination.
func listChannels(args map[string]any) {
	limit := readBoundedInt(args, "limit", 100, 1000)
	types, _ := args["types"].(string)
	if types == "" {
		types = "public_channel"
	}
	cursor, _ := args["cursor"].(string)

	excludeArchived := true
	if v, ok := args["exclude_archived"].(bool); ok {
		excludeArchived = v
	}

	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("types", types)
	q.Set("exclude_archived", strconv.FormatBool(excludeArchived))
	if cursor != "" {
		q.Set("cursor", cursor)
	}

	body, status, err := slackGet("/api/conversations.list?" + q.Encode())
	if err != nil {
		writeError("connector_runtime_error", "list_channels: "+err.Error())
		return
	}
	parsed, ok := decodeOK(status, body, "list_channels")
	if !ok {
		return
	}
	writeOutput(parsed)
}

// searchMessages calls Slack's search.messages endpoint.
//
//	GET https://slack.com/api/search.messages?query={q}&count={n}&sort={s}&page={p}
//
// search.messages requires a user token (xoxp); the manifest's
// `search:read` scope is user-scope-only.
//
// Args:
//
//	query  (string, required) — Slack search query.
//	       Examples: "from:@alice deploy", "in:#general before:2026-04-01",
//	       "has:link release notes".
//	count  (number, optional) — per-page result count; default 20, max 100.
//	sort   (string, optional) — "score" (relevance, default) or "timestamp".
//	page   (number, optional) — 1-based page index; default 1.
//
// Output: Slack's search.messages response. The `messages.matches`
// array carries the hits; each entry has `text`, `user`, `username`,
// `channel`, `ts`, and `permalink`.
func searchMessages(args map[string]any) {
	query, _ := args["query"].(string)
	if query == "" {
		writeError("connector_runtime_error", "search_messages: query is required")
		return
	}
	count := readBoundedInt(args, "count", 20, 100)
	sort, _ := args["sort"].(string)
	if sort == "" {
		sort = "score"
	}
	page := readBoundedInt(args, "page", 1, 100)

	q := url.Values{}
	q.Set("query", query)
	q.Set("count", strconv.Itoa(count))
	q.Set("sort", sort)
	q.Set("page", strconv.Itoa(page))

	body, status, err := slackGet("/api/search.messages?" + q.Encode())
	if err != nil {
		writeError("connector_runtime_error", "search_messages: "+err.Error())
		return
	}
	parsed, ok := decodeOK(status, body, "search_messages")
	if !ok {
		return
	}
	writeOutput(parsed)
}

// sendMessage posts a message via chat.postMessage.
//
//	POST https://slack.com/api/chat.postMessage
//	Content-Type: application/json
//	Body: {"channel": "...", "text": "...", "thread_ts": "..."?}
//
// Slack accepts chat.postMessage as either form-encoded or JSON; we
// use JSON because it is the documented path for `blocks` payloads
// and avoids URL-encoding surprises in text bodies with unicode or
// quotes. The Authorization header carries the user token, so the
// message posts as the authorizing user.
//
// Args:
//
//	channel    (string, required) — channel id (e.g. "C0123456") or
//	           channel name (e.g. "#general"). Slack accepts both.
//	text       (string, required) — message body. Will be approved by
//	           the user before this op runs (see action gating).
//	thread_ts  (string, optional) — parent message `ts` to reply in
//	           thread. Out of scope for v0.0.x's primary path, but
//	           supported because chat.postMessage takes it natively and
//	           agents that pass it should not be silently ignored.
//
// NOT idempotent. Action manifest MUST set
// [[execute]].idempotent = false and gate on per-call user approval.
func sendMessage(args map[string]any) {
	channel, _ := args["channel"].(string)
	text, _ := args["text"].(string)
	if channel == "" || text == "" {
		writeError("connector_runtime_error", "send_message: channel and text are required")
		return
	}

	payload := map[string]any{
		"channel": channel,
		"text":    text,
	}
	if threadTS, _ := args["thread_ts"].(string); threadTS != "" {
		payload["thread_ts"] = threadTS
	}

	reqBody, err := json.Marshal(payload)
	if err != nil {
		writeError("connector_runtime_error", "send_message: encode: "+err.Error())
		return
	}

	body, status, err := slackPostJSON("/api/chat.postMessage", reqBody)
	if err != nil {
		writeError("connector_runtime_error", "send_message: "+err.Error())
		return
	}
	parsed, ok := decodeOK(status, body, "send_message")
	if !ok {
		return
	}
	writeOutput(parsed)
}

// slackGet issues an authenticated GET against the Slack API base.
// The runtime injects the OAuth user token as `Authorization: Bearer
// <token>` host-side; the connector never sees the bytes.
func slackGet(path string) ([]byte, int, error) {
	return slackRequest("GET", path, nil, "")
}

// slackPostJSON issues an authenticated POST with a JSON body. Same
// credential injection rules as slackGet.
func slackPostJSON(path string, body []byte) ([]byte, int, error) {
	return slackRequest("POST", path, body, "application/json; charset=utf-8")
}

// slackRequest is the shared host-call helper. Builds the envelope,
// invokes aileron_host.http_request, reads the response.
//
// Returns (body, status, err). The host's structured *Error is on
// per-call state when rc != 0; this function returns a generic Go
// error so the caller can decide how to report it.
func slackRequest(method, path string, body []byte, contentType string) ([]byte, int, error) {
	headers := map[string]string{"Accept": "application/json"}
	if contentType != "" {
		headers["Content-Type"] = contentType
	}
	envelope := map[string]any{
		"method":     method,
		"url":        slackBase + path,
		"headers":    headers,
		"credential": "oauth2",
	}
	if body != nil {
		envelope["body"] = string(body)
	}
	req, err := json.Marshal(envelope)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal request: %w", err)
	}
	rc := hostHTTPRequest(ptr(req), uint32(len(req)))
	if rc != 0 {
		return nil, 0, fmt.Errorf("http_request denied or failed (rc=%d)", rc)
	}
	size := hostHTTPResponseSize()
	if size < 0 {
		return nil, 0, fmt.Errorf("http_response_size returned %d", size)
	}
	resp := make([]byte, size)
	if size > 0 {
		n := hostHTTPResponseRead(ptr(resp), uint32(size))
		if n < 0 {
			return nil, 0, fmt.Errorf("http_response_read returned %d", n)
		}
		resp = resp[:n]
	}
	return resp, int(hostHTTPResponseStatus()), nil
}

// decodeOK handles both the transport-level HTTP status and Slack's
// in-body `ok` flag. Slack typically returns HTTP 200 with
// `{"ok": false, "error": "<slack_code>"}` on logical errors, so a
// 2xx status by itself is insufficient to declare success.
//
// On failure decodeOK writes a structured error (mapping Slack's
// `error` codes to the connector's error classes) and returns ok=false.
// On success it returns the parsed body for the caller to emit.
//
// `opName` is used only for connector_runtime_error messages.
func decodeOK(status int, body []byte, opName string) (map[string]any, bool) {
	// Transport-level non-2xx is the rare path for Slack (rate-limit
	// 429 is the common one). Surface it before trying to parse JSON
	// since Slack may return HTML for some edge cases.
	if status == 429 {
		writeError("rate_limited",
			fmt.Sprintf("Slack rate-limited the request (HTTP 429). %s", tail(body, 256)))
		return nil, false
	}
	if status < 200 || status >= 300 {
		writeError("external_api_error",
			fmt.Sprintf("Slack returned HTTP %d: %s", status, tail(body, 512)))
		return nil, false
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		writeError("connector_runtime_error", opName+": parse: "+err.Error())
		return nil, false
	}
	ok, _ := parsed["ok"].(bool)
	if ok {
		return parsed, true
	}
	// Body says ok=false. Map Slack's error code to a connector class.
	slackErr, _ := parsed["error"].(string)
	switch slackErr {
	case "not_authed", "invalid_auth", "token_revoked", "account_inactive", "token_expired":
		writeError("unauthorized",
			"Slack rejected the bound OAuth token ("+slackErr+"). "+
				"Run `aileron binding setup github://ALRubinger/aileron-connector-slack` to re-consent.")
	case "missing_scope", "not_allowed_token_type":
		needed, _ := parsed["needed"].(string)
		provided, _ := parsed["provided"].(string)
		writeError("missing_scope",
			fmt.Sprintf("Slack rejected the request for scope reasons (%s). needed=%q provided=%q. Re-run `aileron binding setup` to grant the missing scope.",
				slackErr, needed, provided))
	case "ratelimited":
		writeError("rate_limited", "Slack rate-limited the request (body: ratelimited).")
	case "":
		writeError("external_api_error",
			fmt.Sprintf("Slack returned ok=false with no error code; body: %s", tail(body, 512)))
	default:
		writeError("external_api_error", "Slack error: "+slackErr)
	}
	return nil, false
}

func writeOutput(out map[string]any) {
	_ = json.NewEncoder(os.Stdout).Encode(output{Output: out})
}

func writeError(class, message string) {
	aileronLog("error", message)
	_ = json.NewEncoder(os.Stdout).Encode(output{Error: &outputError{Class: class, Message: message}})
}
