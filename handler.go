// Copyright (c) the go-ruby-json/json authors
//
// SPDX-License-Identifier: BSD-3-Clause

package json

import "math/big"

// Builder is a streaming sink the parser drives as it reads a document, so a
// host (such as go-embedded-ruby) can construct its own object graph directly —
// with no intermediate tree of this package's `any` values, and no second
// conversion pass. [ParseInto] feeds a Builder; [Parse] is [ParseInto] over the
// package's own [valueBuilder].
//
// The parser calls the scalar methods (Null/Bool/Int/Big/Float/Str) for leaf
// values and the container methods (BeginArray/EndArray and BeginObject/Key/
// EndObject) around composites. Every emitted value — a scalar, or the array or
// object just closed — becomes the *current value*; the host attaches it to its
// enclosing container (the most recent open array element, or the value of the
// most recent object Key) on the next call, or returns it from [Builder.Result]
// at the top level. The call sequence is well-formed by construction: containers
// nest correctly, an object alternates Key then value, and exactly one value is
// emitted at the top level.
//
// A Builder that pre-sizes its containers (BeginArray/BeginObject carry the
// element count) and interns small scalars turns parsing into a single
// allocation-light pass.
type Builder interface {
	// Null appends a JSON null.
	Null()
	// Bool appends a JSON true/false.
	Bool(b bool)
	// Int appends an integer that fits in int64.
	Int(n int64)
	// Big appends an integer too large for int64 (never nil).
	Big(n *big.Int)
	// Float appends a floating-point number.
	Float(f float64)
	// Str appends a string value (an object value or array element, never a key).
	Str(s string)
	// BeginArray opens an array; n is a capacity hint (the exact element count,
	// which the parser computes by a cheap pre-scan) for pre-allocation. The
	// emitted values up to the matching EndArray are its elements.
	BeginArray(n int)
	// EndArray closes the array opened by the matching BeginArray.
	EndArray()
	// BeginObject opens an object; n is a capacity hint (the exact pair count) for
	// pre-allocation. It is followed by (Key, value) sequences up to EndObject.
	BeginObject(n int)
	// Key sets the key for the next emitted value. symbolize reports whether the
	// host should intern it as a Symbol (MRI's symbolize_names: true).
	Key(s string, symbolize bool)
	// EndObject closes the object opened by the matching BeginObject.
	EndObject()
	// Result returns the single top-level value once parsing completes.
	Result() Value
}

// valueBuilder is the default Builder: it materialises this package's own value
// model (nil/bool/int64/*big.Int/float64/string, []any, ordered *Map), so Parse
// keeps returning exactly that tree. It is a thin shim over the streaming parser
// — used by Parse and ParseBytes — kept allocation-conscious (pre-sized
// containers) but without host-specific interning.
type valueBuilder struct {
	stack []frame
	root  Value
	done  bool
	// keys caches the boxed [Value] for each distinct object key seen so far, so
	// a key that recurs across sibling records (the norm for arrays of objects)
	// is boxed into an interface once rather than on every occurrence. MRI
	// likewise deduplicates hash keys (frozen fstrings); the parsed structure is
	// unchanged — only the interface header is shared. Built lazily on first Key.
	keys map[string]Value
}

// frame is one open container on the builder stack.
type frame struct {
	arr []any // non-nil while an array is open
	m   *Map  // non-nil while an object is open
	key Value // pending key set by Key, consumed by the next emit
}

// emit attaches v to the innermost open container, or records it as the result
// at the top level.
func (b *valueBuilder) emit(v Value) {
	if n := len(b.stack); n > 0 {
		f := &b.stack[n-1]
		if f.m != nil {
			f.m.Set(f.key, v)
			return
		}
		f.arr = append(f.arr, v)
		return
	}
	b.root = v
	b.done = true
}

func (b *valueBuilder) Null()           { b.emit(nil) }
func (b *valueBuilder) Bool(x bool)     { b.emit(x) }
func (b *valueBuilder) Int(n int64)     { b.emit(n) }
func (b *valueBuilder) Big(n *big.Int)  { b.emit(n) }
func (b *valueBuilder) Float(f float64) { b.emit(f) }
func (b *valueBuilder) Str(s string)    { b.emit(s) }

func (b *valueBuilder) BeginArray(n int) {
	b.stack = append(b.stack, frame{arr: make([]any, 0, n)})
}

func (b *valueBuilder) EndArray() {
	f := b.stack[len(b.stack)-1]
	b.stack = b.stack[:len(b.stack)-1]
	b.emit(f.arr)
}

func (b *valueBuilder) BeginObject(n int) {
	// No index map: small objects (the norm) resolve keys by linear scan, and a
	// Map builds its hash index itself only once it grows past its threshold.
	b.stack = append(b.stack, frame{m: &Map{pairs: make([]Pair, 0, n)}})
}

func (b *valueBuilder) Key(s string, symbolize bool) {
	f := &b.stack[len(b.stack)-1]
	if k, ok := b.keys[s]; ok {
		f.key = k
		return
	}
	var k Value
	if symbolize {
		k = Symbol(s)
	} else {
		k = s
	}
	if b.keys == nil {
		b.keys = make(map[string]Value, 8)
	}
	b.keys[s] = k
	f.key = k
}

func (b *valueBuilder) EndObject() {
	f := b.stack[len(b.stack)-1]
	b.stack = b.stack[:len(b.stack)-1]
	b.emit(f.m)
}

func (b *valueBuilder) Result() Value { return b.root }
