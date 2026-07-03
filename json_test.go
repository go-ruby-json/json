// Copyright (c) the go-ruby-json/json authors
//
// SPDX-License-Identifier: BSD-3-Clause

package json

import (
	"math"
	"math/big"
	"reflect"
	"strconv"
	"testing"
)

// mustParse parses s, failing the test on error.
func mustParse(t *testing.T, s string, opts ...Option) Value {
	t.Helper()
	v, err := Parse(s, opts...)
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	return v
}

// mustGen generates v compactly, failing the test on error.
func mustGen(t *testing.T, v Value, opts ...Option) string {
	t.Helper()
	s, err := Generate(v, opts...)
	if err != nil {
		t.Fatalf("Generate(%#v): %v", v, err)
	}
	return s
}

// bigStr is the canonical decimal string of a *big.Int Value.
func bigStr(v Value) string {
	if b, ok := v.(*big.Int); ok {
		return b.String()
	}
	return ""
}

// TestGenerateScalars covers every scalar branch of the generator.
func TestGenerateScalars(t *testing.T) {
	bi, _ := new(big.Int).SetString("100000000000000000000000000000", 10)
	cases := []struct {
		v    Value
		want string
	}{
		{nil, "null"},
		{true, "true"},
		{false, "false"},
		{42, "42"},
		{int64(-7), "-7"},
		{bi, "100000000000000000000000000000"},
		{float32(1.5), "1.5"},
		{3.14, "3.14"},
		{2.0, "2.0"},
		{1e100, "1e+100"},
		{1e-7, "0.0000001"},
		{0.1, "0.1"},
		{0.0, "0.0"},
		{math.Copysign(0, -1), "-0.0"},
		{"", "\"\""},
		{"a/b", "\"a/b\""},
		{"héllo", "\"héllo\""},
		{"q\"bs\\nl\nt\tr\rf\fb\b", "\"q\\\"bs\\\\nl\\nt\\tr\\rf\\fb\\b\""},
		{"\x01", "\"\\u0001\""},
		{Symbol("sym"), "\"sym\""},
		{"str", "\"str\""},
	}
	for _, c := range cases {
		if got := mustGen(t, c.v); got != c.want {
			t.Errorf("Generate(%#v) = %q, want %q", c.v, got, c.want)
		}
	}
}

// TestGenerateFloatFormatting locks the fpconv layout (fixed vs scientific
// thresholds, single- and multi-digit mantissas).
func TestGenerateFloatFormatting(t *testing.T) {
	cases := map[float64]string{
		1e14: "100000000000000.0", 1e15: "1e+15", 1e16: "1e+16",
		1e-9: "0.000000001", 1e-10: "1e-10",
		1.5e-9: "0.0000000015", 1.5e-10: "1.5e-10",
		1.5e15: "1.5e+15", 9e14: "900000000000000.0", 9e15: "9e+15",
		1234.5678: "1234.5678", 6.022e23: "6.022e+23",
		5e-324: "5e-324", 1.7976931348623157e308: "1.7976931348623157e+308",
		0.30000000000000004: "0.30000000000000004",
		1234567890123456.0:  "1.234567890123456e+15",
		100.0:               "100.0", -1.0: "-1.0", 10000000.0: "10000000.0",
	}
	for f, want := range cases {
		if got := mustGen(t, f); got != want {
			t.Errorf("Generate(%v) = %q, want %q", f, got, want)
		}
	}
}

