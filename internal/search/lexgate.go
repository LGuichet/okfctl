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

package search

import (
	"sort"

	"github.com/cwest/okfctl/internal/lexical"
	"github.com/cwest/okfctl/internal/okf"
)

// This file implements the term-wise lexical matcher behind --lexical-gate
// (cwest/okfctl#66). It exists because the two search surfaces never meet: the
// semantic query is embedding-only, and core `okfctl search --field body` matches
// the query as a PHRASE, by raw substring. The tokenizer, stopword set and light
// stemmer it matches with live in internal/lexical—the one analysis chain shared
// with core's BM25 ranking—so the gate, core search and hybrid fusion all agree
// on what a "term" is. The thin forwarders below keep this package's existing
// names (and their tests) unchanged.

// stem forwards to lexical.Stem.
func stem(tok string) string { return lexical.Stem(tok) }

// tokenize forwards to lexical.Tokenize.
func tokenize(text string) []string { return lexical.Tokenize(text) }

// LexTerms reduces a query string to its stemmed content terms (forwarding to
// lexical.Terms): it tokenizes, drops stopwords, stems each survivor,
// de-duplicates, and returns them sorted for determinism. An empty slice means
// the query carries NO content signal (an all-stopword query)—the gate reads
// that as "degrade to pure semantic".
func LexTerms(query string) []string { return lexical.Terms(query) }

// LexicalMatchSet returns the set of keys in texts whose text contains at least
// one of terms, matched TERM-WISE on stemmed tokens (not raw substring). texts
// maps an identity (a node path) to the prose to match against (title+body).
// terms are already stemmed (from LexTerms). A node matches when any stemmed
// query term equals any stemmed token of the node's text — so "hash" and
// "hashes" produce overlapping sets, and "agent" does not match "management"
// the way a raw substring would.
//
// The match set is computed against the LIVE bundle text at query time (the
// caller supplies texts), because the vector index carries no prose: contentHash
// keys on title+body only and a value denormalized onto the index would go stale.
func LexicalMatchSet(texts map[string]string, terms []string) map[string]bool {
	if len(terms) == 0 {
		return map[string]bool{}
	}
	want := make(map[string]bool, len(terms))
	for _, t := range terms {
		want[t] = true
	}
	out := map[string]bool{}
	for path, text := range texts {
		for _, tok := range tokenize(text) {
			if want[stem(tok)] {
				out[path] = true
				break
			}
		}
	}
	return out
}

// LexicalGateOptions configures --lexical-gate (cwest/okfctl#66). It is OFF by
// default (a nil *LexicalGateOptions on QueryOptions). When set, the semantic
// result list is GATED against a term-wise lexical match set and the lexical tail
// the semantic band missed is preserved — the two moves that make this a win on
// both embedders rather than a regression on the default one.
//
// The caller resolves Terms (LexTerms) and Match (LexicalMatchSet against the
// live bundle) and passes them in, so the engine stays free of any bundle
// dependency and the same match set is trivially testable in isolation.
type LexicalGateOptions struct {
	// Terms are the stemmed content terms of the query (from LexTerms). EMPTY
	// terms mean the query carried no lexical signal (all stopwords) — the gate
	// degrades to pure semantic (a no-op).
	Terms []string
	// Match is the set of node paths the lexical terms matched, resolved against
	// the live bundle (from LexicalMatchSet).
	Match map[string]bool
	// OverBroadFraction is the degrade threshold: when Match covers MORE than this
	// fraction of TotalNodes, the term carries no discriminating signal (e.g.
	// "agent" at 172/234 = 73% of the real corpus) and the gate degrades to pure
	// semantic. A value <= 0 disables the over-broad degrade.
	OverBroadFraction float64
	// TotalNodes is the bundle's node count, the denominator of the over-broad
	// check. Zero disables the over-broad degrade (no denominator to reason about).
	TotalNodes int
	// WideN is how many top semantic results form the "band" the lexical set is
	// intersected against. It is clamped up to at least k so the band is never
	// narrower than the requested result count. A wide band (default 50) keeps
	// lexical hits that are semantically mid-ranked in the intersection rather
	// than the tail.
	WideN int
}

// Lexical-gate tuning constants (cwest/okfctl#66), the single source of truth for
// BOTH search surfaces (the okfctl-search CLI and the /api/v1/search HTTP handler)
// so the two agree by construction rather than by duplicated literals. WideN is
// the semantic band width the lexical set is intersected against — comfortably
// above the default k so a lexically-relevant node ranked mid-band still lands in
// the intersection rather than the tail. OverBroadFraction is the degrade
// threshold: a term matching MORE than this fraction of the bundle carries no
// discriminating signal and makes the gate a no-op. Calibrated against the real
// corpus (234 nodes): the common question-word content terms match a small
// minority, while `agent` at 172/234 = 73% is the canonical over-broad case the
// issue names — 0.60 sits comfortably below 0.73 and above the discriminating
// terms, so it degrades the noise cases without disabling the useful ones.
const (
	LexicalGateWideN             = 50
	LexicalGateOverBroadFraction = 0.60
)

