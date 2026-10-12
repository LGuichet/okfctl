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

package lexical

import (
	"math"
	"sort"
	"strings"
)

// BM25 parameters. These are the conventional defaults from the literature
// (Robertson & Zaragoza, "The Probabilistic Relevance Framework: BM25 and
// Beyond"), kept as named constants so a reader never meets a bare 1.2 or 0.75.
const (
	// K1 controls term-frequency saturation: the 2nd occurrence of a term adds
	// less than the 1st, and the score approaches a ceiling as tf grows.
	K1 = 1.2
	// B controls length normalisation: 0 ignores field length, 1 normalises
	// fully against the field's average length.
	B = 0.75
)

// Weights are the BM25F per-field boosts applied to a field's (length-normalised)
// term frequency BEFORE saturation. A zero weight removes the field from both
// scoring and document frequency, which is how a --field restriction is
// expressed.
type Weights struct {
	Title float64
	Tags  float64
	Type  float64
	Body  float64
}

// DefaultWeights orders the fields title > tags > type > body. The values are
// justified against the real corpus in docs/specs/2026-10-12-search-bm25.md:
// a title names what a node IS about (one short field, chosen by the author), a
// tag is a deliberate cross-cutting label, a type is a coarse routing class
// shared by many nodes (OKF §4.1), and the body mentions many things in passing.
var DefaultWeights = Weights{Title: 3, Tags: 2, Type: 1.5, Body: 1}

// Document is one rankable unit: a concept node's lexical surfaces. Path is its
// identity (bundle-relative, slash-separated) and the deterministic tie-break.
type Document struct {
	Path  string
	Title string
	Tags  []string
	Type  string
	Body  string
}

// Scored is one ranked hit.
type Scored struct {
	Path  string  `json:"path"`
	Score float64 `json:"score"`
}

// field indexes the four surfaces in a fixed order so every float accumulation
// happens in the same sequence on every run (determinism, not just ranking).
const (
	fTitle = iota
	fTags
	fType
	fBody
	numFields
)

type docStats struct {
	path string
	tf   [numFields]map[string]int
	len  [numFields]int
}

// Index holds the per-document, per-field term statistics of a document set,
// computed once so several queries can be scored against it. It is built in
// memory from the loaded bundle on each run; there is no index file.
type Index struct {
	w    [numFields]float64
	docs []docStats // sorted by path
	avg  [numFields]float64
	df   map[string]int
}

// NewIndex analyses docs (tokenize, drop stopwords, stem—the same chain as
// Terms, so query and document share one coordinate space) and computes field
// lengths, average field lengths and per-TOKEN document frequency. DF counts a
// document once per distinct stemmed token present in any field whose weight
// is non-zero. Counting on tokens, never on raw-substring containment, is what
// keeps short terms like `ci` or `id` from being "found" inside `decision` or
// `identifier` and having their IDF driven to zero.
func NewIndex(docs []Document, w Weights) *Index {
	ix := &Index{
		w:  [numFields]float64{w.Title, w.Tags, w.Type, w.Body},
		df: map[string]int{},
	}
	sorted := make([]Document, len(docs))
	copy(sorted, docs)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

	var total [numFields]int
	for _, d := range sorted {
		ds := docStats{path: d.Path}
		texts := [numFields]string{d.Title, strings.Join(d.Tags, " "), d.Type, d.Body}
		seen := map[string]bool{}
		for f, text := range texts {
			toks := Analyze(text)
			ds.len[f] = len(toks)
			total[f] += len(toks)
			if len(toks) == 0 {
				continue
			}
			ds.tf[f] = make(map[string]int, len(toks))
			for _, t := range toks {
				ds.tf[f][t]++
				if ix.w[f] != 0 && !seen[t] {
					seen[t] = true
					ix.df[t]++
				}
			}
		}
		ix.docs = append(ix.docs, ds)
	}
	if n := len(sorted); n > 0 {
		for f := range total {
			ix.avg[f] = float64(total[f]) / float64(n)
		}
	}
	return ix
}

// N is the number of documents in the index.
func (ix *Index) N() int { return len(ix.docs) }

// DF is the number of documents containing the stemmed token term in a weighted
// field.
func (ix *Index) DF(term string) int { return ix.df[term] }

// IDF is the BM25 inverse document frequency with the +1 inside the log, so it
// is never negative even for a term present in every document:
//
//	log(1 + (N - df + 0.5) / (df + 0.5))
func IDF(n, df int) float64 {
	return math.Log(1 + (float64(n)-float64(df)+0.5)/(float64(df)+0.5))
}

// Scores returns the BM25F score of every document with a positive score for
// query. The query is reduced with Terms (stopwords dropped, stemmed,
// de-duplicated), so an all-stopword query scores nothing. For each term, the
// weighted, length-normalised field frequencies are summed BEFORE saturation:
//
//	tf~ = Σ_f w_f · tf_f / (1 − B + B · len_f / avglen_f)
//	score += IDF · tf~ · (K1 + 1) / (K1 + tf~)
func (ix *Index) Scores(query string) map[string]float64 {
	terms := Terms(query) // sorted: fixed accumulation order
	out := map[string]float64{}
	n := len(ix.docs)
	for _, t := range terms {
		df := ix.df[t]
		if df == 0 {
			continue
		}
		idf := IDF(n, df)
		for i := range ix.docs {
			d := &ix.docs[i]
			var tfw float64
			for f := 0; f < numFields; f++ {
				if ix.w[f] == 0 || d.tf[f] == nil {
					continue
				}
				tf := d.tf[f][t]
				if tf == 0 {
					continue
				}
				norm := 1 - B + B*float64(d.len[f])/ix.avg[f]
				tfw += ix.w[f] * float64(tf) / norm
			}
			if tfw == 0 {
				continue
			}
			out[d.path] += idf * tfw * (K1 + 1) / (K1 + tfw)
		}
	}
	return out
}

// Rank returns every document with a positive score for query, ordered by score
// descending with ties broken by path ascending, so the order is total and
// deterministic.
func (ix *Index) Rank(query string) []Scored {
	scores := ix.Scores(query)
	out := make([]Scored, 0, len(scores))
	for p, s := range scores {
		out = append(out, Scored{Path: p, Score: s})
	}
	SortScored(out)
	return out
}

// Rank is the one-shot form of NewIndex(docs, w).Rank(query).
func Rank(docs []Document, query string, w Weights) []Scored {
	return NewIndex(docs, w).Rank(query)
}

// SortScored orders s by score descending, then path ascending.
func SortScored(s []Scored) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].Score != s[j].Score {
			return s[i].Score > s[j].Score
		}
		return s[i].Path < s[j].Path
	})
}
