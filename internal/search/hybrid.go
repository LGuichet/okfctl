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
	"errors"
	"sort"

	"github.com/cwest/okfctl/internal/lexical"
	"github.com/cwest/okfctl/internal/okf"
)

// RRFK is the reciprocal-rank-fusion constant: a node at 1-based rank r in a
// list contributes 1/(RRFK+r). 60 is the literature default (Cormack, Clarke &
// Büttcher, 2009). Ranking is consumer-side—OKF v0.2 §1 leaves query
// infrastructure out of scope—so this is a tool choice, not a spec rule.
const RRFK = 60

// ErrHybridWithLexicalGate is returned when a caller asks for hybrid fusion and
// the lexical gate at once: both re-order the semantic list with lexical
// evidence, and stacking them has no defined meaning.
var ErrHybridWithLexicalGate = errors.New("hybrid search and the lexical gate cannot be combined")

// FuseRRF fuses a semantic ranking and a lexical ranking by reciprocal rank
// fusion with constant k: each node scores Σ 1/(k+rank) over the lists it
// appears in (ranks 1-based). Scores are not normalised—RRF reads only ranks.
// Ties on the fused score break by path ascending. The Snippet is carried from
// the semantic side; a lexical-only node has none.
func FuseRRF(semantic []Result, lex []lexical.Scored, k int) []Result {
	score := map[string]float64{}
	snippet := map[string]string{}
	var order []string
	add := func(path string, rank int) {
		if _, seen := score[path]; !seen {
			order = append(order, path)
		}
		score[path] += 1.0 / float64(k+rank)
	}
	for i, r := range semantic {
		add(r.Path, i+1)
		snippet[r.Path] = r.Snippet
	}
	for i, s := range lex {
		add(s.Path, i+1)
	}
	out := make([]Result, 0, len(order))
	for _, p := range order {
		out = append(out, Result{Path: p, Score: score[p], Snippet: snippet[p]})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// HybridQuery ranks the bundle's concept nodes twice—semantically with
// QueryWith (filters pre-rank, recency decay and MinRelevance unchanged) and
// lexically with BM25F over the same filtered candidates—then fuses the two
// lists with FuseRRF and cuts to k (k <= 0 returns everything).
//
// The lexical side reads only §4.1 title/tags/type and the §4.2 body;
// okf.LexicalDocuments never returns the §3.1 reserved files. Filtering happens
// before BM25 runs, so its corpus statistics cover the candidate set.
func HybridQuery(s *Store, e Embedder, b *okf.Bundle, q string, k int, opts QueryOptions) ([]Result, error) {
	if opts.LexicalGate != nil {
		return nil, ErrHybridWithLexicalGate
	}
	if b == nil {
		return nil, errors.New("hybrid search needs the loaded bundle")
	}
	semantic, err := QueryWith(s, e, q, 0, opts)
	if err != nil {
		return nil, err
	}

	docs := okf.LexicalDocuments(b)
	if !opts.Filter.IsEmpty() {
		kept := docs[:0:0]
		for _, d := range docs {
			m, ok := opts.Meta[d.Path]
			if opts.Filter.keep(d.Path, m, ok) {
				kept = append(kept, d)
			}
		}
		docs = kept
	}
	lex := lexical.Rank(docs, q, lexical.DefaultWeights)

	fused := FuseRRF(semantic, lex, RRFK)
	if k > 0 && len(fused) > k {
		fused = fused[:k]
	}
	return fused, nil
}
