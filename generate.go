// Copyright (c) the go-ruby-json/json authors
//
// SPDX-License-Identifier: BSD-3-Clause

package json

import (
	"math"
	"math/big"
	"strconv"
	"strings"
	"sync"
)

// genPool recycles the scratch byte buffer each generation grows into, so a
// stream of Generate calls (the common server/log workload) reuses one backing
// array instead of allocating a fresh strings.Builder per call.
var genPool = sync.Pool{New: func() any { b := make([]byte, 0, 256); return &b }}

// generate renders v per config c. It tracks nesting depth so an over-deep tree
// (including a cycle through a *Map / []any) raises a NestingError exactly like
// MRI.
func generate(v Value, c *config) (string, error) {
	bp := genPool.Get().(*[]byte)
	g := &gen{c: c, buf: (*bp)[:0]}
	if err := g.value(v, 0); err != nil {
		*bp = g.buf
		genPool.Put(bp)
		return "", err
	}
	out := string(g.buf)
	*bp = g.buf
	genPool.Put(bp)
	return out, nil
}

// gen holds the streaming generation state: a scratch byte buffer appended to
// directly (cheaper than a strings.Builder for byte-level writes and poolable).
type gen struct {
	c   *config
	buf []byte
}

// nestingLimit reports the active generate nesting limit, or -1 for unlimited.
func (g *gen) nestingLimit() int { return nestingLimit(g.c) }

// writeStr appends a literal string to the buffer.
func (g *gen) writeStr(s string) { g.buf = append(g.buf, s...) }

// writeByte appends one byte to the buffer.
func (g *gen) writeByte(b byte) { g.buf = append(g.buf, b) }

// writeInt appends the base-10 form of n without an intermediate string.
func (g *gen) writeInt(n int64) { g.buf = strconv.AppendInt(g.buf, n, 10) }

// value writes one value at the given structure depth.
func (g *gen) value(v Value, depth int) error {
	switch x := v.(type) {
	case nil:
		g.writeStr("null")
	case bool:
		if x {
			g.writeStr("true")
		} else {
			g.writeStr("false")
		}
	case int:
		g.writeInt(int64(x))
	case int64:
		g.writeInt(x)
	case *big.Int:
		g.writeStr(x.String())
	case float32:
		return g.float(float64(x))
	case float64:
		return g.float(x)
	case string:
		g.writeString(x)
	case Symbol:
		g.writeString(string(x))
	case []any:
		return g.array(x, depth)
	case *Map:
		return g.object(x, depth)
	case map[string]any:
		m := NewMap()
		for _, k := range sortedStringKeys(x) {
			m.Set(k, x[k])
		}
		return g.object(m, depth)
	case map[Symbol]any:
		m := NewMap()
		for _, k := range sortedSymbolKeys(x) {
			m.Set(k, x[k])
		}
		return g.object(m, depth)
	default:
		// Anything outside the model is rendered by its Go string form quoted, a
		// best-effort fallback mirroring MRI's to_s-of-unknown.
		g.writeString(toS(v))
	}
	return nil
}

// toS renders an out-of-model value as a JSON string body.
func toS(v Value) string {
	if s, ok := v.(interface{ String() string }); ok {
		return s.String()
	}
	return ""
}

// float writes f, raising on a non-finite value unless allow_nan is set, and
// formatting finite floats with MRI's json-gem (fpconv) layout.
func (g *gen) float(f float64) error {
	switch {
	case math.IsNaN(f):
		if !g.c.allowNaN {
			return &GeneratorError{Message: "NaN not allowed in JSON"}
		}
		g.writeStr("NaN")
	case math.IsInf(f, 1):
		if !g.c.allowNaN {
			return &GeneratorError{Message: "Infinity not allowed in JSON"}
		}
		g.writeStr("Infinity")
	case math.IsInf(f, -1):
		if !g.c.allowNaN {
			return &GeneratorError{Message: "-Infinity not allowed in JSON"}
		}
		g.writeStr("-Infinity")
	default:
		g.writeStr(formatFloat(f))
	}
	return nil
}

