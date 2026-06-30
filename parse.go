// Copyright (c) the go-ruby-json/json authors
//
// SPDX-License-Identifier: BSD-3-Clause

package json

import (
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

// parse decodes the document s into a Ruby value per config c, reproducing MRI's
// JSON.parse semantics and its JSON::ParserError / JSON::NestingError messages.
func parse(s string, c *config) (Value, error) {
	var b valueBuilder
	if err := parseInto(s, &b, c); err != nil {
		return nil, err
	}
	return b.Result(), nil
}

// parseInto decodes the document s straight into the host Builder b per config c,
// driving it with one streaming pass (no intermediate value tree). It is the
// engine behind both [parse] (default builder) and the public [ParseInto].
func parseInto(s string, b Builder, c *config) error {
	p := &parser{s: s, c: c, b: b}
	p.skipSpace()
	if err := p.value(0); err != nil {
		return err
	}
	p.skipSpace()
	if p.pos != len(p.s) {
		// Trailing content after a complete value: MRI reports the leftover token.
		return p.errAt(p.pos, "unexpected token at end of stream "+quoteTok(p.rest()))
	}
	return nil
}

// parser is the recursive-descent JSON reader. It emits each value into b as it
// is read, rather than returning a tree, so the host materialises its own object
// graph in a single pass.
type parser struct {
	s   string
	pos int
	c   *config
	b   Builder
}

// nestingLimit reports the active parse nesting limit, or -1 for unlimited.
func (p *parser) nestingLimit() int { return nestingLimit(p.c) }

// lineCol returns the 1-based line and column of byte offset pos, counting
// columns by character (MRI reports column = chars+1 since the line start).
func (p *parser) lineCol(pos int) (int, int) {
	line, col := 1, 1
	for i := 0; i < pos && i < len(p.s); {
		if p.s[i] == '\n' {
			line++
			col = 1
			i++
			continue
		}
		_, sz := utf8.DecodeRuneInString(p.s[i:])
		col++
		i += sz
	}
	return line, col
}

// errAt builds a ParserError whose message carries MRI's "at line L column C"
// suffix for byte offset pos.
func (p *parser) errAt(pos int, msg string) error {
	line, col := p.lineCol(pos)
	return &ParserError{Message: msg + " at line " + strconv.Itoa(line) + " column " + strconv.Itoa(col)}
}

// rest returns the remaining input from pos (used in "end of stream" messages).
func (p *parser) rest() string { return p.s[p.pos:] }

// quoteTok renders a token snippet the way MRI does in its messages: single
// quotes around the literal text.
func quoteTok(s string) string { return "'" + s + "'" }

// skipSpace advances over JSON whitespace.
func (p *parser) skipSpace() {
	for p.pos < len(p.s) {
		switch p.s[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

// value parses one JSON value at the given structure depth, emitting it into the
// builder.
func (p *parser) value(depth int) error {
	if p.pos >= len(p.s) {
		return p.errAt(p.pos, "unexpected end of input")
	}
	switch ch := p.s[p.pos]; {
	case ch == '{':
		return p.object(depth)
	case ch == '[':
		return p.array(depth)
	case ch == '"':
		s, err := p.parseString()
		if err != nil {
			return err
		}
		p.b.Str(s)
		return nil
	case p.c.allowNaN && strings.HasPrefix(p.rest(), "-Infinity"):
		p.pos += 9
		p.b.Float(inf(-1))
		return nil
	case ch == '-' || (ch >= '0' && ch <= '9'):
		return p.number()
	case strings.HasPrefix(p.rest(), "true"):
		p.pos += 4
		p.b.Bool(true)
		return nil
	case strings.HasPrefix(p.rest(), "false"):
		p.pos += 5
		p.b.Bool(false)
		return nil
	case strings.HasPrefix(p.rest(), "null"):
		p.pos += 4
		p.b.Null()
		return nil
	case p.c.allowNaN && strings.HasPrefix(p.rest(), "NaN"):
		p.pos += 3
		p.b.Float(nan())
		return nil
	case p.c.allowNaN && strings.HasPrefix(p.rest(), "Infinity"):
		p.pos += 8
		p.b.Float(inf(1))
		return nil
	default:
		return p.unexpectedToken()
	}
}

// unexpectedToken classifies a value that does not start a valid JSON token,
// matching MRI's two shapes: "unexpected token '<word>'" for an alphabetic word
// (e.g. nul/tru/NaN) and "unexpected character: '<rest>'" otherwise.
func (p *parser) unexpectedToken() error {
	r, _ := utf8.DecodeRuneInString(p.rest())
	if isWordStart(r) {
		return p.errAt(p.pos, "unexpected token "+quoteTok(p.word()))
	}
	return p.errAt(p.pos, "unexpected character: "+quoteTok(p.rest()))
}

// isWordStart reports whether r begins a bareword (letters), used to pick the
// "token" vs "character" message form.
func isWordStart(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
}

// word returns the maximal run of letters/digits from pos (the offending token
// text for "unexpected token" messages).
func (p *parser) word() string {
	end := p.pos
	for end < len(p.s) {
		ch := p.s[end]
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' {
			end++
			continue
		}
		break
	}
	return p.s[p.pos:end]
}

// array parses a JSON array body, emitting BeginArray/elements/EndArray.
func (p *parser) array(depth int) error {
	if lim := p.nestingLimit(); lim >= 0 && depth+1 > lim {
		return &NestingError{Message: "nesting of " + strconv.Itoa(depth+1) + " is too deep"}
	}
	open := p.pos
	p.pos++ // consume '['
	p.skipSpace()
	if p.pos < len(p.s) && p.s[p.pos] == ']' {
		p.pos++
		p.b.BeginArray(0)
		p.b.EndArray()
		return nil
	}
	p.b.BeginArray(p.countElems(open))
	for {
		p.skipSpace()
		if err := p.value(depth + 1); err != nil {
			return err
		}
		p.skipSpace()
		if p.pos >= len(p.s) {
			return p.errAt(p.pos, "unexpected end of input")
		}
		switch p.s[p.pos] {
		case ',':
			p.pos++
		case ']':
			p.pos++
			p.b.EndArray()
			return nil
		default:
			return p.errAt(p.pos, "expected ',' or ']' after array value")
		}
	}
}

// object parses a JSON object body, emitting BeginObject/(Key,value)*/EndObject
// in document order so the host preserves key order.
func (p *parser) object(depth int) error {
	if lim := p.nestingLimit(); lim >= 0 && depth+1 > lim {
		return &NestingError{Message: "nesting of " + strconv.Itoa(depth+1) + " is too deep"}
	}
	open := p.pos
	p.pos++ // consume '{'
	p.skipSpace()
	if p.pos < len(p.s) && p.s[p.pos] == '}' {
		p.pos++
		p.b.BeginObject(0)
		p.b.EndObject()
		return nil
	}
	p.b.BeginObject(p.countElems(open))
	for {
		p.skipSpace()
		if p.pos >= len(p.s) {
			return p.errAt(p.pos, "expected object key, got EOF")
		}
		if p.s[p.pos] != '"' {
			return p.errAt(p.pos, "expected object key, got "+quoteTok(p.rest()))
		}
		key, err := p.parseString()
		if err != nil {
			return err
		}
		p.skipSpace()
		if p.pos >= len(p.s) || p.s[p.pos] != ':' {
			return p.errAt(p.pos, "expected ':' after object key")
		}
		p.pos++ // consume ':'
		p.skipSpace()
		p.b.Key(key, p.c.symbolizeNames)
		if err := p.value(depth + 1); err != nil {
			return err
		}
		p.skipSpace()
		if p.pos >= len(p.s) {
			return p.errAt(p.pos, "unexpected end of input")
		}
		switch p.s[p.pos] {
		case ',':
			p.pos++
			p.skipSpace()
			// A trailing comma before '}' is MRI's "expected object key, got: '}'".
			if p.pos < len(p.s) && p.s[p.pos] == '}' {
				return p.errAt(p.pos, "expected object key, got: "+quoteTok(p.rest()))
			}
		case '}':
			p.pos++
			p.b.EndObject()
			return nil
		default:
			return p.errAt(p.pos, "expected ',' or '}' after object value, got: "+quoteTok(p.rest()))
		}
	}
}

// countScanCap bounds the pre-sizing scan (countElems) so a container's hint
// costs O(1) amortised over the parse rather than O(body): pre-sizing is only a
// hint, so for a body longer than the cap a cheap estimate suffices and the
// element slice simply grows the rest of the way. Without the cap a deeply
// nested document would rescan each enclosing body and cost O(depth²).
const countScanCap = 512

// countElems returns a pre-sizing hint for the container whose opening '[' or
// '{' is at open: the exact number of top-level elements (array items or object
// pairs) when the body fits within countScanCap bytes, else a quick estimate. It
// scans once over the (bounded) body, tracking only nesting depth and skipping
// string bodies so commas inside strings or nested containers are ignored. A
// malformed body is harmless here because the real parse re-validates and
// reports the error. The container is known non-empty (the caller handles the
// empty case), so the hint is always >= 1.
func (p *parser) countElems(open int) int {
	depth := 0
	commas := 0
	end := open + countScanCap
	if end > len(p.s) {
		end = len(p.s)
	}
	for i := open; i < end; i++ {
		switch p.s[i] {
		case '"':
			// Skip the string body, honouring backslash escapes.
			i++
			for i < end {
				if p.s[i] == '\\' {
					i++
				} else if p.s[i] == '"' {
					break
				}
				i++
			}
		case '[', '{':
			depth++
		case ']', '}':
			depth--
			if depth == 0 {
				return commas + 1
			}
		case ',':
			if depth == 1 {
				commas++
			}
		}
	}
	// The body did not close within the scan window: return the commas already
	// seen plus one as a conservative lower-bound hint. Under-shooting merely lets
	// the element slice grow the rest of the way (amortised O(1)); over-shooting —
	// e.g. a bytes-based estimate — would badly over-allocate a deeply nested
	// document, where each level's body is large but holds only a few elements.
	return commas + 1
}

// number parses a JSON number, returning an int64 / *big.Int for an integral
// literal and a float64 otherwise. A malformed numeric literal is MRI's
// "invalid number" or "unexpected character" depending on the offending prefix.
func (p *parser) number() error {
	start := p.pos
	// Disallow a leading '+' and a bare '.' (MRI: "unexpected character").
	// (Reached only via the '-'/digit dispatch, so the first char is valid here.)
	i := p.pos
	if p.s[i] == '-' {
		i++
	}
	// integer part
	intStart := i
	for i < len(p.s) && p.s[i] >= '0' && p.s[i] <= '9' {
		i++
	}
	intDigits := p.s[intStart:i]
	isFloat := false
	// Leading-zero rule: "0" alone is fine, "01" is invalid.
	if len(intDigits) > 1 && intDigits[0] == '0' {
		p.pos = i
		return p.errAt(start, "invalid number: "+quoteTok(p.s[start:i]))
	}
	if len(intDigits) == 0 {
		// e.g. "-" with no digits, or a number-leading char with no integer part.
		p.pos = i
		return p.errAt(start, "invalid number: "+quoteTok(p.s[start:i]))
	}
	// fraction
	if i < len(p.s) && p.s[i] == '.' {
		isFloat = true
		i++
		fs := i
		for i < len(p.s) && p.s[i] >= '0' && p.s[i] <= '9' {
			i++
		}
		if i == fs { // "1." with no fraction digits
			p.pos = i
			return p.errAt(start, "invalid number: "+quoteTok(p.s[start:i]))
		}
	}
	// exponent
	if i < len(p.s) && (p.s[i] == 'e' || p.s[i] == 'E') {
		isFloat = true
		i++
		if i < len(p.s) && (p.s[i] == '+' || p.s[i] == '-') {
			i++
		}
		es := i
		for i < len(p.s) && p.s[i] >= '0' && p.s[i] <= '9' {
			i++
		}
		if i == es { // "1e" with no exponent digits
			p.pos = i
			return p.errAt(start, "invalid number: "+quoteTok(p.s[start:i]))
		}
	}
	lit := p.s[start:i]
	p.pos = i
	if isFloat {
		// The literal is syntactically valid here, so ParseFloat only ever reports
		// ErrRange (overflow -> ±Inf, underflow -> 0); MRI returns those values too
		// (1e400 -> Infinity), so the value is used regardless of the range error.
		f, _ := strconv.ParseFloat(lit, 64)
		p.b.Float(f)
		return nil
	}
	if n, err := strconv.ParseInt(lit, 10, 64); err == nil {
		p.b.Int(n)
		return nil
	}
	bi, _ := new(big.Int).SetString(lit, 10)
	p.b.Big(bi)
	return nil
}

// parseString parses a JSON string literal beginning at the current '"',
// decoding escapes (including \uXXXX and surrogate pairs) into a Go string.
func (p *parser) parseString() (string, error) {
	start := p.pos
	p.pos++ // consume opening '"'
	// Fast path: scan for the closing quote over a run of ordinary bytes. The
	// overwhelmingly common string has no backslash escape, so it can be returned
	// as a direct sub-slice of the input with no Builder and no per-byte copy.
	bodyStart := p.pos
	for p.pos < len(p.s) {
		ch := p.s[p.pos]
		if ch == '"' {
			s := p.s[bodyStart:p.pos]
			p.pos++
			return s, nil
		}
		if ch == '\\' || ch < 0x20 {
			break // hand off to the slow, escape-aware path below
		}
		p.pos++
	}
	// Slow path: the string contains an escape (or a control char): build it,
	// seeding the Builder with the verbatim run already scanned.
	var sb strings.Builder
	sb.WriteString(p.s[bodyStart:p.pos])
	for {
		if p.pos >= len(p.s) {
			return "", p.errAt(p.pos, `unexpected end of input, expected closing "`)
		}
		ch := p.s[p.pos]
		switch {
		case ch == '"':
			p.pos++
			return sb.String(), nil
		case ch == '\\':
			if err := p.parseEscape(&sb, start); err != nil {
				return "", err
			}
		case ch < 0x20:
			// MRI: "invalid ASCII control character in string: <rest bytes raw>".
			return "", p.errAt(p.pos, "invalid ASCII control character in string: "+p.rest())
		default:
			sb.WriteByte(ch)
			p.pos++
		}
	}
}

// parseEscape decodes one backslash escape into sb. esc points at the '\'.
func (p *parser) parseEscape(sb *strings.Builder, strStart int) error {
	escPos := p.pos
	p.pos++ // consume '\'
	if p.pos >= len(p.s) {
		return p.errAt(p.pos, `unexpected end of input, expected closing "`)
	}
	switch c := p.s[p.pos]; c {
	case '"':
		sb.WriteByte('"')
		p.pos++
	case '\\':
		sb.WriteByte('\\')
		p.pos++
	case '/':
		sb.WriteByte('/')
		p.pos++
	case 'b':
		sb.WriteByte('\b')
		p.pos++
	case 'f':
		sb.WriteByte('\f')
		p.pos++
	case 'n':
		sb.WriteByte('\n')
		p.pos++
	case 'r':
		sb.WriteByte('\r')
		p.pos++
	case 't':
		sb.WriteByte('\t')
		p.pos++
	case 'u':
		return p.parseUnicodeEscape(sb, escPos)
	default:
		_ = strStart
		// MRI: "invalid escape character in string: '<rest from the backslash>'".
		return p.errAt(escPos, "invalid escape character in string: "+quoteTok(p.s[escPos:]))
	}
	return nil
}

// parseUnicodeEscape decodes a \uXXXX (and a following \uXXXX low surrogate when
// the first is a high surrogate). escPos points at the '\' of the first escape.
func (p *parser) parseUnicodeEscape(sb *strings.Builder, escPos int) error {
	hi, err := p.readHex4(escPos)
	if err != nil {
		return err
	}
	if hi >= 0xD800 && hi <= 0xDBFF {
		// High surrogate: a low-surrogate \uXXXX must follow.
		if !strings.HasPrefix(p.s[p.pos:], `\u`) {
			return p.errAt(escPos, "incomplete surrogate pair at "+quoteTok(p.s[escPos:]))
		}
		loPos := p.pos
		lo, ok := p.readHex4Opt(loPos)
		if !ok {
			// A malformed low \u escape is an "incomplete surrogate pair" in MRI,
			// not a bare unicode-escape error.
			return p.errAt(escPos, "incomplete surrogate pair at "+quoteTok(p.s[escPos:]))
		}
		if lo < 0xDC00 || lo > 0xDFFF {
			return p.errAt(escPos, "invalid surrogate pair at "+quoteTok(p.s[escPos:]))
		}
		r := 0x10000 + (rune(hi)-0xD800)<<10 + (rune(lo) - 0xDC00)
		sb.WriteRune(r)
		return nil
	}
	// A lone surrogate (high without a following low handled above, or a bare low)
	// is encoded to its WTF-8 three-byte form, as MRI does.
	writeWTF8(sb, hi)
	return nil
}

// writeWTF8 writes code unit u (which may be a lone surrogate) as its raw
// three-byte UTF-8-style encoding, matching MRI's tolerance of unpaired
// surrogates in \u escapes.
func writeWTF8(sb *strings.Builder, u int) {
	if u >= 0xD800 && u <= 0xDFFF {
		sb.WriteByte(byte(0xE0 | (u >> 12)))
		sb.WriteByte(byte(0x80 | ((u >> 6) & 0x3F)))
		sb.WriteByte(byte(0x80 | (u & 0x3F)))
		return
	}
	sb.WriteRune(rune(u))
}

// readHex4 reads the first `\uXXXX` escape of a sequence starting at the '\' at
// escPos, returning the code unit or the MRI "incomplete unicode character
// escape sequence" error.
func (p *parser) readHex4(escPos int) (int, error) {
	v, ok := p.readHex4Opt(escPos)
	if !ok {
		return 0, p.errAt(escPos, "incomplete unicode character escape sequence at "+quoteTok(p.s[escPos:]))
	}
	return v, nil
}

// readHex4Opt reads a `\uXXXX` escape starting at the '\' at uPos, advancing pos
// past the four hex digits and returning the code unit and whether it was
// well-formed (four hex digits present).
func (p *parser) readHex4Opt(uPos int) (int, bool) {
	p.pos = uPos + 2 // skip "\u"
	if p.pos+4 > len(p.s) {
		return 0, false
	}
	var v int
	for k := 0; k < 4; k++ {
		d := hexVal(p.s[p.pos+k])
		if d < 0 {
			return 0, false
		}
		v = v<<4 | d
	}
	p.pos += 4
	return v, true
}

// hexVal returns the value of a hex digit byte, or -1 if it is not one.
func hexVal(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10
	}
	return -1
}