// BundleTexts maps each node's path to the prose the lexical gate matches
// against: its frontmatter title plus body — the SAME surface embedText feeds the
// vector index, so the lexical and semantic sides agree on what a node "says". A
// nil bundle yields an empty map (the gate then matches nothing and the engine
// degrades). This is the single builder both surfaces call so they cannot drift.
func BundleTexts(b *okf.Bundle) map[string]string {
	if b == nil {
		return nil
	}
	m := make(map[string]string, len(b.Nodes))
	for path, n := range b.Nodes {
		title, _ := n.Frontmatter["title"].(string)
		m[path] = title + " " + n.Body
	}
	return m
}

// BuildLexicalGate assembles the LexicalGateOptions for a --lexical-gate query
// over a live bundle: it reduces the query to its stemmed content terms, resolves
// the term-wise match set against the bundle prose, and wires the shared WideN /
// OverBroadFraction constants and the bundle's node count. Both search surfaces
// call THIS constructor, so a gated query cannot rank differently on the CLI than
// over HTTP for the same bundle. A nil bundle yields a gate that matches nothing
// and degrades to pure semantic (the safe no-op).
func BuildLexicalGate(b *okf.Bundle, query string) *LexicalGateOptions {
	terms := LexTerms(query)
	total := 0
	if b != nil {
		total = len(b.Nodes)
	}
	return &LexicalGateOptions{
		Terms:             terms,
		Match:             LexicalMatchSet(BundleTexts(b), terms),
		OverBroadFraction: LexicalGateOverBroadFraction,
		TotalNodes:        total,
		WideN:             LexicalGateWideN,
	}
}

// degrades reports whether the gate should behave as a pure no-op: no content
// terms, or a match set so broad it carries no signal. Both conditions are the
// documented degrade-to-semantic rule that keeps question-shaped queries exactly
// as good as they are with the gate off.
func (g *LexicalGateOptions) degrades() bool {
	if len(g.Terms) == 0 {
		return true
	}
	if g.OverBroadFraction > 0 && g.TotalNodes > 0 {
		if float64(len(g.Match)) > g.OverBroadFraction*float64(g.TotalNodes) {
			return true
		}
	}
	return false
}

// applyLexicalGate composes the gated result from a fully-ranked semantic list.
// ranked is the complete semantic ranking (best first). The composition, per
// cwest/okfctl#66:
//
//  1. Take the semantic BAND: the first WideN of ranked.
//  2. Emit the intersection (band ∩ Match) in SEMANTIC order.
//  3. APPEND the lexical hits the band missed, ordered by SEMANTIC SCORE
//     descending (ties broken by path for determinism), each carrying its own
//     semantic score from the full ranking. Score order — not path order —
//     because the caller cuts to k AFTER this composition (cwest/okfctl#73): a
//     path-ordered tail lets an alphabetic prefix decide which lexical hits
//     survive a small k, dropping stronger matches. Recall is unchanged (the
//     same set is preserved); only the order within the tail changes.
//  4. The caller cuts to k.
//
// When the gate degrades (empty terms or over-broad match) this returns ranked
// unchanged — the no-op path that makes the gate safe to leave available.
func applyLexicalGate(ranked []Result, g *LexicalGateOptions, k int) []Result {
	if g == nil || g.degrades() {
		return ranked
	}
	wide := g.WideN
	if wide < k {
		wide = k
	}
	if wide <= 0 || wide > len(ranked) {
		wide = len(ranked)
	}

	inBand := make(map[string]bool, wide)
	var intersection []Result
	for i := 0; i < wide; i++ {
		r := ranked[i]
		inBand[r.Path] = true
		if g.Match[r.Path] {
			intersection = append(intersection, r)
		}
	}

	// Lexical-only tail: matched nodes not already in the band, each carrying its
	// semantic score looked up from the full ranking. Ordered by score DESCENDING
	// (ties by path) so the caller's top-k cut keeps the STRONGEST lexical hits,
	// not an alphabetic prefix (cwest/okfctl#73).
	scoreByPath := make(map[string]Result, len(ranked))
	for _, r := range ranked {
		scoreByPath[r.Path] = r
	}
	var tail []Result
	for path := range g.Match {
		if inBand[path] {
			continue
		}
		r, ok := scoreByPath[path]
		if !ok {
			continue // matched a node absent from the index; nothing to rank
		}
		tail = append(tail, r)
	}
	sort.Slice(tail, func(i, j int) bool {
		if tail[i].Score != tail[j].Score {
			return tail[i].Score > tail[j].Score // higher score first
		}
		return tail[i].Path < tail[j].Path // deterministic tie-break
	})

	out := make([]Result, 0, len(intersection)+len(tail))
	out = append(out, intersection...)
	out = append(out, tail...)
	return out
}
