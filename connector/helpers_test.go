package main

import (
	"strings"
	"testing"
)

func TestReadBoundedInt_DefaultsWhenMissing(t *testing.T) {
	if got := readBoundedInt(nil, "limit", 100, 1000); got != 100 {
		t.Errorf("nil args: got %d, want default 100", got)
	}
	if got := readBoundedInt(map[string]any{}, "limit", 100, 1000); got != 100 {
		t.Errorf("empty args: got %d, want default 100", got)
	}
	if got := readBoundedInt(map[string]any{"other": 50.0}, "limit", 100, 1000); got != 100 {
		t.Errorf("missing key: got %d, want default 100", got)
	}
}

func TestReadBoundedInt_DefaultsOnNonNumeric(t *testing.T) {
	cases := []any{
		"50",        // strings are rejected — JSON would deliver a number
		[]int{1, 2}, // slice
		nil,         // explicit nil
		true,        // bool
	}
	for _, v := range cases {
		args := map[string]any{"limit": v}
		if got := readBoundedInt(args, "limit", 100, 1000); got != 100 {
			t.Errorf("value %v (%T): got %d, want default 100", v, v, got)
		}
	}
}

func TestReadBoundedInt_AcceptsFloat64AndInt(t *testing.T) {
	if got := readBoundedInt(map[string]any{"limit": 42.0}, "limit", 100, 1000); got != 42 {
		t.Errorf("float64 42: got %d, want 42", got)
	}
	if got := readBoundedInt(map[string]any{"limit": 42}, "limit", 100, 1000); got != 42 {
		t.Errorf("int 42: got %d, want 42", got)
	}
}

func TestReadBoundedInt_ClampsToCap(t *testing.T) {
	if got := readBoundedInt(map[string]any{"limit": 5000.0}, "limit", 100, 1000); got != 1000 {
		t.Errorf("clamp float64: got %d, want 1000", got)
	}
	if got := readBoundedInt(map[string]any{"limit": 5000}, "limit", 100, 1000); got != 1000 {
		t.Errorf("clamp int: got %d, want 1000", got)
	}
}

func TestReadBoundedInt_NonPositiveFallsBackToDefault(t *testing.T) {
	// The agent might ship limit=0 (meaning "no preference") or a
	// negative as a malformed input. Either way the contract is the
	// default, not "fetch zero rows" — that would surprise the agent
	// with empty results from a request it expected to work.
	if got := readBoundedInt(map[string]any{"limit": 0.0}, "limit", 100, 1000); got != 100 {
		t.Errorf("zero: got %d, want default 100", got)
	}
	if got := readBoundedInt(map[string]any{"limit": -10}, "limit", 100, 1000); got != 100 {
		t.Errorf("negative: got %d, want default 100", got)
	}
}

func TestTail_LeavesShortInputAlone(t *testing.T) {
	got := tail([]byte("short"), 100)
	if got != "short" {
		t.Errorf("got %q, want %q", got, "short")
	}
}

func TestTail_TruncatesLongInputWithEllipsis(t *testing.T) {
	long := strings.Repeat("x", 200)
	got := tail([]byte(long), 50)
	if !strings.HasPrefix(got, "...") {
		t.Errorf("expected ... prefix; got %q", got[:10])
	}
	if !strings.HasSuffix(got, strings.Repeat("x", 50)) {
		t.Errorf("expected last 50 x's; got %q", got)
	}
}

func TestTail_HandlesEmpty(t *testing.T) {
	if got := tail(nil, 100); got != "" {
		t.Errorf("nil: got %q", got)
	}
	if got := tail([]byte{}, 100); got != "" {
		t.Errorf("empty: got %q", got)
	}
}
