// Copyright (c) the go-ruby-json/json authors
//
// SPDX-License-Identifier: BSD-3-Clause

package json

import (
	"math"
	"math/big"
	"testing"
)

// valueSource is a Source that renders a tree of this package's own value model
// through an Encoder, so a streaming GenerateSource can be checked byte-identical
// to the tree-walking Generate over the same value. It exercises every Encoder
// method (Null/Bool/Int/Big/Float/Str, Array+Elem, Object+Key).
type valueSource struct{ v Value }

func (s valueSource) EmitTo(e *Encoder) error { return emitVal(e, s.v) }

// emitVal pushes one value-model value into the encoder.
func emitVal(e *Encoder, v Value) error {
	switch x := v.(type) {
	case nil:
		e.Null()
	case bool:
		e.Bool(x)
	case int:
		e.Int(int64(x))
	case int64:
		e.Int(x)
	case *big.Int:
		e.Big(x)
	case float64:
		return e.Float(x)
	case string:
		e.Str(x)
	case Symbol:
		e.Str(string(x))
	case []any:
		return e.Array(len(x), func() error {
			for _, el := range x {
				e.Elem()
				if err := emitVal(e, el); err != nil {
					return err
				}
			}
			return nil
		})
	case *Map:
		return e.Object(x.Len(), func() error {
			for _, p := range x.Pairs() {
				e.Key(keyString(p.Key))
				if err := emitVal(e, p.Val); err != nil {
					return err
				}
			}
			return nil
		})
	case map[string]any:
		// Generate emits a plain map in sorted-key order; mirror that here so the
		// streaming output is comparable.
		keys := sortedStringKeys(x)
		return e.Object(len(keys), func() error {
			for _, k := range keys {
				e.Key(k)
				if err := emitVal(e, x[k]); err != nil {
					return err
				}
			}
			return nil
		})
	}
	return nil
}

// TestParseInto checks ParseInto over the default valueBuilder matches Parse, and
// surfaces the parser's errors unchanged.
func TestParseInto(t *testing.T) {
	const doc = `{"a":1,"b":[true,null,"x",1.5,12345678901234567890],"c":{"d":-2}}`
	var b valueBuilder
	if err := ParseInto(doc, &b); err != nil {
		t.Fatalf("ParseInto: %v", err)
	}
	want := mustParse(t, doc)
	if got := mustGen(t, b.Result()); got != mustGen(t, want) {
		t.Fatalf("ParseInto result %q != Parse result %q", got, mustGen(t, want))
	}
	// Symbolized keys flow through ParseInto's options.
	var bs valueBuilder
	if err := ParseInto(`{"k":1}`, &bs, WithSymbolizeNames(true)); err != nil {
		t.Fatalf("ParseInto symbolize: %v", err)
	}
	m := bs.Result().(*Map)
	if _, ok := m.Get(Symbol("k")); !ok {
		t.Errorf("symbolized key absent: %#v", m.Pairs())
	}
	// A malformed document returns the parser error.
	var be valueBuilder
	if err := ParseInto(`{`, &be); err == nil {
		t.Error("ParseInto of malformed doc: want error")
	}
}

// TestGenerateSource checks GenerateSource is byte-identical to Generate over the
// same value across every value shape, and PrettyGenerateSource to PrettyGenerate.
func TestGenerateSource(t *testing.T) {
	big1 := new(big.Int).Exp(big.NewInt(10), big.NewInt(30), nil)
	nested := []any{
		nil, true, false, int64(7), 1.5, "s\n\"x", Symbol("sym"), big1,
		[]any{}, map[string]any{},
		mapOf("z", int64(1), "a", []any{int64(2), int64(3)}),
	}
	values := []Value{
		nil, true, int64(42), big1, 2.5, "hi", Symbol("y"),
		[]any{int64(1), "two", nil}, nested,
		mapOf("k", "v", "n", int64(0)),
	}
	for _, v := range values {
		want := mustGen(t, v)
		got, err := GenerateSource(valueSource{v})
		if err != nil {
			t.Fatalf("GenerateSource(%#v): %v", v, err)
		}
		if got != want {
			t.Errorf("GenerateSource = %q, Generate = %q", got, want)
		}
		pwant, err := PrettyGenerate(v)
		if err != nil {
			t.Fatalf("PrettyGenerate(%#v): %v", v, err)
		}
		pgot, err := PrettyGenerateSource(valueSource{v})
		if err != nil {
			t.Fatalf("PrettyGenerateSource(%#v): %v", v, err)
		}
		if pgot != pwant {
			t.Errorf("PrettyGenerateSource = %q, PrettyGenerate = %q", pgot, pwant)
		}
	}
}

