package rangeserver

import (
	"strings"
	"testing"
)

func TestParseRange(t *testing.T) {
	const ok, ign, uns = RangeOK, RangeIgnored, RangeUnsatisfiable
	huge := "99999999999999999999999999"
	tests := []struct {
		v          string
		size       int64
		start, end int64
		res        RangeResult
	}{
		// int-range
		{"bytes=0-99", 1000, 0, 99, ok},
		{"bytes=0-0", 1000, 0, 0, ok},
		{"bytes=999-999", 1000, 999, 999, ok},
		{"bytes=100-", 1000, 100, 999, ok},
		{"bytes=0-", 1000, 0, 999, ok},
		{"bytes=990-2000", 1000, 990, 999, ok},
		{"bytes=0-" + huge, 1000, 0, 999, ok},
		{"bytes=0007-0009", 1000, 7, 9, ok},
		{"BYTES=0-9", 1000, 0, 9, ok},
		{"Bytes=0-9", 1000, 0, 9, ok},
		// suffix-range
		{"bytes=-10", 1000, 990, 999, ok},
		{"bytes=-1000", 1000, 0, 999, ok},
		{"bytes=-5000", 1000, 0, 999, ok},
		{"bytes=-" + huge, 1000, 0, 999, ok},
		{"bytes=-0", 1000, 0, 0, uns},
		{"bytes=-000", 1000, 0, 0, uns},
		// unsatisfiable first-pos
		{"bytes=1000-", 1000, 0, 0, uns},
		{"bytes=1000-1005", 1000, 0, 0, uns},
		{"bytes=5000-6000", 1000, 0, 0, uns},
		{"bytes=" + huge + "-", 1000, 0, 0, uns},
		// last < first is invalid, even past EOF
		{"bytes=100-99", 1000, 0, 0, ign},
		{"bytes=2000-1500", 1000, 0, 0, ign},
		// whitespace: OWS around list elements only
		{"bytes= 0-99", 1000, 0, 99, ok},
		{"bytes=0-99 ", 1000, 0, 99, ok},
		{"bytes=\t0-99\t", 1000, 0, 99, ok},
		{"bytes=0 -99", 1000, 0, 0, ign},
		{"bytes=0- 99", 1000, 0, 0, ign},
		{"bytes =0-99", 1000, 0, 0, ign},
		{" bytes=0-99", 1000, 0, 0, ign},
		{"bytes=- 10", 1000, 0, 0, ign},
		// empty list elements are skipped
		{"bytes=0-99,", 1000, 0, 99, ok},
		{"bytes=,0-99", 1000, 0, 99, ok},
		{"bytes=, ,0-99, ,", 1000, 0, 99, ok},
		// invalid syntax
		{"", 1000, 0, 0, ign},
		{"bytes", 1000, 0, 0, ign},
		{"bytes=", 1000, 0, 0, ign},
		{"bytes=,", 1000, 0, 0, ign},
		{"bytes=-", 1000, 0, 0, ign},
		{"bytes=abc", 1000, 0, 0, ign},
		{"bytes=a-9", 1000, 0, 0, ign},
		{"bytes=0-9a", 1000, 0, 0, ign},
		{"bytes=+1-9", 1000, 0, 0, ign},
		{"bytes=-+9", 1000, 0, 0, ign},
		{"bytes=--9", 1000, 0, 0, ign},
		{"bytes=0-9-10", 1000, 0, 0, ign},
		{"bytes=1e3-", 1000, 0, 0, ign},
		{"bytes=0x10-", 1000, 0, 0, ign},
		{"bytes=0=9", 1000, 0, 0, ign},
		{"bytes=٣-9", 1000, 0, 0, ign},
		// multiple ranges are ignored
		{"bytes=0-1,5-6", 1000, 0, 0, ign},
		{"bytes=0-1, -5", 1000, 0, 0, ign},
		{"bytes=2000-,3000-", 1000, 0, 0, ign},
		// other units
		{"items=0-9", 1000, 0, 0, ign},
		{"none", 1000, 0, 0, ign},
		{"bytess=0-9", 1000, 0, 0, ign},
		// empty representation
		{"bytes=0-", 0, 0, 0, uns},
		{"bytes=0-0", 0, 0, 0, uns},
		{"bytes=-0", 0, 0, 0, uns},
		{"bytes=-10", 0, 0, 0, ign},
		// one-byte representation
		{"bytes=-10", 1, 0, 0, ok},
		{"bytes=0-10", 1, 0, 0, ok},
		{"bytes=1-", 1, 0, 0, uns},
	}
	for _, tc := range tests {
		start, end, res := ParseRange(tc.v, tc.size)
		if start != tc.start || end != tc.end || res != tc.res {
			t.Errorf("ParseRange(%q, %d) = %d, %d, %d; want %d, %d, %d", tc.v, tc.size, start, end, res, tc.start, tc.end, tc.res)
		}
	}
}

func TestETagLists(t *testing.T) {
	const e = `"sha256-0123456789abcdef"`
	tests := []struct {
		list         string
		strong, weak bool
	}{
		{e, true, true},
		{`*`, true, true},
		{` * `, true, true},
		{`W/` + e, false, true},
		{`"x", ` + e, true, true},
		{`"x",` + e + `,`, true, true},
		{`, "x" ,, ` + e, true, true},
		{`"x", "y"`, false, false},
		{`"x"`, false, false},
		{`"sha256-0123456789abcdef`, false, false}, // unterminated
		{`sha256-0123456789abcdef`, false, false},  // unquoted
		{e + ` junk`, false, false},
		{`"a b"`, false, false}, // space is not etagc
		{`"a,` + e[1:], false, false},
		{`*, ` + e, false, false},
		{``, false, false},
	}
	for _, tc := range tests {
		if got := etagListMatch(tc.list, e, true); got != tc.strong {
			t.Errorf("strong %q = %v, want %v", tc.list, got, tc.strong)
		}
		if got := etagListMatch(tc.list, e, false); got != tc.weak {
			t.Errorf("weak %q = %v, want %v", tc.list, got, tc.weak)
		}
	}
	// A weak current tag never matches strongly.
	if etagListMatch(`W/"a"`, `W/"a"`, true) || !etagListMatch(`W/"a"`, `W/"a"`, false) {
		t.Error("weak current tag comparison")
	}
	for v, want := range map[string]bool{e: true, `W/` + e: false, `"a"`: true, `"a", "b"`: false, `*`: false, ``: false, `"a" `: false} {
		if got := isStrongETag(v); got != want {
			t.Errorf("isStrongETag(%q) = %v", v, got)
		}
	}
	if s := strings.Repeat(`"x",`, 100); !etagListMatch(s+e, e, true) {
		t.Error("long list")
	}
}
