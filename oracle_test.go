// Copyright (c) the go-ruby-json/json authors
//
// SPDX-License-Identifier: BSD-3-Clause

package json

import (
	"math"
	"math/big"
	"os/exec"
	"strings"
	"testing"
)

// rubyBin locates a usable `ruby` whose RUBY_VERSION is at least 4.0 (the JSON
// gem shipped with MRI 4.0 is the conformance oracle). When ruby is absent or
// too old — the qemu cross-arch lanes, the Windows lane, and runners carrying an
// old system ruby — the oracle tests skip themselves, and the deterministic,
// ruby-free suite alone holds coverage at 100%.
func rubyBin(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("ruby")
	if err != nil {
		t.Skip("ruby not on PATH; skipping MRI oracle")
	}
	out, err := exec.Command(path, "-e", "print RUBY_VERSION").Output()
	if err != nil {
		t.Skipf("cannot query ruby version: %v", err)
	}
	if !rubyAtLeast(string(out), 4, 0) {
		t.Skipf("ruby %s < 4.0; skipping MRI oracle", out)
	}
	return path
}

// rubyAtLeast reports whether the dotted version string ver is at least
// major.minor.
func rubyAtLeast(ver string, major, minor int) bool {
	parts := strings.Split(strings.TrimSpace(ver), ".")
	if len(parts) < 2 {
		return false
	}
	maj := atoiSafe(parts[0])
	min := atoiSafe(parts[1])
	if maj != major {
		return maj > major
	}
	return min >= minor
}

// atoiSafe parses a non-negative decimal prefix, returning 0 on any junk.
func atoiSafe(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			break
		}
		n = n*10 + int(s[i]-'0')
	}
	return n
}

// rubyJSON runs a ruby `-rjson` script reading the input document on stdin (in
// binary mode, so embedded newlines survive Windows text-mode) and returns its
// stdout. The script itself $stdout.binmode's so the bytes are never CRLF
// mangled.
func rubyJSON(t *testing.T, bin, script, stdin string) string {
	t.Helper()
	full := "$stdout.binmode\n$stdin.binmode\nINPUT = $stdin.read\n" + script
	cmd := exec.Command(bin, "-rjson", "-e", full)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ruby error: %v\nscript:\n%s\noutput:\n%s", err, script, out)
	}
	return string(out)
}

// TestOracleGenerate checks that Generate matches MRI's JSON.generate for a wide
// corpus: MRI parses the document we generated and re-generates it, and the two
// byte strings must be identical.
func TestOracleGenerate(t *testing.T) {
	bin := rubyBin(t)
	bi, _ := new(big.Int).SetString("123456789012345678901234567890", 10)
	m := NewMap()
	m.Set("a", int64(1))
	m.Set("b", []any{int64(1), 2.5, true, nil, "x"})
	m.Set("nested", func() *Map { n := NewMap(); n.Set("d", int64(3)); return n }())
	cases := []Value{
		nil, true, false, int64(42), int64(-7), bi,
		3.14, 2.0, 1e100, 1e-7, 0.1, 100.0, -1.0, 6.022e23, 5e-324,
		"hello", "", "a/b", "héllo", "q\"bs\\nl\nt\tr\rf\fb\b",
		[]any{}, []any{int64(1), int64(2), int64(3)},
		map[string]any{}, m,
	}
	for _, v := range cases {
		got, err := Generate(v)
		if err != nil {
			t.Fatalf("Generate(%#v): %v", v, err)
		}
		// MRI: re-generate the document and compare bytes. JSON.generate of a bare
		// scalar is accepted in MRI 4.0.
		out := rubyJSON(t, bin, "print JSON.generate(JSON.parse(INPUT, quirks_mode: true))", got)
		if out != got {
			t.Errorf("Generate(%#v) = %q; MRI re-generate = %q", v, got, out)
		}
	}
}