// TestEncoderEmptyContainers covers the Array/Object zero-count and nil-emit
// fast paths (both write "[]" / "{}").
func TestEncoderEmptyContainers(t *testing.T) {
	cases := []struct {
		src  Source
		want string
	}{
		{srcFunc(func(e *Encoder) error { return e.Array(0, func() error { return nil }) }), "[]"},
		{srcFunc(func(e *Encoder) error { return e.Array(3, nil) }), "[]"},
		{srcFunc(func(e *Encoder) error { return e.Object(0, func() error { return nil }) }), "{}"},
		{srcFunc(func(e *Encoder) error { return e.Object(2, nil) }), "{}"},
	}
	for _, c := range cases {
		got, err := GenerateSource(c.src)
		if err != nil {
			t.Fatalf("GenerateSource: %v", err)
		}
		if got != c.want {
			t.Errorf("empty container = %q want %q", got, c.want)
		}
	}
}

// TestEncoderFloatError covers Encoder.Float's non-finite error arm and its
// propagation out of Array and Object emit callbacks.
func TestEncoderFloatError(t *testing.T) {
	inf := math.Inf(1)
	scalar := srcFunc(func(e *Encoder) error { return e.Float(inf) })
	if _, err := GenerateSource(scalar); err == nil {
		t.Error("scalar inf: want GeneratorError")
	}
	inArr := srcFunc(func(e *Encoder) error {
		return e.Array(1, func() error { e.Elem(); return e.Float(inf) })
	})
	if _, err := GenerateSource(inArr); err == nil {
		t.Error("array inf: want error")
	}
	inObj := srcFunc(func(e *Encoder) error {
		return e.Object(1, func() error { e.Key("k"); return e.Float(inf) })
	})
	if _, err := GenerateSource(inObj); err == nil {
		t.Error("object inf: want error")
	}
	// allow_nan lets a non-finite float through (covers the success arm via Source).
	got, err := GenerateSource(scalar, WithAllowNaN(true))
	if err != nil || got != "Infinity" {
		t.Errorf("allow_nan inf = %q, %v", got, err)
	}
}

// TestEncoderNesting covers the Encoder's nesting-limit guard on both Array and
// Object (a structure one level past the limit is a NestingError).
func TestEncoderNesting(t *testing.T) {
	deepArr := srcFunc(func(e *Encoder) error {
		return e.Array(1, func() error {
			e.Elem()
			return e.Array(1, func() error { e.Elem(); e.Int(1); return nil })
		})
	})
	if _, err := GenerateSource(deepArr, WithMaxNesting(1)); err == nil {
		t.Error("over-deep array: want NestingError")
	}
	deepObj := srcFunc(func(e *Encoder) error {
		return e.Object(1, func() error {
			e.Key("a")
			return e.Object(1, func() error { e.Key("b"); e.Int(1); return nil })
		})
	})
	if _, err := GenerateSource(deepObj, WithMaxNesting(1)); err == nil {
		t.Error("over-deep object: want NestingError")
	}
}

// srcFunc adapts a function to the Source interface.
type srcFunc func(*Encoder) error

func (f srcFunc) EmitTo(e *Encoder) error { return f(e) }

// mapOf builds an ordered *Map from alternating key/value pairs (string keys).
func mapOf(kv ...any) *Map {
	m := NewMap()
	for i := 0; i+1 < len(kv); i += 2 {
		m.Set(kv[i], kv[i+1])
	}
	return m
}