// nan returns a NaN float64 (used by allow_nan parsing).
func nan() float64 { return math.NaN() }

// inf returns +Inf (sign>=0) or -Inf (used by allow_nan parsing).
func inf(sign int) float64 { return math.Inf(sign) }

// formatFloat renders f exactly as MRI's json gem (fpconv_dtoa) does: the
// shortest round-tripping decimal, in fixed notation when the decimal point sits
// in (-9, 15] and in "<mant>e±NN" scientific notation otherwise.
func formatFloat(f float64) string {
	if f == 0 {
		if math.Signbit(f) {
			return "-0.0"
		}
		return "0.0"
	}
	neg := math.Signbit(f)
	abs := math.Abs(f)

	// strconv 'e' with prec -1 yields the shortest digits and a base-10 exponent:
	// d.dddde±XX. Extract the bare digit string and the exponent of the leading
	// digit.
	s := strconv.FormatFloat(abs, 'e', -1, 64)
	mant, expPart, _ := strings.Cut(s, "e")
	exp, _ := strconv.Atoi(expPart)
	digits := strings.Replace(mant, ".", "", 1) // e.g. "1234"
	// decpt is the number of digits to the left of the decimal point if written in
	// fixed notation: value = 0.<digits> * 10^decpt.
	decpt := exp + 1

	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	// MRI's fpconv: fixed notation for -9 < decpt <= 15, else scientific.
	if decpt > -9 && decpt <= 15 {
		emitFixed(&b, digits, decpt)
	} else {
		emitScientific(&b, digits, decpt)
	}
	return b.String()
}

// emitFixed writes digits in fixed-point notation with decpt integer digits,
// always including a fractional part (MRI always shows a decimal point, e.g.
// "2.0").
func emitFixed(b *strings.Builder, digits string, decpt int) {
	n := len(digits)
	switch {
	case decpt <= 0:
		// 0.00…digits
		b.WriteString("0.")
		for i := 0; i < -decpt; i++ {
			b.WriteByte('0')
		}
		b.WriteString(digits)
	case decpt >= n:
		// digits followed by zeros, then ".0"
		b.WriteString(digits)
		for i := 0; i < decpt-n; i++ {
			b.WriteByte('0')
		}
		b.WriteString(".0")
	default:
		b.WriteString(digits[:decpt])
		b.WriteByte('.')
		b.WriteString(digits[decpt:])
	}
}

// emitScientific writes digits as <m>e±NN where m is the single leading digit
// optionally followed by ".rest", and NN is decpt-1 (the exponent of the leading
// digit), with a sign — MRI's fpconv layout (e.g. "1e+15", "1.5e-10", "5e-324").
// Scientific notation is only chosen when |exponent| >= 10, so the exponent is
// always at least two digits without padding.
func emitScientific(b *strings.Builder, digits string, decpt int) {
	b.WriteByte(digits[0])
	if len(digits) > 1 {
		b.WriteByte('.')
		b.WriteString(digits[1:])
	}
	b.WriteByte('e')
	e := decpt - 1
	if e < 0 {
		b.WriteByte('-')
		e = -e
	} else {
		b.WriteByte('+')
	}
	b.WriteString(strconv.Itoa(e))
}

// array writes a JSON array, honouring array_nl / indent.
func (g *gen) array(a []any, depth int) error {
	if lim := g.nestingLimit(); lim >= 0 && depth+1 > lim {
		return genNestingErr(lim)
	}
	if len(a) == 0 {
		g.writeStr("[]")
		return nil
	}
	g.writeByte('[')
	g.writeStr(g.c.arrayNL)
	for i, e := range a {
		if i > 0 {
			g.writeByte(',')
			g.writeStr(g.c.arrayNL)
		}
		g.indent(depth + 1)
		if err := g.value(e, depth+1); err != nil {
			return err
		}
	}
	g.writeStr(g.c.arrayNL)
	g.indent(depth)
	g.writeByte(']')
	return nil
}

