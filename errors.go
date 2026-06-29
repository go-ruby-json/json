// Copyright (c) the go-ruby-json/json authors
//
// SPDX-License-Identifier: BSD-3-Clause

package json

// Error is the base of this package's error taxonomy, mirroring MRI's
// JSON::JSONError hierarchy. Every error returned by Parse, Generate and
// PrettyGenerate is one of [*ParserError], [*NestingError], [*GeneratorError] or
// [*TypeError]; all satisfy Error. RubyClass reports the matching Ruby exception
// class name so a host (go-embedded-ruby) can raise the right Ruby exception.
type Error interface {
	error
	// RubyClass is the fully-qualified Ruby exception class this error maps to
	// (e.g. "JSON::ParserError").
	RubyClass() string
}

// ParserError is a malformed-document error: MRI's JSON::ParserError. Its
// message matches MRI's, including the "at line L column C" suffix.
type ParserError struct{ Message string }

func (e *ParserError) Error() string     { return e.Message }
func (e *ParserError) RubyClass() string { return "JSON::ParserError" }

// NestingError is raised when nesting exceeds the limit, on both parse and
// generate: MRI's JSON::NestingError (a subclass of JSON::ParserError in MRI; we
// model it as its own type and report it as the more specific class).
type NestingError struct{ Message string }

func (e *NestingError) Error() string     { return e.Message }
func (e *NestingError) RubyClass() string { return "JSON::NestingError" }

// GeneratorError is raised when a value cannot be generated — a non-finite float
// without allow_nan: MRI's JSON::GeneratorError.
type GeneratorError struct{ Message string }

func (e *GeneratorError) Error() string     { return e.Message }
func (e *GeneratorError) RubyClass() string { return "JSON::GeneratorError" }

// TypeError mirrors MRI raising a plain TypeError when Parse is handed a
// non-String (e.g. JSON.parse(123)).
type TypeError struct{ Message string }

func (e *TypeError) Error() string     { return e.Message }
func (e *TypeError) RubyClass() string { return "TypeError" }