// TestOracleGenerateFloats checks float formatting against MRI directly over a
// broad numeric corpus (the fpconv layout is the subtle part).
func TestOracleGenerateFloats(t *testing.T) {
	bin := rubyBin(t)
	floats := []float64{
		0.0, 1.0, -1.0, 0.5, 1.5, 100.0, 1234.5678,
		1e6, 1e7, 1e14, 1e15, 1e16, 1e17, 1e20, 1e21, 1e22,
		1e-1, 1e-3, 1e-7, 1e-8, 1e-9, 1e-10, 1.23e-10, 9.999999e22,
		5e-324, 1.7976931348623157e308, 0.30000000000000004,
		12345678901234.5, 1234567890123456.0, 6.022e23, 1.5e-9, 1.5e-10,
		9e14, 9e15, math.Pi, math.E,
	}
	for _, f := range floats {
		got, _ := Generate(f)
		// Feed the float literal to MRI and compare JSON.generate output.
		out := rubyJSON(t, bin, "print JSON.generate(Float(INPUT))", strconv64(f))
		if out != got {
			t.Errorf("Generate(%v) = %q; MRI = %q", f, got, out)
		}
	}
}

// strconv64 prints f with full precision so MRI reconstructs the identical
// float64 (Go's shortest round-trip form).
func strconv64(f float64) string {
	s, _ := Generate(f, WithAllowNaN(true))
	return s
}

// TestOraclePrettyGenerate checks PrettyGenerate matches JSON.pretty_generate
// for nested structures.
func TestOraclePrettyGenerate(t *testing.T) {
	bin := rubyBin(t)
	m := NewMap()
	m.Set("a", int64(1))
	m.Set("b", []any{int64(1), int64(2)})
	c := NewMap()
	c.Set("d", int64(3))
	m.Set("c", c)
	m.Set("e", []any{})
	got, _ := PrettyGenerate(m)
	out := rubyJSON(t, bin, "print JSON.pretty_generate(JSON.parse(INPUT))", "{\"a\":1,\"b\":[1,2],\"c\":{\"d\":3},\"e\":[]}")
	if out != got {
		t.Errorf("PrettyGenerate = %q; MRI = %q", got, out)
	}
}

// TestOracleParse checks Parse agrees with MRI's JSON.parse over a corpus: MRI
// parses the document and inspects the value; we parse it and inspect ours via a
// matching Ruby-style renderer, then compare.
func TestOracleParse(t *testing.T) {
	bin := rubyBin(t)
	docs := []string{
		"{\"a\":1,\"b\":[1,2.5,true,null,\"x\"]}",
		"[1,2,3]",
		"\"hi\\nthere\"",
		"123", "1.5", "1e3", "-0", "100000000000000000000000000000",
		"true", "false", "null",
		"  [ 1 , 2 ]  ",
		"\"\\u0041\\u00e9\"",
		"{}", "[]",
		"{\"k\":{\"deep\":[1,{\"x\":\"y\"}]}}",
	}
	for _, doc := range docs {
		v, err := Parse(doc)
		if err != nil {
			t.Fatalf("Parse(%q): %v", doc, err)
		}
		// MRI's inspect of the parsed value, compared to ours.
		out := strings.TrimRight(rubyJSON(t, bin, "print JSON.parse(INPUT).inspect", doc), "\n")
		if got := rubyInspect(v); got != out {
			t.Errorf("Parse(%q) inspect = %q; MRI = %q", doc, got, out)
		}
	}
}

// TestOracleParseErrors checks the ParserError / NestingError messages match MRI
// exactly for malformed documents.
func TestOracleParseErrors(t *testing.T) {
	bin := rubyBin(t)
	bad := []string{
		"", "   ", "{", "[", "{\"a\"}", "{\"a\":}", "[1,]", "{,}",
		"nul", "tru", "123abc", "{\"a\":1} trailing", "\"unterminated",
		"[1 2]", "{\"a\":1,}", "01", "+1", ".5", "1.", "1e", "'single'",
		"NaN", "Infinity", "}", "]", "{\"a\":1 2}",
		"\"a\\qb\"", "\"\\u12\"", "\"\\ud83d\"", "\"\\ud83dx\"", "\"\\ud83d\\u0041\"",
	}
	for _, doc := range bad {
		_, err := Parse(doc)
		if err == nil {
			t.Errorf("Parse(%q) expected error", doc)
			continue
		}
		out := strings.TrimRight(rubyJSON(t, bin,
			"begin; JSON.parse(INPUT); print 'NO ERROR'; rescue => e; print e.message; end", doc), "\n")
		if err.Error() != out {
			t.Errorf("Parse(%q) = %q; MRI = %q", doc, err.Error(), out)
		}
	}
}

