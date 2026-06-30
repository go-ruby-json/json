// Copyright (c) the go-ruby-json/json authors
//
// SPDX-License-Identifier: BSD-3-Clause

package json

// Option configures Parse, Generate and PrettyGenerate, mirroring the keyword
// options of MRI's JSON.parse / JSON.generate / JSON.pretty_generate. An option
// that does not apply to a given call is simply ignored (MRI tolerates the same).
type Option func(*config)

// config is the resolved configuration for one call.
type config struct {
	// parse options
	symbolizeNames bool
	maxNesting     int  // 0 means "use the default"; <0 means unlimited
	maxNestingSet  bool // whether the caller pinned maxNesting (so 0 = unlimited)
	allowNaN       bool

	// generate state (JSON::State equivalents)
	indent   string
	space    string
	spaceB   string // space_before (before the ':')
	objectNL string
	arrayNL  string
}

// resolve builds a config from defaults plus opts. The defaults match MRI's
// JSON.generate (compact: empty indent/newlines) and JSON.parse (max_nesting
// 100, symbolize_names false, allow_nan false).
func resolve(opts []Option) config {
	c := config{
		maxNesting: defaultMaxNesting,
		space:      "",
	}
	for _, opt := range opts {
		opt(&c)
	}
	return c
}

// defaultMaxNesting is MRI's default JSON.parse / JSON.generate nesting limit.
const defaultMaxNesting = 100

// nestingLimit reports c's active nesting limit, or -1 when the caller pinned an
// unlimited limit (WithMaxNesting(0) / a negative value). A config that was never
// pinned keeps the defaultMaxNesting set by resolve.
func nestingLimit(c *config) int {
	if c.maxNestingSet && c.maxNesting <= 0 {
		return -1
	}
	return c.maxNesting
}

// WithSymbolizeNames makes Parse return object keys as [Symbol] instead of
// string (MRI's symbolize_names: true). It has no effect on Generate.
func WithSymbolizeNames(on bool) Option {
	return func(c *config) { c.symbolizeNames = on }
}

// WithMaxNesting sets the maximum structure depth for Parse and Generate (MRI's
// max_nesting:). A value of 0 (or negative) disables the limit, matching MRI's
// max_nesting: false / 0.
func WithMaxNesting(n int) Option {
	return func(c *config) {
		c.maxNesting = n
		c.maxNestingSet = true
	}
}

// WithAllowNaN permits the non-finite floats NaN, Infinity and -Infinity in both
// directions (MRI's allow_nan:). On Generate they emit the bare NaN / Infinity /
// -Infinity tokens; on Parse those bare tokens are accepted. Without it, a
// non-finite float is a GeneratorError and the tokens are a ParserError.
func WithAllowNaN(on bool) Option {
	return func(c *config) { c.allowNaN = on }
}

// WithIndent sets the per-level indent string used by generation (MRI's
// indent:). PrettyGenerate defaults it to two spaces.
func WithIndent(s string) Option {
	return func(c *config) { c.indent = s }
}

// WithSpace sets the string emitted after the ':' separating an object key from
// its value (MRI's space:). PrettyGenerate defaults it to a single space.
func WithSpace(s string) Option {
	return func(c *config) { c.space = s }
}

// WithSpaceBefore sets the string emitted before the ':' in an object pair
// (MRI's space_before:). Default empty.
func WithSpaceBefore(s string) Option {
	return func(c *config) { c.spaceB = s }
}

// WithObjectNL sets the newline string emitted after each object delimiter and
// between pairs (MRI's object_nl:). PrettyGenerate defaults it to "\n".
func WithObjectNL(s string) Option {
	return func(c *config) { c.objectNL = s }
}

// WithArrayNL sets the newline string emitted after each array delimiter and
// between elements (MRI's array_nl:). PrettyGenerate defaults it to "\n".
func WithArrayNL(s string) Option {
	return func(c *config) { c.arrayNL = s }
}

// Parse parses a JSON document into a tree of Ruby values, matching JSON.parse.
// Objects come back as an ordered [*Map] (key order preserved), arrays as []any,
// numbers as int64 / *big.Int / float64, and strings as Go strings (with
// WithSymbolizeNames, object keys come back as [Symbol]). A malformed document
// returns a [*ParserError]; exceeding the nesting limit a [*NestingError]; a
// non-string input a [*TypeError].
func Parse(s string, opts ...Option) (Value, error) {
	c := resolve(opts)
	return parse(s, &c)
}

// ParseBytes is Parse for a byte slice (MRI accepts a String either way).
func ParseBytes(b []byte, opts ...Option) (Value, error) {
	return Parse(string(b), opts...)
}

// ParseInto parses a JSON document straight into the host [Builder] dst,
// reproducing JSON.parse semantics (and its errors) but with no intermediate
// tree of this package's values — the parser drives dst as it reads, so a host
// such as go-embedded-ruby materialises its own object graph in a single
// allocation-light pass. On success dst.Result() holds the top-level value; on a
// malformed document, nesting overflow or non-string input the matching error is
// returned and dst's result is unspecified.
func ParseInto(s string, dst Builder, opts ...Option) error {
	c := resolve(opts)
	return parseInto(s, dst, &c)
}

// Generate renders a Ruby value to a compact JSON document, matching
// JSON.generate / Object#to_json. The value is drawn from the package value
// model; a non-finite float without WithAllowNaN returns a [*GeneratorError], and
// over-deep nesting a [*NestingError].
func Generate(v Value, opts ...Option) (string, error) {
	c := resolve(opts)
	return generate(v, &c)
}

// PrettyGenerate renders a Ruby value with MRI's JSON.pretty_generate layout:
// two-space indent, "\n" object/array newlines and a single space after ':'. Any
// WithIndent / WithSpace / WithObjectNL / WithArrayNL options override those
// defaults.
func PrettyGenerate(v Value, opts ...Option) (string, error) {
	pretty := []Option{
		WithIndent("  "),
		WithSpace(" "),
		WithObjectNL("\n"),
		WithArrayNL("\n"),
	}
	c := resolve(append(pretty, opts...))
	return generate(v, &c)
}
