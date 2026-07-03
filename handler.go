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
	// BeginArray opens an array; n is a capacity hint for pre-allocation, or 0
	// when the count is not known up front (the default parser passes 0 and sizes
	// each container exactly at EndArray instead of pre-scanning for the count). A
	// Builder that pre-sizes must tolerate n == 0 by growing on demand. The
	// emitted values up to the matching EndArray are its elements.
	BeginArray(n int)
	// EndArray closes the array opened by the matching BeginArray.
	EndArray()
	// BeginObject opens an object; n is a capacity hint, or 0 when unknown (see
	// BeginArray). It is followed by (Key, value) sequences up to EndObject.
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
//
// Container storage is drawn from per-parse *arenas* (slabs) rather than a fresh
// heap allocation per container: the [Map] structs, their `[]Pair` backing and
// the `[]any` array backing are each carved as sub-slices / sub-elements of a
// large shared slab, bump-allocated as the parse walks the document. Because a
// container's storage survives inside the returned tree, the slab it lives in
// stays reachable through that tree; the builder itself is discarded after the
// parse. This collapses the three dominant per-container allocations (a *Map, its
// pair slice, an array's element slice) into a handful of slab allocations for
// the whole document, with byte-identical output.
type valueBuilder struct {
	stack []frame
	root  Value
	done  bool
	// Key cache: the boxed [Value] for each distinct object key seen so far, so a
	// key that recurs across sibling records (the norm for arrays of objects) is
	// boxed into an interface once rather than on every occurrence. MRI likewise
	// deduplicates hash keys (frozen fstrings); the parsed structure is unchanged —
	// only the interface header is shared.
	//
	// A document usually has a small set of distinct keys, so the cache is a flat
	// slice scanned linearly: for a handful of short keys a couple of string
	// compares beat hashing the key on every occurrence. Every distinct key also
	// gets a dense id (its index here) used for O(1) duplicate-key detection while
	// an object is built. Once the distinct-key count crosses keyCacheMax a
	// string→id index map is added to keep resolution O(1) for a key-heavy
	// document (the cache slice is retained so an id still maps back to its Value).
	keyCache []keyEnt
	keyIndex map[string]int32

	// Bump arenas. Each *Cur is the next free index in its slab; when a request
	// would overflow the slab a fresh, larger-if-needed slab is allocated and the
	// old one is kept alive by whatever tree nodes already reference it.
	// Reservations carry the *exact* final length (discovered at container close),
	// so a region is never over-reserved and no reservation overlaps another.
	mapSlab  []Map
	mapCur   int
	pairSlab []Pair
	pairCur  int
	elemSlab []any
	elemCur  int

	// Materialisation scratch. A container's members are appended here in
	// document order while it is open; at close the exact-length run is copied
	// into the arena and the scratch truncated back. One shared stack per member
	// kind (array elements → vscratch, object pairs → pscratch) suffices because
	// containers close depth-first, so a run is always a contiguous tail. Both
	// grow once to the document's peak width and are reused for every container,
	// which is what lets the parser skip the per-container element pre-scan
	// entirely (the exact size is known at close with no look-ahead).
	vscratch []any
	pscratch []Pair
}

// Arena slab sizes: the fallback growth chunk when a document outgrows its
// length-seeded slab (or was never seeded). A request larger than a chunk gets
// its own exact-sized slab.
const (
	mapSlabChunk  = 64
	pairSlabChunk = 256
	elemSlabChunk = 256
)

// seed right-sizes the arenas from the document length so a typical document
// draws each storage kind from a single slab with little waste, while a
// pathologically skewed document is capped (and simply grows past the seed).
// The divisors are conservative lower bounds on bytes-per-node for JSON; the cap
// bounds the up-front reservation for a huge document (growth handles the rest).
func (b *valueBuilder) seed(docLen int) {
	const cap = 1 << 13
	est := func(divisor int) int {
		n := docLen / divisor
		if n < 8 {
			n = 8
		}
		if n > cap {
			n = cap
		}
		return n
	}
	b.pairSlab = make([]Pair, est(12))
	b.elemSlab = make([]any, est(14))
	b.mapSlab = make([]Map, est(40))
	// The container stack depth and the scratch peak width are bounded by the
	// document but rarely large; a small fixed reservation avoids the handful of
	// regrowth copies a from-empty append would otherwise pay on every parse.
	b.stack = make([]frame, 0, 16)
	b.vscratch = make([]any, 0, 64)
	b.pscratch = make([]Pair, 0, 16)
}

