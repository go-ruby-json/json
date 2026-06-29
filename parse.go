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
	p := &parser{s: s, c: c}
	p.skipSpace()
	v, err := p.value(0)
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.pos != len(p.s) {
		// Trailing content after a complete value: MRI reports the leftover token.
		return nil, p.errAt(p.pos, "unexpected token at end of stream "+quoteTok(p.rest()))
	}
	return v, nil
}

// parser is the recursive-descent JSON reader.
type parser struct {
	s   string
	pos int
	c   *config
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

// value parses one JSON value at the given structure depth.
func (p *parser) value(depth int) (Value, error) {
	if p.pos >= len(p.s) {
		return nil, p.errAt(p.pos, "unexpected end of input")
	}
	switch ch := p.s[p.pos]; {
	case ch == '{':
		return p.object(depth)
	case ch == '[':
		return p.array(depth)
	case ch == '"':
		s, err := p.parseString()
		if err != nil {
			return nil, err
		}
		return s, nil
	case p.c.allowNaN && strings.HasPrefix(p.rest(), "-Infinity"):
		p.pos += 9
		return inf(-1), nil
	case ch == '-' || (ch >= '0' && ch <= '9'):
		return p.number()
	case strings.HasPrefix(p.rest(), "true"):
		p.pos += 4
		return true, nil
	case strings.HasPrefix(p.rest(), "false"):
		p.pos += 5
		return false, nil
	case strings.HasPrefix(p.rest(), "null"):
		p.pos += 4
		return nil, nil
	case p.c.allowNaN && strings.HasPrefix(p.rest(), "NaN"):
		p.pos += 3
		return nan(), nil
	case p.c.allowNaN && strings.HasPrefix(p.rest(), "Infinity"):
		p.pos += 8
		return inf(1), nil
	default:
		return nil, p.unexpectedToken()
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

// array parses a JSON array body.
func (p *parser) array(depth int) (Value, error) {
	if lim := p.nestingLimit(); lim >= 0 && depth+1 > lim {
		return nil, &NestingError{Message: "nesting of " + strconv.Itoa(depth+1) + " is too deep"}
	}
	p.pos++ // consume '['
	arr := []any{}
	p.skipSpace()
	if p.pos < len(p.s) && p.s[p.pos] == ']' {
		p.pos++
		return arr, nil
	}
	for {
		p.skipSpace()
		v, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		arr = append(arr, v)
		p.skipSpace()
		if p.pos >= len(p.s) {
			return nil, p.errAt(p.pos, "unexpected end of input")
		}
		switch p.s[p.pos] {
		case ',':
			p.pos++
		case ']':
			p.pos++
			return arr, nil
		default:
			return nil, p.errAt(p.pos, "expected ',' or ']' after array value")
		}
	}
}

// object parses a JSON object body, preserving key order in a *Map.
func (p *parser) object(depth int) (Value, error) {
	if lim := p.nestingLimit(); lim >= 0 && depth+1 > lim {
		return nil, &NestingError{Message: "nesting of " + strconv.Itoa(depth+1) + " is too deep"}
	}
	p.pos++ // consume '{'
	m := NewMap()
	p.skipSpace()
	if p.pos < len(p.s) && p.s[p.pos] == '}' {
		p.pos++
		return m, nil
	}
	for {
		p.skipSpace()
		if p.pos >= len(p.s) {
			return nil, p.errAt(p.pos, "expected object key, got EOF")
		}
		if p.s[p.pos] != '"' {
			return nil, p.errAt(p.pos, "expected object key, got "+quoteTok(p.rest()))
		}
		key, err := p.parseString()
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		if p.pos >= len(p.s) || p.s[p.pos] != ':' {
			return nil, p.errAt(p.pos, "expected ':' after object key")
		}
		p.pos++ // consume ':'
		p.skipSpace()
		v, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		if p.c.symbolizeNames {
			m.Set(Symbol(key), v)
		} else {
			m.Set(key, v)
		}
		p.skipSpace()
		if p.pos >= len(p.s) {
			return nil, p.errAt(p.pos, "unexpected end of input")
		}
		switch p.s[p.pos] {
		case ',':
			p.pos++
			p.skipSpace()
			// A trailing comma before '}' is MRI's "expected object key, got: '}'".
			if p.pos < len(p.s) && p.s[p.pos] == '}' {
				return nil, p.errAt(p.pos, "expected object key, got: "+quoteTok(p.rest()))
			}
		case '}':
			p.pos++
			return m, nil
		default:
			return nil, p.errAt(p.pos, "expected ',' or '}' after object value, got: "+quoteTok(p.rest()))
		}
	}
}

// number parses a JSON number, returning an int64 / *big.Int for an integral
// literal and a float64 otherwise. A malformed numeric literal is MRI's
// "invalid number" or "unexpected character" depending on the offending prefix.
func (p *parser) number() (Value, error) {
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
		return nil, p.errAt(start, "invalid number: "+quoteTok(p.s[start:i]))
	}
	if len(intDigits) == 0 {
		// e.g. "-" with no digits, or a number-leading char with no integer part.
		p.pos = i
		return nil, p.errAt(start, "invalid number: "+quoteTok(p.s[start:i]))
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
			return nil, p.errAt(start, "invalid number: "+quoteTok(p.s[start:i]))
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
			return nil, p.errAt(start, "invalid number: "+quoteTok(p.s[start:i]))
		}
	}
	lit := p.s[start:i]
	p.pos = i
	if isFloat {
		// The literal is syntactically valid here, so ParseFloat only ever reports
		// ErrRange (overflow -> ±Inf, underflow -> 0); MRI returns those values too
		// (1e400 -> Infinity), so the value is used regardless of the range error.
		f, _ := strconv.ParseFloat(lit, 64)
		return f, nil
	}
	if n, err := strconv.ParseInt(lit, 10, 64); err == nil {
		return n, nil
	}
	bi, _ := new(big.Int).SetString(lit, 10)
	return bi, nil
}

// parseString parses a JSON string literal beginning at the current '"',
// decoding escapes (including \uXXXX and surrogate pairs) into a Go string.
func (p *parser) parseString() (string, error) {
	start := p.pos
	p.pos++ // consume opening '"'
	var sb strings.Builder
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