// TestGenerateCollections covers arrays, objects, plain Go maps and key kinds.
func TestGenerateCollections(t *testing.T) {
	if got := mustGen(t, []any{}); got != "[]" {
		t.Errorf("empty array = %q", got)
	}
	if got := mustGen(t, []any{int64(1), 2.5, true, nil, "x"}); got != "[1,2.5,true,null,\"x\"]" {
		t.Errorf("array = %q", got)
	}
	m := NewMap()
	m.Set("a", int64(1))
	m.Set("b", []any{int64(1), int64(2)})
	if got := mustGen(t, m); got != "{\"a\":1,\"b\":[1,2]}" {
		t.Errorf("object = %q", got)
	}
	if got := mustGen(t, NewMap()); got != "{}" {
		t.Errorf("empty object = %q", got)
	}
	if got := mustGen(t, map[string]any{"b": int64(2), "a": int64(1)}); got != "{\"a\":1,\"b\":2}" {
		t.Errorf("string map = %q", got)
	}
	if got := mustGen(t, map[Symbol]any{"b": int64(2), "a": int64(1)}); got != "{\"a\":1,\"b\":2}" {
		t.Errorf("symbol map = %q", got)
	}
	mk := NewMap()
	mk.Set(Symbol("s"), int64(1))
	mk.Set(42, int64(2))
	mk.Set(int64(43), int64(3))
	bi, _ := new(big.Int).SetString("100000000000000000000000000000", 10)
	mk.Set(bi, int64(4))
	mk.Set(true, int64(5))
	mk.Set(false, int64(6))
	mk.Set(nil, int64(7))
	mk.Set(1.5, int64(8))
	want := "{\"s\":1,\"42\":2,\"43\":3,\"100000000000000000000000000000\":4,\"true\":5,\"false\":6,\"\":7,\"1.5\":8}"
	if got := mustGen(t, mk); got != want {
		t.Errorf("key kinds = %q\nwant %q", got, want)
	}
}

// TestGenerateNaN covers the non-finite float branches and allow_nan.
func TestGenerateNaN(t *testing.T) {
	for _, f := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := Generate(f); err == nil {
			t.Errorf("Generate(%v) should error", f)
		} else if _, ok := err.(*GeneratorError); !ok {
			t.Errorf("Generate(%v) err type = %T", f, err)
		}
	}
	cases := map[float64]string{math.NaN(): "NaN", math.Inf(1): "Infinity", math.Inf(-1): "-Infinity"}
	for f, want := range cases {
		if got := mustGen(t, f, WithAllowNaN(true)); got != want {
			t.Errorf("Generate(%v, allowNaN) = %q, want %q", f, got, want)
		}
	}
	msgs := map[float64]string{
		math.NaN(): "NaN not allowed in JSON", math.Inf(1): "Infinity not allowed in JSON",
		math.Inf(-1): "-Infinity not allowed in JSON",
	}
	for f, msg := range msgs {
		_, err := Generate(f)
		if err.Error() != msg {
			t.Errorf("Generate(%v) msg = %q, want %q", f, err.Error(), msg)
		}
	}
}

// TestGenerateNaNNested makes the non-finite error propagate up through array
// and object generation.
func TestGenerateNaNNested(t *testing.T) {
	if _, err := Generate([]any{math.NaN()}); err == nil {
		t.Error("nan in array should error")
	}
	m := NewMap()
	m.Set("k", math.Inf(1))
	if _, err := Generate(m); err == nil {
		t.Error("inf in object should error")
	}
}

// TestPrettyGenerate locks the MRI pretty layout, including empty collections.
func TestPrettyGenerate(t *testing.T) {
	m := NewMap()
	m.Set("a", int64(1))
	m.Set("b", []any{int64(1), int64(2)})
	c := NewMap()
	c.Set("d", int64(3))
	m.Set("c", c)
	want := "{\n  \"a\": 1,\n  \"b\": [\n    1,\n    2\n  ],\n  \"c\": {\n    \"d\": 3\n  }\n}"
	got, err := PrettyGenerate(m)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("pretty = %q\nwant %q", got, want)
	}
	if g, _ := PrettyGenerate([]any{}); g != "[]" {
		t.Errorf("pretty empty array = %q", g)
	}
	if g, _ := PrettyGenerate(NewMap()); g != "{}" {
		t.Errorf("pretty empty object = %q", g)
	}
	mx := NewMap()
	mx.Set("x", []any{})
	if g, _ := PrettyGenerate(mx); g != "{\n  \"x\": []\n}" {
		t.Errorf("pretty nested empty = %q", g)
	}
}