// object writes a JSON object, honouring object_nl / space / space_before /
// indent and rendering keys as their string/symbol name (else their to_s).
func (g *gen) object(m *Map, depth int) error {
	if lim := g.nestingLimit(); lim >= 0 && depth+1 > lim {
		return genNestingErr(lim)
	}
	if m.Len() == 0 {
		g.writeStr("{}")
		return nil
	}
	g.writeByte('{')
	g.writeStr(g.c.objectNL)
	for i, p := range m.pairs {
		if i > 0 {
			g.writeByte(',')
			g.writeStr(g.c.objectNL)
		}
		g.indent(depth + 1)
		g.writeString(keyString(p.Key))
		g.writeStr(g.c.spaceB)
		g.writeByte(':')
		g.writeStr(g.c.space)
		if err := g.value(p.Val, depth+1); err != nil {
			return err
		}
	}
	g.writeStr(g.c.objectNL)
	g.indent(depth)
	g.writeByte('}')
	return nil
}

// indent writes the indent string depth times (no-op when empty, i.e. compact).
func (g *gen) indent(depth int) {
	if g.c.indent == "" {
		return
	}
	for i := 0; i < depth; i++ {
		g.writeStr(g.c.indent)
	}
}

// keyString renders a hash key as the string JSON needs: a string/symbol by its
// text, anything else by its Go/Ruby string form.
func keyString(k Value) string {
	switch x := k.(type) {
	case string:
		return x
	case Symbol:
		return string(x)
	case int:
		return strconv.FormatInt(int64(x), 10)
	case int64:
		return strconv.FormatInt(x, 10)
	case *big.Int:
		return x.String()
	case bool:
		if x {
			return "true"
		}
		return "false"
	case nil:
		return ""
	case float64:
		return formatFloat(x)
	default:
		return toS(k)
	}
}

// genNestingErr builds the MRI generate NestingError, whose message reports the
// limit value and the circular-reference hint.
func genNestingErr(limit int) error {
	return &NestingError{Message: "nesting of " + strconv.Itoa(limit) +
		" is too deep. Did you try to serialize objects with circular references?"}
}

// hexLower indexes lowercase hex digits for \uXXXX escapes.
const hexLower = "0123456789abcdef"

// writeString writes s as a JSON string literal with MRI's escaping: the named
// escapes for \" \\ \b \f \n \r \t, \u00XX for other control characters, and
// UTF-8 text (including non-ASCII and the slash) passed through verbatim.
//
// It scans byte-wise, not rune-wise: only the ASCII bytes '"', '\\' and the
// controls (< 0x20) ever need escaping, and every byte of a multi-byte UTF-8
// sequence is >= 0x80, so non-ASCII text is copied through unexamined. Runs of
// ordinary bytes are bulk-appended, so an escape-free string (the common case)
// costs a single copy — far cheaper than decoding every rune.
func (g *gen) writeString(s string) {
	g.writeByte('"')
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x20 && c != '"' && c != '\\' {
			continue
		}
		if start < i {
			g.writeStr(s[start:i])
		}
		switch c {
		case '"':
			g.writeStr(`\"`)
		case '\\':
			g.writeStr(`\\`)
		case '\n':
			g.writeStr(`\n`)
		case '\r':
			g.writeStr(`\r`)
		case '\t':
			g.writeStr(`\t`)
		case '\f':
			g.writeStr(`\f`)
		case '\b':
			g.writeStr(`\b`)
		default: // remaining control characters < 0x20
			g.writeStr(`\u00`)
			g.writeByte(hexLower[(c>>4)&0xf])
			g.writeByte(hexLower[c&0xf])
		}
		start = i + 1
	}
	if start < len(s) {
		g.writeStr(s[start:])
	}
	g.writeByte('"')
}
