// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package lexical is okfctl's one text-analysis chain: the tokenizer, stopword
// set and light stemmer shared by core `okfctl search` (BM25 ranking) and the
// okfctl-search plugin (`--lexical-gate`, hybrid fusion). It depends on nothing
// but the standard library, so core can import it without depending on the
// plugin and the plugin can import it without a cycle through internal/okf.
//
// The chain was introduced for --lexical-gate (cwest/okfctl#66), where two
// reproduced failures on the real corpus drove every choice:
//
//  1. Phrase-wise body match on a question ("how should an agent decide when to
//     delegate work") returns 0 hits—so a query is reduced to its CONTENT TERMS
//     (stopwords dropped) and matched term-wise, never as a phrase.
//  2. Raw-substring match is asymmetric: `hash` matches 18 nodes, `hashes` 0;
//     `agent` 172, `agents` 100—so terms are NORMALIZED (stemmed) so a term and
//     its inflection collapse to one form.
//
// okfctl is a spec consumer, not an NLP toolkit, so the transforms are the
// lightest that fix the proven failures: a fixed English stopword set and a
// suffix stripper, not a full Porter stemmer (whose aggressive rewrites would
// over-collapse distinct identifiers).
package lexical

import (
	"sort"
	"strings"
	"unicode"
)

// stopwords is a small closed set of English function words plus the
// question-shaped leaders proven necessary by the 0-hit reproduction. Content
// words (nouns, verbs like "delegate", domain terms like "agent") are NOT here—
// removing them would defeat term matching. Kept deliberately short: an
// over-broad stopword list silently drops real query signal.
var stopwords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true,
	"be": true, "but": true, "by": true, "can": true, "did": true, "do": true,
	"does": true, "for": true, "from": true, "how": true, "i": true, "if": true,
	"in": true, "into": true, "is": true, "it": true, "its": true, "me": true,
	"my": true, "no": true, "not": true, "of": true, "on": true, "or": true,
	"our": true, "should": true, "so": true, "than": true, "that": true,
	"the": true, "their": true, "them": true, "then": true, "there": true,
	"these": true, "they": true, "this": true, "to": true, "up": true,
	"was": true, "we": true, "were": true, "what": true, "when": true,
	"where": true, "which": true, "who": true, "why": true, "will": true,
	"with": true, "would": true, "you": true, "your": true,
}

// IsStopword reports whether tok (already lowercased) is in the closed stopword
// set.
func IsStopword(tok string) bool { return stopwords[tok] }

// MinStemLen is the shortest a token may be REDUCED to by suffix stripping. A
// suffix is only removed if at least this many runes remain, so short tokens
// ("is", "os", "ci", "go") and short identifiers survive intact rather than
// being mangled to a single letter.
const MinStemLen = 3

// Stem applies a deliberately light suffix strip so a term and its common
// inflections collapse to one form: it removes at most ONE of the ordered
// suffixes {ing, ed, es, s, e} and only when the remaining stem is >= MinStemLen
// runes. Ordered longest-first so "hashes" -> "hash" (via es), not "hashe".
// This is the fix for the hash/hashes and agent/agents asymmetry; it is NOT a
// linguistic stemmer and makes no claim beyond collapsing those inflections.
func Stem(tok string) string {
	// Order matters: try longer, more specific suffixes before shorter ones.
	for _, suf := range []string{"ing", "ed", "es", "s", "e"} {
		if len(tok) > len(suf) && strings.HasSuffix(tok, suf) {
			if trimmed := tok[:len(tok)-len(suf)]; len([]rune(trimmed)) >= MinStemLen {
				return trimmed
			}
		}
	}
	return tok
}

// Tokenize lowercases text and splits it into alphanumeric tokens, discarding
// every non-alphanumeric rune as a boundary. This is the ONE tokenizer used for
// both the query and node text, so the query side and the match side always
// share a coordinate space.
func Tokenize(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// Analyze runs the full document-side chain over text: tokenize, drop
// stopwords, stem. Unlike Terms it KEEPS duplicates and source order, because a
// ranking function needs term frequency and field length.
func Analyze(text string) []string {
	toks := Tokenize(text)
	out := toks[:0]
	for _, tok := range toks {
		if stopwords[tok] {
			continue
		}
		out = append(out, Stem(tok))
	}
	return out
}

// Terms reduces a query string to its stemmed content terms: it tokenizes,
// drops stopwords, stems each survivor, de-duplicates, and returns them sorted
// for determinism. An empty slice means the query carries NO content signal (an
// all-stopword query).
func Terms(query string) []string {
	seen := map[string]bool{}
	var out []string
	for _, tok := range Tokenize(query) {
		if stopwords[tok] {
			continue
		}
		s := Stem(tok)
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