// TestGenerateStateOptions exercises the JSON::State knobs individually.
func TestGenerateStateOptions(t *testing.T) {
	g, _ := Generate([]any{int64(1), int64(2)}, WithIndent("\t"), WithArrayNL("\n"))
	if g != "[\n\t1,\n\t2\n]" {
		t.Errorf("indent/array_nl = %q", g)
	}
	m := NewMap()
	m.Set("a", int64(1))
	m.Set("b", int64(2))
	g, _ = Generate(m, WithSpaceBefore(" "), WithSpace(" "))
	if g != "{\"a\" : 1,\"b\" : 2}" {
		t.Errorf("space_before/space = %q", g)
	}
	g, _ = Generate(m, WithObjectNL("\n"), WithIndent("  "))
	if g != "{\n  \"a\":1,\n  \"b\":2\n}" {
		t.Errorf("object_nl = %q", g)
	}
}

// TestGenerateNesting covers the generate nesting limit and the unlimited case.
func TestGenerateNesting(t *testing.T) {
	build := func(d int) []any {
		var v any = []any{int64(1)}
		for i := 1; i < d; i++ {
			v = []any{v}
		}
		return v.([]any)
	}
	if _, err := Generate(build(2), WithMaxNesting(2)); err != nil {
		t.Errorf("depth 2, max 2 should pass: %v", err)
	}
	_, err := Generate(build(3), WithMaxNesting(2))
	if err == nil {
		t.Fatal("depth 3, max 2 should fail")
	}
	if _, ok := err.(*NestingError); !ok {
		t.Errorf("err type = %T", err)
	}
	if err.Error() != "nesting of 2 is too deep. Did you try to serialize objects with circular references?" {
		t.Errorf("msg = %q", err.Error())
	}
	mkObj := func(d int) Value {
		var v Value = int64(1)
		for i := 0; i < d; i++ {
			m := NewMap()
			m.Set("k", v)
			v = m
		}
		return v
	}
	if _, err := Generate(mkObj(3), WithMaxNesting(2)); err == nil {
		t.Error("object depth 3, max 2 should fail")
	}
	if _, err := Generate(build(200), WithMaxNesting(0)); err != nil {
		t.Errorf("unlimited generate: %v", err)
	}
	if _, err := Generate(build(100)); err != nil {
		t.Errorf("default depth 100: %v", err)
	}
	if _, err := Generate(build(101)); err == nil {
		t.Error("default depth 101 should fail")
	}
}

// TestGenerateFallback covers the out-of-model default branch.
func TestGenerateFallback(t *testing.T) {
	if got := mustGen(t, stringerVal{}); got != "\"S\"" {
		t.Errorf("stringer fallback = %q", got)
	}
	if got := mustGen(t, struct{ X int }{1}); got != "\"\"" {
		t.Errorf("non-stringer fallback = %q", got)
	}
	mk := NewMap()
	mk.Set(stringerVal{}, int64(1))
	if got := mustGen(t, mk); got != "{\"S\":1}" {
		t.Errorf("stringer key = %q", got)
	}
	mk2 := NewMap()
	mk2.Set(struct{ Y int }{2}, int64(1))
	if got := mustGen(t, mk2); got != "{\"\":1}" {
		t.Errorf("non-stringer key = %q", got)
	}
}

// stringerVal is an out-of-model value with a String method.
type stringerVal struct{}

func (stringerVal) String() string { return "S" }

// TestParseScalars covers each scalar production of the parser.
func TestParseScalars(t *testing.T) {
	if v := mustParse(t, "123"); v != int64(123) {
		t.Errorf("int = %#v", v)
	}
	if v := mustParse(t, "-0"); v != int64(0) {
		t.Errorf("neg zero = %#v", v)
	}
	if v := mustParse(t, "1.5"); v != 1.5 {
		t.Errorf("float = %#v", v)
	}
	if v := mustParse(t, "1e3"); v != 1000.0 {
		t.Errorf("exp = %#v", v)
	}
	if v := mustParse(t, "-1.5e-3"); v != -0.0015 {
		t.Errorf("negexp = %#v", v)
	}
	if v := mustParse(t, "1E2"); v != 100.0 {
		t.Errorf("upper E = %#v", v)
	}
	if v := mustParse(t, "12e+2"); v != 1200.0 {
		t.Errorf("plus exp = %#v", v)
	}
	if s := bigStr(mustParse(t, "100000000000000000000000000000")); s != "100000000000000000000000000000" {
		t.Errorf("bignum = %q", s)
	}
	if v := mustParse(t, "true"); v != true {
		t.Errorf("true = %#v", v)
	}
	if v := mustParse(t, "false"); v != false {
		t.Errorf("false = %#v", v)
	}
	if v := mustParse(t, "null"); v != nil {
		t.Errorf("null = %#v", v)
	}
	if v := mustParse(t, "  [ 1 , 2 ]  "); !reflect.DeepEqual(v, []any{int64(1), int64(2)}) {
		t.Errorf("ws = %#v", v)
	}
}

