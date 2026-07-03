// Copyright (c) the go-ruby-json/json authors
//
// SPDX-License-Identifier: BSD-3-Clause

package json

import (
	"strconv"
	"strings"
	"testing"
)

// flatObject builds `{"k0":0,"k1":1,...,"k(n-1)":n-1}` — a single object with n
// distinct short keys, used to exercise the key cache/index and arena overflow.
func flatObject(n int) string {
	var b strings.Builder
	b.WriteByte('{')
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`"k`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`":`)
		b.WriteString(strconv.Itoa(i))
	}
	b.WriteByte('}')
	return b.String()
}

// wantFlatObject asserts v is an n-entry Map with k<i> == int64(i) in order.
func wantFlatObject(t *testing.T, v Value, n int) {
	t.Helper()
	m, ok := v.(*Map)
	if !ok {
		t.Fatalf("not a *Map: %T", v)
	}
	if m.Len() != n {
		t.Fatalf("Len = %d, want %d", m.Len(), n)
	}
	for i := 0; i < n; i++ {
		key := "k" + strconv.Itoa(i)
		got, ok := m.Get(key)
		if !ok {
			t.Fatalf("missing key %q", key)
		}
		if got != int64(i) {
			t.Fatalf("key %q = %v, want %d", key, got, i)
		}
	}
	if p := m.Pairs(); len(p) == n && p[0].Key != "k0" {
		t.Fatalf("first key = %v, want k0 (order not preserved)", p[0].Key)
	}
}

// TestParseEmptyInput covers value() reached at end of input (empty and
// whitespace-only documents are parse errors).
func TestParseEmptyInput(t *testing.T) {
	for _, s := range []string{"", "   ", "\t\n\r "} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q): want error, got nil", s)
		}
	}
}

// TestParseControlBetweenTokens covers skipSpace returning on a sub-0x20 byte
// that is not JSON whitespace (it is not skipped; the parser then errors).
func TestParseControlBetweenTokens(t *testing.T) {
	if _, err := Parse("[1,\x011]"); err == nil {
		t.Errorf("want error for control char between tokens")
	}
}

// TestParseIntegerBoundaries covers parseInt64's int64/​big.Int boundary: the
// exact 64-bit limits stay int64, one past each limit (and any 20+ digit run)
// becomes *big.Int, with the value preserved.
func TestParseIntegerBoundaries(t *testing.T) {
	int64Cases := map[string]int64{
		"9223372036854775807":  9223372036854775807,  // MaxInt64
		"-9223372036854775808": -9223372036854775808, // MinInt64
		"0":                    0,
		"-1":                   -1,
	}
	for s, want := range int64Cases {
		v := mustParse(t, s)
		if v != want {
			t.Errorf("Parse(%q) = %v (%T), want int64 %d", s, v, v, want)
		}
	}
	bigCases := []string{
		"9223372036854775808",   // MaxInt64 + 1
		"-9223372036854775809",  // MinInt64 - 1
		"12345678901234567890",  // 20 digits
		"-12345678901234567890", // 20 digits, negative
		"100000000000000000000000000000",
	}
	for _, s := range bigCases {
		v := mustParse(t, s)
		if bigStr(v) != s {
			t.Errorf("Parse(%q) = %v (%T), want *big.Int %s", s, v, v, s)
		}
	}
}

// TestParseManyKeysObject covers the key-index migration (past keyCacheMax
// distinct keys), the id>=64 duplicate-scan fallback, buildMap's large-object
// index path, and the pair-arena overflow — all via one big flat object.
func TestParseManyKeysObject(t *testing.T) {
	const n = 300 // > keyCacheMax (32), > 64 (bitmask width), > pairSlabChunk (256)
	v := mustParse(t, flatObject(n))
	wantFlatObject(t, v, n)
}

// TestParseRepeatedKeysAcrossObjects covers the key-index *lookup hit* path: the
// second object's keys all resolve through the migrated index.
func TestParseRepeatedKeysAcrossObjects(t *testing.T) {
	obj := flatObject(40) // > keyCacheMax so the index migrates on the first object
	v := mustParse(t, "["+obj+","+obj+"]")
	arr, ok := v.([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("want 2-element array, got %T", v)
	}
	wantFlatObject(t, arr[0], 40)
	wantFlatObject(t, arr[1], 40)
}

// TestParseDuplicateKeysSmall covers buildMap's small-object last-wins dedup.
func TestParseDuplicateKeysSmall(t *testing.T) {
	m := mustParse(t, `{"a":1,"b":2,"a":3}`).(*Map)
	if m.Len() != 2 {
		t.Fatalf("Len = %d, want 2", m.Len())
	}
	if got, _ := m.Get("a"); got != int64(3) {
		t.Errorf("a = %v, want 3 (last wins)", got)
	}
	if m.Pairs()[0].Key != "a" || m.Pairs()[1].Key != "b" {
		t.Errorf("order not preserved: %v", m.Pairs())
	}
}

// TestParseDuplicateKeysLarge covers buildMap's large-object index dedup,
// including the overwrite of an earlier pair's value (last wins).
func TestParseDuplicateKeysLarge(t *testing.T) {
	var b strings.Builder
	b.WriteByte('{')
	for i := 0; i < 20; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`"k` + strconv.Itoa(i) + `":` + strconv.Itoa(i))
	}
	b.WriteString(`,"k0":999}`) // duplicate of the first key in a >16-pair object
	m := mustParse(t, b.String()).(*Map)
	if m.Len() != 20 {
		t.Fatalf("Len = %d, want 20", m.Len())
	}
	if got, _ := m.Get("k0"); got != int64(999) {
		t.Errorf("k0 = %v, want 999 (last wins)", got)
	}
}

// TestParseArrayArenaOverflow covers the element-arena overflow (a flat array
// with more elements than the length-seeded slab).
func TestParseArrayArenaOverflow(t *testing.T) {
	const n = 400
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(i))
	}
	b.WriteByte(']')
	arr, ok := mustParse(t, b.String()).([]any)
	if !ok || len(arr) != n {
		t.Fatalf("want %d-element array, got %T len %d", n, arr, len(arr))
	}
	for i := 0; i < n; i++ {
		if arr[i] != int64(i) {
			t.Fatalf("elem %d = %v", i, arr[i])
		}
	}
}

// TestParseHugeDocumentSeedClamp covers seed()'s cap clamp for a document large
// enough that the length-derived slab estimate is capped (and then grows).
func TestParseHugeDocumentSeedClamp(t *testing.T) {
	const objs = 4000 // past the seed cap (needs docLen > 8192*12 ≈ 98 KB)
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; i < objs; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"id":` + strconv.Itoa(i) + `,"name":"item-` + strconv.Itoa(i) + `"}`)
	}
	b.WriteByte(']')
	if len(b.String()) <= 8192*12 {
		t.Fatalf("test document too small (%d bytes) to exercise the seed clamp", b.Len())
	}
	arr, ok := mustParse(t, b.String()).([]any)
	if !ok || len(arr) != objs {
		t.Fatalf("want %d-element array, got %T len %d", objs, arr, len(arr))
	}
	last := arr[objs-1].(*Map)
	if got, _ := last.Get("id"); got != int64(objs-1) {
		t.Errorf("last id = %v, want %d", got, objs-1)
	}
}
