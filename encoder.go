// Copyright (c) the go-ruby-json/json authors
//
// SPDX-License-Identifier: BSD-3-Clause

package json

import "math/big"

// Source is a value a host can render to JSON by pushing it into an [Encoder],
// the mirror of [Builder] on the generate side: instead of first converting its
// object graph into this package's value model and handing that to [Generate],
// the host walks its own graph once and calls the Encoder's typed methods. This
// removes the per-node intermediate value and its type-switch.
//
// [GenerateSource] / [PrettyGenerateSource] call EmitTo with a configured
// Encoder; EmitTo emits exactly one value (a scalar, or one Array/Object whose
// body emits its elements). A non-nil error (e.g. a non-finite float without
// allow_nan, surfaced by [Encoder.Float]) aborts generation and is returned to
// the caller.
type Source interface {
	EmitTo(e *Encoder) error
}

// Encoder is the generate-side façade a [Source] writes through. It wraps the
// streaming generator, so a host emits a value with one call and the bytes,
// nesting limit, MRI-faithful formatting and pretty-print layout are handled
// here.
type Encoder struct {
	g     *gen
	depth int
	// arrIdx is a per-open-container counter stack: the top entry counts elements
	// (array) or pairs (object) emitted so far in the innermost container, so Elem
	// and Key write the separators only between items.
	arrIdx []int
}

// Null emits a JSON null.
func (e *Encoder) Null() { e.g.writeStr("null") }

// Bool emits true or false.
func (e *Encoder) Bool(b bool) {
	if b {
		e.g.writeStr("true")
	} else {
		e.g.writeStr("false")
	}
}

// Int emits an integer that fits in int64.
func (e *Encoder) Int(n int64) { e.g.writeInt(n) }

// Big emits an arbitrary-precision integer.
func (e *Encoder) Big(n *big.Int) { e.g.writeStr(n.String()) }

// Float emits a float with MRI's fpconv layout, returning a GeneratorError for a
// non-finite value unless allow_nan is configured.
func (e *Encoder) Float(f float64) error { return e.g.float(f) }

// Str emits a string value with MRI's JSON string escaping.
func (e *Encoder) Str(s string) { e.g.writeString(s) }

// Array emits a JSON array of n elements (n is the exact count, used only for
// the array_nl / indent layout decisions — it is not relied on for correctness).
// emit is called once and must push exactly n element values via the encoder; it
// honours the configured nesting limit and pretty-print layout. A nil emit (or
// n == 0) writes an empty array.
func (e *Encoder) Array(n int, emit func() error) error {
	if lim := e.g.nestingLimit(); lim >= 0 && e.depth+1 > lim {
		return genNestingErr(lim)
	}
	if n == 0 || emit == nil {
		e.g.writeStr("[]")
		return nil
	}
	e.depth++
	e.g.writeByte('[')
	e.g.writeStr(e.g.c.arrayNL)
	e.arrIdx = append(e.arrIdx, 0)
	if err := emit(); err != nil {
		return err
	}
	e.arrIdx = e.arrIdx[:len(e.arrIdx)-1]
	e.g.writeStr(e.g.c.arrayNL)
	e.depth--
	e.g.indent(e.depth)
	e.g.writeByte(']')
	return nil
}

// Elem must be called once per array element by the Array emit callback, before
// emitting that element, so the comma and newline separators (and the per-line
// indent) land between elements exactly as MRI lays them out.
func (e *Encoder) Elem() {
	i := len(e.arrIdx) - 1
	if e.arrIdx[i] > 0 {
		e.g.writeByte(',')
		e.g.writeStr(e.g.c.arrayNL)
	}
	e.arrIdx[i]++
	e.g.indent(e.depth)
}

// Object emits a JSON object of n pairs (n is the exact count, used only for the
// object_nl / indent layout). emit is called once and must push exactly n pairs
// via [Encoder.Key] then a value. A nil emit (or n == 0) writes an empty object.
func (e *Encoder) Object(n int, emit func() error) error {
	if lim := e.g.nestingLimit(); lim >= 0 && e.depth+1 > lim {
		return genNestingErr(lim)
	}
	if n == 0 || emit == nil {
		e.g.writeStr("{}")
		return nil
	}
	e.depth++
	e.g.writeByte('{')
	e.g.writeStr(e.g.c.objectNL)
	e.arrIdx = append(e.arrIdx, 0)
	if err := emit(); err != nil {
		return err
	}
	e.arrIdx = e.arrIdx[:len(e.arrIdx)-1]
	e.g.writeStr(e.g.c.objectNL)
	e.depth--
	e.g.indent(e.depth)
	e.g.writeByte('}')
	return nil
}

// Key emits an object key (the pair separator, indent, the quoted key and the
// space/colon) before its value, and must be called once per pair by the Object
// emit callback, immediately before emitting that pair's value. The key is
// rendered as a JSON string (MRI coerces every object key to a string).
func (e *Encoder) Key(s string) {
	i := len(e.arrIdx) - 1
	if e.arrIdx[i] > 0 {
		e.g.writeByte(',')
		e.g.writeStr(e.g.c.objectNL)
	}
	e.arrIdx[i]++
	e.g.indent(e.depth)
	e.g.writeString(s)
	e.g.writeStr(e.g.c.spaceB)
	e.g.writeByte(':')
	e.g.writeStr(e.g.c.space)
}

// GenerateSource renders src to a compact JSON document by pulling its values
// through an [Encoder], the streaming counterpart of [Generate] (no intermediate
// value tree). The options behave exactly as for Generate.
func GenerateSource(src Source, opts ...Option) (string, error) {
	c := resolve(opts)
	return generateSource(src, &c)
}

// PrettyGenerateSource is [GenerateSource] with MRI's pretty_generate layout
// (two-space indent, "\n" newlines, a space after ':'), overridable by opts.
func PrettyGenerateSource(src Source, opts ...Option) (string, error) {
	pretty := []Option{
		WithIndent("  "),
		WithSpace(" "),
		WithObjectNL("\n"),
		WithArrayNL("\n"),
	}
	c := resolve(append(pretty, opts...))
	return generateSource(src, &c)
}

// generateSource runs one streaming generation of src under c, reusing the
// pooled scratch buffer like [generate].
func generateSource(src Source, c *config) (string, error) {
	bp := genPool.Get().(*[]byte)
	g := &gen{c: c, buf: (*bp)[:0]}
	e := &Encoder{g: g}
	if err := src.EmitTo(e); err != nil {
		*bp = g.buf
		genPool.Put(bp)
		return "", err
	}
	out := string(g.buf)
	*bp = g.buf
	genPool.Put(bp)
	return out, nil
}