// TestParseStrings covers escapes, \u, and surrogate pairs.
func TestParseStrings(t *testing.T) {
	cases := []struct{ in, want string }{
		{"\"hi\\nthere\"", "hi\nthere"},
		{"\"\\\"\\\\\\/\\b\\f\\n\\r\\t\"", "\"\\/\b\f\n\r\t"},
		{"\"\\u0041\\u00e9\"", "Aé"},
		{"\"\\ud83d\\ude00\"", "\U0001F600"},
		{"\"a\\u0000b\"", "a\x00b"},
		{"\"AB\\u0041\"", "ABA"},
		{"\"raw \U0001F600\"", "raw \U0001F600"},
	}
	for _, c := range cases {
		if v := mustParse(t, c.in); v != c.want {
			t.Errorf("Parse(%q) = %q, want %q", c.in, v, c.want)
		}
	}
	if v := mustParse(t, "\"\\udc00\""); !reflect.DeepEqual([]byte(v.(string)), []byte{0xED, 0xB0, 0x80}) {
		t.Errorf("lone low surrogate = % x", []byte(v.(string)))
	}
}

// TestParseCollections covers arrays, nested objects and key order.
func TestParseCollections(t *testing.T) {
	v := mustParse(t, "{\"a\":1,\"b\":[1,2.5,true,null,\"x\"],\"c\":{\"d\":3}}")
	m, ok := v.(*Map)
	if !ok {
		t.Fatalf("not a Map: %#v", v)
	}
	keys := []string{m.pairs[0].Key.(string), m.pairs[1].Key.(string), m.pairs[2].Key.(string)}
	if !reflect.DeepEqual(keys, []string{"a", "b", "c"}) {
		t.Errorf("key order = %v", keys)
	}
	if bv, _ := m.Get("b"); !reflect.DeepEqual(bv, []any{int64(1), 2.5, true, nil, "x"}) {
		t.Errorf("b = %#v", bv)
	}
	if v := mustParse(t, "[]"); !reflect.DeepEqual(v, []any{}) {
		t.Errorf("empty array = %#v", v)
	}
	if v := mustParse(t, "{}"); v.(*Map).Len() != 0 {
		t.Errorf("empty object len != 0")
	}
	dv := mustParse(t, "{\"a\":1,\"a\":2}").(*Map)
	if dv.Len() != 1 {
		t.Errorf("dup len = %d", dv.Len())
	}
	if av, _ := dv.Get("a"); av != int64(2) {
		t.Errorf("dup a = %#v", av)
	}
}

// TestParseSymbolize covers symbolize_names.
func TestParseSymbolize(t *testing.T) {
	v := mustParse(t, "{\"a\":{\"b\":1}}", WithSymbolizeNames(true)).(*Map)
	if _, ok := v.Get(Symbol("a")); !ok {
		t.Errorf("top key not symbolized: %#v", v.pairs)
	}
	inner, _ := v.Get(Symbol("a"))
	if _, ok := inner.(*Map).Get(Symbol("b")); !ok {
		t.Errorf("inner key not symbolized")
	}
}

// TestParseAllowNaN covers the NaN/Infinity tokens under allow_nan.
func TestParseAllowNaN(t *testing.T) {
	v := mustParse(t, "[NaN, Infinity, -Infinity]", WithAllowNaN(true)).([]any)
	if !math.IsNaN(v[0].(float64)) || !math.IsInf(v[1].(float64), 1) || !math.IsInf(v[2].(float64), -1) {
		t.Errorf("allow_nan parse = %#v", v)
	}
	for _, in := range []string{"NaN", "Infinity", "-Infinity"} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) should error without allow_nan", in)
		}
	}
}

