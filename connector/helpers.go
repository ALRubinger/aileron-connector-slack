// Pure helpers for the connector — no host-import dependencies, so
// this file builds on every Go target (including the host platform).
// main.go is wasip1-only because it imports the aileron_host module;
// keeping these helpers in a separate, untagged file lets `go test`
// exercise them as ordinary Go unit tests.

package main

// readBoundedInt extracts a numeric arg from `args` with a default
// and an upper bound. JSON decodes numbers as float64 by default;
// this normalises to int with sensible bounds so the agent cannot
// blow past Slack's page-size limits or request absurdly expensive
// scans.
//
// Zero or negative inputs fall back to def (the default). Inputs
// above cap are clamped down to cap. Anything that is not a number
// (string, nil, missing) returns def.
func readBoundedInt(args map[string]any, key string, def, cap int) int {
	v, ok := args[key]
	if !ok {
		return def
	}
	switch n := v.(type) {
	case float64:
		i := int(n)
		if i <= 0 {
			return def
		}
		if i > cap {
			return cap
		}
		return i
	case int:
		if n <= 0 {
			return def
		}
		if n > cap {
			return cap
		}
		return n
	default:
		return def
	}
}

// tail returns the last n bytes of b as a string, with a leading
// "..." when truncation occurred. Used to bound error messages so a
// large Slack error response does not flood the agent's context
// window.
func tail(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return "..." + string(b[len(b)-n:])
}