// newMap hands out the next *Map from the map arena (no per-object allocation).
func (b *valueBuilder) newMap() *Map {
	if b.mapCur >= len(b.mapSlab) {
		b.mapSlab = make([]Map, mapSlabChunk)
		b.mapCur = 0
	}
	m := &b.mapSlab[b.mapCur]
	b.mapCur++
	return m
}

// reservePairs carves a len-0 cap-n `[]Pair` from the pair arena for an object of
// n pairs. Set appends into it up to cap n with no allocation; should the hint
// undershoot, append transparently reallocates that one slice off-arena (correct,
// just unpooled). Reserving the full cap up front keeps a nested object's region
// from overlapping its parent's.
func (b *valueBuilder) reservePairs(n int) []Pair {
	if n <= 0 {
		n = 1
	}
	if b.pairCur+n > len(b.pairSlab) {
		size := pairSlabChunk
		if n > size {
			size = n
		}
		b.pairSlab = make([]Pair, size)
		b.pairCur = 0
	}
	s := b.pairSlab[b.pairCur : b.pairCur : b.pairCur+n]
	b.pairCur += n
	return s
}

// reserveElems carves a len-0 cap-n `[]any` from the element arena for an array
// of n elements, with the same bump/overflow discipline as reservePairs.
func (b *valueBuilder) reserveElems(n int) []any {
	if n <= 0 {
		n = 1
	}
	if b.elemCur+n > len(b.elemSlab) {
		size := elemSlabChunk
		if n > size {
			size = n
		}
		b.elemSlab = make([]any, size)
		b.elemCur = 0
	}
	s := b.elemSlab[b.elemCur : b.elemCur : b.elemCur+n]
	b.elemCur += n
	return s
}

// frame is one open container on the builder stack. start is the index of the
// container's first member in the relevant scratch stack (vscratch for an array,
// pscratch for an object); isObj selects which; key/keyID hold an object's
// pending key (its Value and dense id) between Key and the value emit that
// consumes it. For an object, seen is a bitmask of the key ids emitted so far
// (ids < 64) and dup records whether any key has repeated — set in O(1) as each
// pair is emitted, so EndObject skips the duplicate-key scan for the common
// distinct-key object.
type frame struct {
	start int
	isObj bool
	dup   bool
	keyID int
	key   Value
	seen  uint64
}