// TestParseNesting covers the parse nesting limit (default, custom, unlimited).
func TestParseNesting(t *testing.T) {
	deepArr := func(n int) string {
		s := ""
		for i := 0; i < n; i++ {
			s = "[" + s + "]"
		}
		return s
	}
	if _, err := Parse(deepArr(100)); err != nil {
		t.Errorf("depth 100 default: %v", err)
	}
	_, err := Parse(deepArr(101))
	if err == nil {
		t.Fatal("depth 101 should fail")
	}
	ne, ok := err.(*NestingError)
	if !ok || ne.Error() != "nesting of 101 is too deep" {
		t.Errorf("err = %v (%T)", err, err)
	}
	objDeep := func(n int) string {
		s := "1"
		for i := 0; i < n; i++ {
			s = "{\"k\":" + s + "}"
		}
		return s
	}
	if _, err := Parse(objDeep(3), WithMaxNesting(2)); err == nil {
		t.Error("object depth 3, max 2 should fail")
	}
	if _, err := Parse("[[[1]]]", WithMaxNesting(2)); err == nil {
		t.Error("[[[1]]] max 2 should fail")
	}
	if _, err := Parse(deepArr(300), WithMaxNesting(0)); err != nil {
		t.Errorf("unlimited: %v", err)
	}
}

// TestParseErrors locks every MRI ParserError message.
func TestParseErrors(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "unexpected end of input at line 1 column 1"},
		{"   ", "unexpected end of input at line 1 column 4"},
		{"{", "expected object key, got EOF at line 1 column 2"},
		{"[", "unexpected end of input at line 1 column 2"},
		{"{\"a\"}", "expected ':' after object key at line 1 column 5"},
		{"{\"a\":}", "unexpected character: '}' at line 1 column 6"},
		{"[1,]", "unexpected character: ']' at line 1 column 4"},
		{"{,}", "expected object key, got ',}' at line 1 column 2"},
		{"nul", "unexpected token 'nul' at line 1 column 1"},
		{"tru", "unexpected token 'tru' at line 1 column 1"},
		{"123abc", "unexpected token at end of stream 'abc' at line 1 column 4"},
		{"{\"a\":1} trailing", "unexpected token at end of stream 'trailing' at line 1 column 9"},
		{"\"unterminated", "unexpected end of input, expected closing \" at line 1 column 14"},
		{"[1 2]", "expected ',' or ']' after array value at line 1 column 4"},
		{"{\"a\":1,}", "expected object key, got: '}' at line 1 column 8"},
		{"01", "invalid number: '01' at line 1 column 1"},
		{"+1", "unexpected character: '+1' at line 1 column 1"},
		{".5", "unexpected character: '.5' at line 1 column 1"},
		{"1.", "invalid number: '1.' at line 1 column 1"},
		{"1e", "invalid number: '1e' at line 1 column 1"},
		{"'single'", "unexpected character: ''single'' at line 1 column 1"},
		{"NaN", "unexpected token 'NaN' at line 1 column 1"},
		{"Infinity", "unexpected token 'Infinity' at line 1 column 1"},
		{"}", "unexpected character: '}' at line 1 column 1"},
		{"]", "unexpected character: ']' at line 1 column 1"},
		{"\"a\\qb\"", "invalid escape character in string: '\\qb\"' at line 1 column 3"},
		{"\"\\u12\"", "incomplete unicode character escape sequence at '\\u12\"' at line 1 column 2"},
		{"\"\\ud83d\"", "incomplete surrogate pair at '\\ud83d\"' at line 1 column 2"},
		{"\"\\ud83dx\"", "incomplete surrogate pair at '\\ud83dx\"' at line 1 column 2"},
		{"\"\\ud83dA\"", "incomplete surrogate pair at '\\ud83dA\"' at line 1 column 2"},
		{"\"\\ud83d\\u0041\"", "invalid surrogate pair at '\\ud83d\\u0041\"' at line 1 column 2"},
		{"-", "invalid number: '-' at line 1 column 1"},
		{"\"\\u12g4\"", "incomplete unicode character escape sequence at '\\u12g4\"' at line 1 column 2"},
	}
	for _, c := range cases {
		_, err := Parse(c.in)
		if err == nil {
			t.Errorf("Parse(%q) want error %q, got nil", c.in, c.want)
			continue
		}
		if err.Error() != c.want {
			t.Errorf("Parse(%q) = %q, want %q", c.in, err.Error(), c.want)
		}
		if _, ok := err.(Error); !ok {
			t.Errorf("Parse(%q) err is not Error: %T", c.in, err)
		}
	}
}