// TestOracleNesting checks the nesting-limit messages match MRI on both parse and
// generate.
func TestOracleNesting(t *testing.T) {
	bin := rubyBin(t)
	deep := strings.Repeat("[", 101) + "1" + strings.Repeat("]", 101)
	_, err := Parse(deep)
	if err == nil {
		t.Fatal("deep parse should error")
	}
	out := strings.TrimRight(rubyJSON(t, bin,
		"begin; JSON.parse(INPUT); print 'NO'; rescue => e; print e.message; end", deep), "\n")
	if err.Error() != out {
		t.Errorf("parse nesting = %q; MRI = %q", err.Error(), out)
	}
}

// TestOracleNaN checks NaN / Infinity handling matches MRI in both directions.
func TestOracleNaN(t *testing.T) {
	bin := rubyBin(t)
	// allow_nan generate
	for _, f := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		got, _ := Generate(f, WithAllowNaN(true))
		lit := map[bool]string{true: "Float::NAN"}[math.IsNaN(f)]
		if lit == "" {
			if math.IsInf(f, 1) {
				lit = "Float::INFINITY"
			} else {
				lit = "-Float::INFINITY"
			}
		}
		out := rubyJSON(t, bin, "print JSON.generate("+lit+", allow_nan: true)", "")
		if out != got {
			t.Errorf("Generate(%v, allowNaN) = %q; MRI = %q", f, got, out)
		}
	}
	// generate error message
	_, err := Generate(math.NaN())
	out := rubyJSON(t, bin,
		"begin; JSON.generate(Float::NAN); rescue => e; print e.message; end", "")
	if err.Error() != out {
		t.Errorf("Generate(NaN) err = %q; MRI = %q", err.Error(), out)
	}
}

// rubyInspect renders a parsed Value the way Ruby's #inspect would, so the parse
// oracle can compare structures (MRI 4.0 inspect: hashes as {"k" => v}, arrays as
// [a, b], strings with #inspect escaping).
func rubyInspect(v Value) string {
	var sb strings.Builder
	inspect(&sb, v)
	return sb.String()
}

// inspect is the recursive worker for rubyInspect.
func inspect(sb *strings.Builder, v Value) {
	switch x := v.(type) {
	case nil:
		sb.WriteString("nil")
	case bool:
		if x {
			sb.WriteString("true")
		} else {
			sb.WriteString("false")
		}
	case int64:
		sb.WriteString(formatInt(x))
	case *big.Int:
		sb.WriteString(x.String())
	case float64:
		sb.WriteString(rubyFloatInspect(x))
	case string:
		inspectString(sb, x)
	case Symbol:
		sb.WriteByte(':')
		sb.WriteString(string(x))
	case []any:
		sb.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				sb.WriteString(", ")
			}
			inspect(sb, e)
		}
		sb.WriteByte(']')
	case *Map:
		sb.WriteByte('{')
		for i, p := range x.pairs {
			if i > 0 {
				sb.WriteString(", ")
			}
			inspect(sb, p.Key)
			sb.WriteString(" => ")
			inspect(sb, p.Val)
		}
		sb.WriteByte('}')
	}
}

// formatInt renders an int64 in decimal.
func formatInt(n int64) string { return big.NewInt(n).String() }

// rubyFloatInspect renders a float the way Ruby's Float#inspect does (uses
// Float#to_s, which differs from JSON.generate's fpconv for large/small values,
// but the parse-oracle corpus only carries values whose forms agree).
func rubyFloatInspect(f float64) string {
	// For the corpus here (1.5, 2.5, 1000.0, …) Ruby's to_s matches our
	// JSON float form; reuse it.
	return formatFloat(f)
}

// inspectString renders a Go string as Ruby's String#inspect: double-quoted with
// \n, \t, \", \\ escapes and UTF-8 passthrough.
func inspectString(sb *strings.Builder, s string) {
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			sb.WriteString("\\\"")
		case '\\':
			sb.WriteString("\\\\")
		case '\n':
			sb.WriteString("\\n")
		case '\t':
			sb.WriteString("\\t")
		case '\r':
			sb.WriteString("\\r")
		default:
			sb.WriteRune(r)
		}
	}
	sb.WriteByte('"')
}