// emit attaches v to the innermost open container's scratch run, or records it as
// the result at the top level. An object member becomes a Pair with the pending
// key; an array member is the value itself.
func (b *valueBuilder) emit(v Value) {
	if n := len(b.stack); n > 0 {
		f := &b.stack[n-1]
		if f.isObj {
			// Track duplicate keys in O(1): a key id already present in seen means
			// this object repeats a key (last-wins de-dup deferred to EndObject). An
			// id past the bitmask width (a huge-key document) conservatively forces
			// the scan path.
			if uint(f.keyID) < 64 {
				bit := uint64(1) << uint(f.keyID)
				if f.seen&bit != 0 {
					f.dup = true
				}
				f.seen |= bit
			} else {
				f.dup = true
			}
			b.pscratch = append(b.pscratch, Pair{Key: f.key, Val: v})
			return
		}
		b.vscratch = append(b.vscratch, v)
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

// BeginArray opens an array scratch run at the current vscratch tail. The n hint
// is ignored: the exact element count is known at EndArray, so no pre-sizing (and
// no per-container element pre-scan) is needed.
func (b *valueBuilder) BeginArray(int) {
	b.stack = append(b.stack, frame{start: len(b.vscratch)})
}

// EndArray copies the array's scratch run into the element arena as an
// exact-length []any, truncates the scratch, and emits the array.
func (b *valueBuilder) EndArray() {
	top := len(b.stack) - 1
	start := b.stack[top].start
	b.stack = b.stack[:top]
	run := b.vscratch[start:]
	out := b.reserveElems(len(run))
	out = append(out, run...)
	b.vscratch = b.vscratch[:start]
	b.emit(out)
}

// BeginObject opens an object scratch run at the current pscratch tail. The n
// hint is ignored (see BeginArray).
func (b *valueBuilder) BeginObject(int) {
	b.stack = append(b.stack, frame{start: len(b.pscratch), isObj: true})
}

// keyEnt is one entry of the linear key cache: the raw key text and its boxed,
// canonical Value.
type keyEnt struct {
	s string
	v Value
}

// keyCacheMax is the distinct-key count at which the linear key cache migrates to
// a hash map. Below it a linear scan of short keys is cheaper than hashing every
// key occurrence; above it hashing wins.
const keyCacheMax = 32

func (b *valueBuilder) Key(s string, symbolize bool) {
	f := &b.stack[len(b.stack)-1]
	if b.keyIndex != nil {
		if id, ok := b.keyIndex[s]; ok {
			f.key = b.keyCache[id].v
			f.keyID = int(id)
			return
		}
	} else {
		for i := range b.keyCache {
			if b.keyCache[i].s == s {
				f.key = b.keyCache[i].v
				f.keyID = i
				return
			}
		}
	}
	var k Value
	if symbolize {
		k = Symbol(s)
	} else {
		k = s
	}
	id := len(b.keyCache)
	b.keyCache = append(b.keyCache, keyEnt{s: s, v: k})
	if b.keyIndex != nil {
		b.keyIndex[s] = int32(id)
	} else if id+1 > keyCacheMax {
		// Add a string→id index for O(1) lookup on a key-heavy document; the cache
		// slice stays so an id still resolves to its Value.
		b.keyIndex = make(map[string]int32, (id+1)*2)
		for i := range b.keyCache {
			b.keyIndex[b.keyCache[i].s] = int32(i)
		}
	}
	f.key = k
	f.keyID = id
}

// EndObject builds the object's *Map from its scratch run — de-duplicating keys
// last-wins (MRI semantics), copying the surviving pairs into the pair arena, and
// truncating the scratch — then emits it.
func (b *valueBuilder) EndObject() {
	top := len(b.stack) - 1
	f := b.stack[top]
	b.stack = b.stack[:top]
	run := b.pscratch[f.start:]
	m := b.buildMap(run, f.dup)
	b.pscratch = b.pscratch[:f.start]
	b.emit(m)
}

// buildMap materialises a *Map from a run of scratch pairs in document order. The
// caller passes hasDup, computed in O(1) as the pairs were emitted: when it is
// false — the overwhelming case, a distinct-key object — the run is copied into
// the pair arena in one shot and the Map's key index stays lazy (built on first
// lookup, matching the Map's own small-object policy). Only when a key actually
// repeats does it take the last-wins de-duplication path (a later equal key
// overwrites the earlier pair's value in place, keeping the earlier position),
// using a hash index for a large object so de-duplication stays O(n).
func (b *valueBuilder) buildMap(run []Pair, hasDup bool) *Map {
	m := b.newMap()
	out := b.reservePairs(len(run))
	if !hasDup {
		out = append(out, run...)
		m.pairs = out
		// A large object still gets a hash index so post-parse key lookups stay
		// O(1), matching the index a Map builds for itself once it grows past the
		// threshold (a small object keeps the cheaper linear scan).
		if len(out) > mapIndexThreshold {
			m.buildIndex()
		}
		return m
	}
	if len(run) > mapIndexThreshold {
		idx := make(map[any]int, len(run)*2)
		for _, pr := range run {
			if j, ok := idx[pr.Key]; ok {
				out[j].Val = pr.Val
				continue
			}
			idx[pr.Key] = len(out)
			out = append(out, pr)
		}
		m.pairs = out
		m.index = idx
		return m
	}
	for _, pr := range run {
		dup := false
		for j := range out {
			if out[j].Key == pr.Key {
				out[j].Val = pr.Val
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, pr)
		}
	}
	m.pairs = out
	return m
}

func (b *valueBuilder) Result() Value { return b.root }