// TestParseControlCharError covers the raw-control-character branch.
func TestParseControlCharError(t *testing.T) {
	_, err := Parse("\"X\tY\"")
	want := "invalid ASCII control character in string: \tY\" at line 1 column 3"
	if err == nil || err.Error() != want {
		t.Errorf("control char err = %v, want %q", err, want)
	}
}

// TestParseIncompleteEscapeAtEOF covers a backslash as the final byte and an
// unfinished \u escape at EOF.
func TestParseIncompleteEscapeAtEOF(t *testing.T) {
	for _, in := range []string{"\"a\\", "\"\\u00"} {
		_, err := Parse(in)
		if err == nil {
			t.Fatalf("%q should error", in)
		}
		if _, ok := err.(*ParserError); !ok {
			t.Errorf("%q err type = %T", in, err)
		}
	}
}

// TestParseMultilineColumns covers the line/column counting across newlines.
func TestParseMultilineColumns(t *testing.T) {
	_, err := Parse("[\n  1,\n  bad\n]")
	want := "unexpected token 'bad' at line 3 column 3"
	if err == nil || err.Error() != want {
		t.Errorf("multiline err = %v, want %q", err, want)
	}
}

// TestParseUnterminatedArrayObject covers EOF inside an array/object after a
// complete value.
func TestParseUnterminatedArrayObject(t *testing.T) {
	for _, in := range []string{"[1", "{\"a\":1"} {
		_, err := Parse(in)
		if err == nil || err.Error() != "unexpected end of input at line 1 column "+itoa(len(in)+1) {
			t.Errorf("Parse(%q) = %v", in, err)
		}
	}
}

func itoa(n int) string { return new(big.Int).SetInt64(int64(n)).String() }

// TestParseTypeError covers the TypeError host type and the byte entry point.
func TestParseTypeError(t *testing.T) {
	te := &TypeError{Message: "x"}
	if te.RubyClass() != "TypeError" || te.Error() != "x" {
		t.Errorf("TypeError = %q / %q", te.RubyClass(), te.Error())
	}
	if v := mustParseBytes(t, []byte("[1,2]")); !reflect.DeepEqual(v, []any{int64(1), int64(2)}) {
		t.Errorf("ParseBytes = %#v", v)
	}
}

