// Copyright (c) the go-ruby-json/json authors
//
// SPDX-License-Identifier: BSD-3-Clause

package json

import (
	"embed"
	"sort"
	"strings"
	"testing"
)

// jsonTestSuite is the canonical cross-implementation JSON parsing corpus
// nst/JSONTestSuite (github.com/nst/JSONTestSuite, test_parsing/). Each file is
// named y_* (a conformant parser MUST accept), n_* (MUST reject), or i_* (the
// RFC leaves the behaviour implementation-defined). The files are vendored
// verbatim — several carry deliberately malformed bytes, BOMs and invalid UTF-8
// — so this package gates on the reference's own accept/reject suite.
//
//go:embed jsontestsuite
var jsonTestSuite embed.FS

// jsonSuiteKnownFailing is the frozen set of JSONTestSuite files whose required
// accept/reject verdict this package does not match, because it faithfully
// reproduces the MRI JSON 4.0 gem rather than RFC 8259's strictest reading (the
// gem is laxer in a few documented places). It is a shrink-only conformance
// RATCHET: every y_/n_ file NOT listed here must get the required verdict, so no
// change may introduce a new divergence, and a listed file that starts matching
// is reported so the entry can be removed. i_* files are implementation-defined
// and never gated — only recorded. Baseline captured 2026-08-03.
//
// The gem accepts (MRI does too) some inputs the RFC-strict suite marks n_:
// leading/embedded/BOM whitespace variants and a few lexical edge cases that
// MRI's scanner tolerates. Each entry is a place where MRI itself diverges from
// the suite, not a parser bug; closing any of them would mean diverging from MRI.
var jsonSuiteKnownFailing = map[string]bool{}

// TestJSONTestSuiteConformance is the differential accept/reject gate against the
// canonical nst/JSONTestSuite corpus. Every y_ file outside the knownFailing set
// must parse without error and every n_ file must error; a new divergence fails
// CI and a listed file that now matches is reported so the ratchet can tighten.
// i_ files are recorded for visibility but never gated.
func TestJSONTestSuiteConformance(t *testing.T) {
	entries, err := jsonTestSuite.ReadDir("jsontestsuite")
	if err != nil {
		t.Fatalf("read corpus dir: %v", err)
	}
	if len(entries) < 300 {
		t.Fatalf("expected ~318 JSONTestSuite files, found %d", len(entries))
	}
	var yPass, yTot, nPass, nTot, iAccept, iTot int
	var newFail, fixed []string
	for _, e := range entries {
		name := e.Name()
		data, err := jsonTestSuite.ReadFile("jsontestsuite/" + name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		_, perr := ParseBytes(data)
		accepted := perr == nil
		switch {
		case strings.HasPrefix(name, "y_"):
			yTot++
			ok := accepted // y_ must be accepted
			if ok {
				yPass++
			}
			recordRatchet(name, ok, jsonSuiteKnownFailing, &newFail, &fixed)
		case strings.HasPrefix(name, "n_"):
			nTot++
			ok := !accepted // n_ must be rejected
			if ok {
				nPass++
			}
			recordRatchet(name, ok, jsonSuiteKnownFailing, &newFail, &fixed)
		case strings.HasPrefix(name, "i_"):
			iTot++
			if accepted {
				iAccept++
			}
		}
	}
	t.Logf("JSONTestSuite: y_ %d/%d accepted, n_ %d/%d rejected (required verdict "+
		"%d/%d = %.2f%%); i_ %d/%d accepted (implementation-defined, not gated); "+
		"%d known gaps", yPass, yTot, nPass, nTot, yPass+nPass, yTot+nTot,
		100*float64(yPass+nPass)/float64(yTot+nTot), iAccept, iTot, len(jsonSuiteKnownFailing))
	if len(fixed) > 0 {
		sort.Strings(fixed)
		t.Errorf("files now getting the required verdict that are still listed in "+
			"jsonSuiteKnownFailing: %v\nremove them to tighten the ratchet", fixed)
	}
	if len(newFail) > 0 {
		sort.Strings(newFail)
		t.Errorf("REGRESSION: %d JSONTestSuite file(s) with the wrong verdict: %v",
			len(newFail), newFail)
	}
}

// recordRatchet folds one gated file's result into the shrink-only bookkeeping.
func recordRatchet(name string, ok bool, known map[string]bool, newFail, fixed *[]string) {
	switch {
	case ok && known[name]:
		*fixed = append(*fixed, name)
	case !ok && !known[name]:
		*newFail = append(*newFail, name)
	}
}
