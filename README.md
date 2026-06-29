<p align="center"><img src="https://raw.githubusercontent.com/go-ruby-json/brand/main/social/go-ruby-json-json.png" alt="go-ruby-json/json" width="720"></p>

# json — go-ruby-json

[![Docs](https://img.shields.io/badge/docs-mkdocs--material-DC2626)](https://go-ruby-json.github.io/docs/)
[![License](https://img.shields.io/badge/license-BSD--3--Clause-blue)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.26.4%2B-00ADD8)](https://go.dev/dl/)
[![Coverage](https://img.shields.io/badge/coverage-100%25-1a7f37)](#tests--coverage)

**A pure-Go (no cgo) reimplementation of Ruby's [JSON](https://docs.ruby-lang.org/en/master/JSON.html)
parser and generator** — matching MRI 4.0.5's `JSON.parse`, `JSON.generate` and
`JSON.pretty_generate` **byte-for-byte**. It turns a JSON document into a tree of
Ruby values and renders such a tree back to the exact bytes MRI's `json` gem
produces — **without any Ruby runtime**.

It is the JSON backend for
[go-embedded-ruby](https://github.com/go-embedded-ruby/ruby), but is a
**standalone, reusable** module with no dependency on the Ruby runtime — a sibling
of [go-ruby-regexp](https://github.com/go-ruby-regexp/regexp) (the Onigmo engine),
[go-ruby-erb](https://github.com/go-ruby-erb/erb) (the ERB compiler) and
[go-ruby-yaml](https://github.com/go-ruby-yaml/yaml) (Psych).

> **Why not `encoding/json`?** Ruby's JSON semantics differ in ways that matter:
> insertion-ordered objects, big-integer parsing, `symbolize_names`, the exact
> `fpconv` float layout, the `JSON::ParserError` / `NestingError` /
> `GeneratorError` taxonomy with MRI-exact messages (down to the `at line L
> column C` suffix), NaN/Infinity handling, and `pretty_generate`'s precise
> spacing. This package ports MRI's behaviour directly rather than wrapping the
> standard library.

## Features

Faithful port of MRI's `JSON.parse` + `JSON.generate`, validated against the
`ruby` binary on every supported platform:

- **Full JSON grammar** — objects, arrays, strings (with `\u` escapes and
  surrogate pairs), numbers (integers, big integers, floats and exponents),
  `true` / `false` / `null`.
- **Insertion-ordered objects** — parsed objects come back as an ordered
  [`*Map`](#ruby-value-model) so key order round-trips (last value wins on a
  duplicate key, MRI-style).
- **`symbolize_names`** — object keys as `Symbol` instead of `string`.
- **MRI-exact errors** — the `JSON::ParserError` / `JSON::NestingError` /
  `JSON::GeneratorError` / `TypeError` taxonomy, with the same messages and
  `at line L column C` positions; a nesting limit (default 100) on both parse and
  generate.
- **`fpconv` float formatting** — the json gem's shortest-round-trip layout
  (`2.0`, `1e+15`, `0.0000001`, `5e-324`), not Go's or Ruby's `Float#to_s`.
- **MRI string escaping** — `\" \\ \b \f \n \r \t`, `\u00XX` for other control
  characters, `/` **not** escaped, UTF-8 passed through verbatim.
- **`pretty_generate`** — the exact two-space-indent layout, plus the individual
  `JSON::State` knobs (`indent` / `space` / `space_before` / `object_nl` /
  `array_nl`).
- **NaN / Infinity** — a `GeneratorError` / `ParserError` by default, accepted as
  the bare `NaN` / `Infinity` / `-Infinity` tokens under `allow_nan`.

CGO-free, dependency-free, **100% test coverage**, `gofmt` + `go vet` clean, and
green across the six 64-bit Go targets (amd64, arm64, riscv64, loong64, ppc64le,
s390x).

## Install

```sh
go get github.com/go-ruby-json/json
```

## Usage

```go
package main

import (
	"fmt"

	"github.com/go-ruby-json/json"
)

func main() {
	// Parse — objects come back as an ordered *json.Map.
	v, _ := json.Parse(`{"name":"web","ports":[80,443],"big":100000000000000000000}`)
	m := v.(*json.Map)
	ports, _ := m.Get("ports")
	fmt.Println(ports) // [80 443]

	// Generate — compact, MRI-byte-exact.
	out := json.NewMap()
	out.Set("checked", true)
	out.Set("ratio", 2.0)
	s, _ := json.Generate(out)
	fmt.Println(s) // {"checked":true,"ratio":2.0}

	// PrettyGenerate — JSON.pretty_generate layout.
	p, _ := json.PrettyGenerate([]any{int64(1), int64(2)})
	fmt.Println(p)
	// [
	//   1,
	//   2
	// ]

	// symbolize_names.
	sv, _ := json.Parse(`{"a":1}`, json.WithSymbolizeNames(true))
	fmt.Printf("%#v\n", sv.(*json.Map).Pairs()[0].Key) // "a" (json.Symbol)
}
```

## Ruby value model

A Ruby value is an `any` drawn from a small, fixed set of Go types, so a host can
map its own object graph to and from this package:

| Ruby             | Go (Generate accepts)              | Go (Parse returns)   |
| ---------------- | ---------------------------------- | -------------------- |
| `nil`            | `nil`                              | `nil`                |
| `true` / `false` | `bool`                             | `bool`               |
| `Integer`        | `int`, `int64`, `*big.Int`         | `int64` / `*big.Int` |
| `Float`          | `float64`, `float32`               | `float64`            |
| `String`         | `string`                           | `string`             |
| `Symbol`         | `json.Symbol`                      | `json.Symbol` (`symbolize_names`) |
| `Array`          | `[]any`                            | `[]any`              |
| `Hash`           | `*json.Map`, `map[string]any`, `map[Symbol]any` | `*json.Map` (ordered) |

A plain Go map is generated in sorted-key order; a `*json.Map` preserves
insertion order, and `Parse` always returns objects as `*json.Map`.

## API

```go
// Parse parses a JSON document into a tree of Ruby values (JSON.parse).
func Parse(s string, opts ...Option) (any, error)
func ParseBytes(b []byte, opts ...Option) (any, error)

// Generate renders a Ruby value to a compact JSON document (JSON.generate).
func Generate(v any, opts ...Option) (string, error)

// PrettyGenerate renders with the JSON.pretty_generate layout.
func PrettyGenerate(v any, opts ...Option) (string, error)

func WithSymbolizeNames(on bool) Option // symbolize_names:
func WithMaxNesting(n int) Option       // max_nesting: (0 / negative = unlimited)
func WithAllowNaN(on bool) Option       // allow_nan:
func WithIndent(s string) Option        // indent:
func WithSpace(s string) Option         // space:
func WithSpaceBefore(s string) Option   // space_before:
func WithObjectNL(s string) Option      // object_nl:
func WithArrayNL(s string) Option       // array_nl:

type Symbol string
type Map    struct { /* insertion-ordered Hash */ }
func NewMap() *Map
func (m *Map) Set(key, val any)
func (m *Map) Get(key any) (any, bool)
func (m *Map) Pairs() []Pair
func (m *Map) Len() int

// Error taxonomy (each carries its Ruby exception class via RubyClass()).
type Error interface { error; RubyClass() string }
type ParserError    struct{ Message string } // JSON::ParserError
type NestingError   struct{ Message string } // JSON::NestingError
type GeneratorError struct{ Message string } // JSON::GeneratorError
type TypeError      struct{ Message string } // TypeError
```

## Tests & coverage

The suite pairs deterministic, ruby-free tests (which alone hold coverage at
100%, so the qemu cross-arch and Windows lanes pass the gate) with a
**differential MRI oracle**: a wide corpus is generated/parsed here and checked
against the system `ruby` (`JSON.generate` / `JSON.parse` / `JSON.pretty_generate`
and the error messages). The oracle reads its input on `$stdin.binmode` and
writes on `$stdout.binmode` so Windows text-mode never mangles embedded newlines,
and **self-gates on MRI ≥ 4.0** (the JSON gem 4.0 is the reference), skipping
where `ruby` is absent or older.

```sh
COVERPKG=$(go list ./... | paste -sd, -)
go test -race -coverpkg="$COVERPKG" -coverprofile=cover.out ./...
go tool cover -func=cover.out | tail -1   # 100.0%
```

## License

BSD-3-Clause — see [LICENSE](LICENSE). Copyright the go-ruby-json/json authors.