func mustParseBytes(t *testing.T, b []byte) Value {
	t.Helper()
	v, err := ParseBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestErrorTaxonomy covers RubyClass on every error type.
func TestErrorTaxonomy(t *testing.T) {
	errs := []struct {
		e    Error
		want string
	}{
		{&ParserError{"p"}, "JSON::ParserError"},
		{&NestingError{"n"}, "JSON::NestingError"},
		{&GeneratorError{"g"}, "JSON::GeneratorError"},
		{&TypeError{"t"}, "TypeError"},
	}
	for _, c := range errs {
		if c.e.RubyClass() != c.want {
			t.Errorf("RubyClass = %q, want %q", c.e.RubyClass(), c.want)
		}
		if c.e.Error() == "" {
			t.Errorf("empty Error() for %s", c.want)
		}
	}
}

// TestMapAPI covers the public Map accessors and value helpers.
func TestMapAPI(t *testing.T) {
	m := NewMap()
	m.Set("a", int64(1))
	m.Set("a", int64(2))
	if m.Len() != 1 {
		t.Errorf("len = %d", m.Len())
	}
	if v, ok := m.Get("a"); !ok || v != int64(2) {
		t.Errorf("get a = %#v, %v", v, ok)
	}
	if _, ok := m.Get("missing"); ok {
		t.Error("missing key found")
	}
	if len(m.Pairs()) != 1 {
		t.Errorf("pairs = %d", len(m.Pairs()))
	}
	var z Map
	z.Set("k", int64(9))
	if v, _ := z.Get("k"); v != int64(9) {
		t.Errorf("zero-map get = %#v", v)
	}
	// A Map that grows past mapIndexThreshold builds and then uses its hash
	// index; exercise inserting past the threshold, a hit/miss Get through the
	// index, and a duplicate-key last-wins update once the index is live.
	var lm Map
	n := mapIndexThreshold + 5
	for i := 0; i < n; i++ {
		lm.Set("k"+strconv.Itoa(i), int64(i))
	}
	if lm.index == nil {
		t.Fatal("large Map should have built its hash index")
	}
	if lm.Len() != n {
		t.Errorf("large map len = %d, want %d", lm.Len(), n)
	}
	if v, ok := lm.Get("k0"); !ok || v != int64(0) {
		t.Errorf("indexed get k0 = %#v, %v", v, ok)
	}
	if v, ok := lm.Get("k" + strconv.Itoa(n-1)); !ok || v != int64(n-1) {
		t.Errorf("indexed get last = %#v, %v", v, ok)
	}
	if _, ok := lm.Get("absent"); ok {
		t.Error("indexed get of absent key found")
	}
	lm.Set("k0", int64(999)) // duplicate update through the live index
	if v, _ := lm.Get("k0"); v != int64(999) {
		t.Errorf("indexed dup update = %#v", v)
	}
	if lm.Len() != n {
		t.Errorf("len after dup update = %d, want %d", lm.Len(), n)
	}
	if asBigInt(int(5)).Int64() != 5 || asBigInt(int64(6)).Int64() != 6 {
		t.Error("asBigInt int/int64")
	}
	bi := big.NewInt(7)
	if asBigInt(bi) != bi {
		t.Error("asBigInt big")
	}
	if asBigInt("nope") != nil {
		t.Error("asBigInt non-int should be nil")
	}
}

// TestParseObjectKeyError covers a bad escape inside an object key string and
// junk after a complete pair (the two uncovered object branches).
func TestParseObjectKeyError(t *testing.T) {
	if _, err := Parse("{\"a\\q\":1}"); err == nil {
		t.Error("bad key escape should error")
	}
	_, err := Parse("{\"a\":1 2}")
	want := "expected ',' or '}' after object value, got: '2}' at line 1 column 8"
	if err == nil || err.Error() != want {
		t.Errorf("junk after pair = %v, want %q", err, want)
	}
}

// TestParseLowSurrogateError covers an incomplete low surrogate after a valid
// high surrogate: MRI reports "incomplete surrogate pair", not a bare unicode
// escape error.
func TestParseLowSurrogateError(t *testing.T) {
	_, err := Parse("\"\\ud83d\\u12\"")
	want := "incomplete surrogate pair at '\\ud83d\\u12\"' at line 1 column 2"
	if err == nil || err.Error() != want {
		t.Errorf("incomplete low surrogate = %v, want %q", err, want)
	}
}

// TestParseHexUpper covers uppercase hex digits in a \u escape.
func TestParseHexUpper(t *testing.T) {
	if v := mustParse(t, "\"\\u00AB\""); v != "«" {
		t.Errorf("uppercase hex = %q", v)
	}
}

// TestParseFloatOverflow covers a float literal that overflows to Infinity,
// matching MRI (1e400 -> Infinity, 1e-400 -> 0.0).
func TestParseFloatOverflow(t *testing.T) {
	if v := mustParse(t, "1e400"); !mathIsInf(v.(float64), 1) {
		t.Errorf("1e400 = %v", v)
	}
	if v := mustParse(t, "-1e400"); !mathIsInf(v.(float64), -1) {
		t.Errorf("-1e400 = %v", v)
	}
	if v := mustParse(t, "1e-400"); v.(float64) != 0 {
		t.Errorf("1e-400 = %v", v)
	}
}

func mathIsInf(f float64, sign int) bool { return math.IsInf(f, sign) }
