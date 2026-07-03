// Copyright (c) the go-ruby-json/json authors
//
// SPDX-License-Identifier: BSD-3-Clause

// Package json is a pure-Go (CGO-free) reimplementation of Ruby's JSON parser
// and generator, matching MRI 4.0.5's JSON.parse / JSON.generate /
// JSON.pretty_generate byte-for-byte. Parsing and generating JSON is fully
// deterministic — it needs no interpreter — so it lives here as pure Go: [Parse]
// turns a JSON document into a tree of Ruby values, and [Generate] /
// [PrettyGenerate] render such a tree back to the exact bytes MRI's json gem
// produces.
//
// It is the JSON backend for go-embedded-ruby, but is a standalone, reusable
// module with no dependency on the Ruby runtime — a sibling of go-ruby-regexp
// (the Onigmo engine), go-ruby-erb (the ERB compiler) and go-ruby-yaml (Psych).
//
// # Ruby value model
//
// A Ruby value is represented by an [any] drawn from a small, fixed set of Go
// types so a host (such as go-embedded-ruby) can map its own object graph to and
// from this package:
//
//	Ruby            Go (Generate accepts)            Go (Parse returns)
//	----            ---------------------            ------------------
//	nil             nil                              nil
//	true / false    bool                             bool
//	Integer         int, int64, *big.Int             int64 or *big.Int
//	Float           float64, float32                 float64
//	String          string                           string
//	Symbol          Symbol                           Symbol (symbolize_names)
//	Array           []any                            []any
//	Hash            *Map (ordered), map[...]any      *Map (insertion order)
//
// Parse returns mappings as an ordered [*Map] so key order is preserved (MRI
// keeps it); Generate accepts a [*Map], or a plain Go map (emitted in
// sorted-key order for determinism).
package json

import (
	"math/big"
	"sort"
)

// Value is the interface satisfied by every Ruby value this package handles. It
// is purely documentary — the public API uses any — but a host may use it to
// constrain its own adapters.
type Value = any

// Symbol is a Ruby Symbol (`:name`). Generate emits it as its bare name (a JSON
// string of the name); Parse yields Symbol keys when WithSymbolizeNames(true) is
// set (MRI's symbolize_names: true).
type Symbol string

// Pair is one entry of an ordered mapping.
type Pair struct {
	Key Value
	Val Value
}

// Map is an insertion-ordered Ruby Hash. Parse returns objects as *Map so key
// order round-trips; Generate accepts *Map, or a plain Go map (emitted in sorted
// key order).
//
// The identity index is built lazily: while a Map holds at most
// [mapIndexThreshold] pairs a linear scan resolves a key faster than a hash
// lookup and needs no map allocation, so the overwhelmingly common small object
// carries no per-object map. The index is materialised only once a Map grows
// past the threshold, restoring O(1) lookup for large hashes.
type Map struct {
	pairs []Pair
	index map[any]int // nil until len(pairs) exceeds mapIndexThreshold
}

// mapIndexThreshold is the pair count above which a Map switches from a linear
// key scan to a hash index. Below it the scan wins (no allocation, better cache
// behaviour); the crossover is where hashing amortises the map's cost.
const mapIndexThreshold = 16

// NewMap returns an empty ordered Map.
func NewMap() *Map { return &Map{} }

// Len reports the number of entries.
func (m *Map) Len() int { return len(m.pairs) }

// Pairs returns the entries in insertion order. The slice must not be mutated.
func (m *Map) Pairs() []Pair { return m.pairs }

// find locates key, returning its pair index and whether it is present. It uses
// the hash index when one exists, else a linear scan (keys are comparable — the
// same requirement the map imposed, so a non-comparable key panics identically).
func (m *Map) find(key Value) (int, bool) {
	if m.index != nil {
		i, ok := m.index[key]
		return i, ok
	}
	for i := range m.pairs {
		if m.pairs[i].Key == key {
			return i, true
		}
	}
	return 0, false
}

// Set inserts or replaces the entry for key. A later equal key replaces the
// earlier entry's value in place (MRI's last-wins on duplicate object keys),
// keeping the original position.
func (m *Map) Set(key, val Value) {
	if i, ok := m.find(key); ok {
		m.pairs[i].Val = val
		return
	}
	if m.index != nil {
		m.index[key] = len(m.pairs)
	}
	m.pairs = append(m.pairs, Pair{Key: key, Val: val})
	if m.index == nil && len(m.pairs) > mapIndexThreshold {
		m.buildIndex()
	}
}

// buildIndex materialises the hash index over the current (duplicate-free) pairs
// once the Map has grown past mapIndexThreshold.
func (m *Map) buildIndex() {
	m.index = make(map[any]int, len(m.pairs)*2)
	for i := range m.pairs {
		m.index[m.pairs[i].Key] = i
	}
}

// Get returns the value for key and whether it was present.
func (m *Map) Get(key Value) (Value, bool) {
	if i, ok := m.find(key); ok {
		return m.pairs[i].Val, true
	}
	return nil, false
}

// sortedMapKeys returns the keys of a plain Go map[string]any / map[Symbol]any
// in sorted order, so plain-map emission is deterministic.
func sortedStringKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sortedSymbolKeys is sortedStringKeys for a Symbol-keyed map.
func sortedSymbolKeys[V any](m map[Symbol]V) []Symbol {
	keys := make([]Symbol, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

// asBigInt is the canonical big-integer view of an integer Value; it is never
// called with a non-integer.
func asBigInt(v Value) *big.Int {
	switch n := v.(type) {
	case int:
		return big.NewInt(int64(n))
	case int64:
		return big.NewInt(n)
	case *big.Int:
		return n
	}
	return nil
}
