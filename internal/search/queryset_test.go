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
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/cwest/okfctl/internal/lexical"
	"github.com/cwest/okfctl/internal/okf"
)

// querySet is one checked-in benchmark file under testdata/queryset/: a
// bundle name (the file's basename) and hand-vetted queries against it. The
// format is fixed by docs/specs/2026-10-12-search-hybrid.md.
type querySet struct {
	Bundle  string      `json:"bundle"`
	Queries []queryCase `json:"queries"`
}

// queryCase is one judgment. Kind is "exact-term" (a rare identifier or
// literal phrase that appears verbatim in the relevant node) or "paraphrase"
// (a question worded without the node's own vocabulary). Relevant holds the
// bundle-relative node paths that answer the query.
type queryCase struct {
	ID       string   `json:"id"`
	Query    string   `json:"query"`
	Kind     string   `json:"kind"`
	Relevant []string `json:"relevant"`
}

const (
	kindExactTerm  = "exact-term"
	kindParaphrase = "paraphrase"
	evalCutoff     = 5 // recall@5
)

func decodeQuerySet(data []byte) (querySet, error) {
	var qs querySet
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&qs); err != nil {
		return querySet{}, err
	}
	return qs, nil
}

func loadQuerySets(t *testing.T) map[string]querySet {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("testdata", "queryset", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no query sets under testdata/queryset")
	}
	out := map[string]querySet{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		qs, err := decodeQuerySet(data)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		out[strings.TrimSuffix(filepath.Base(f), ".json")] = qs
	}
	return out
}

// recallAtK is the fraction of relevant paths found in the first k of ranked.
func recallAtK(ranked, relevant []string, k int) float64 {
	if len(relevant) == 0 {
		return 0
	}
	if k > len(ranked) {
		k = len(ranked)
	}
	want := map[string]bool{}
	for _, r := range relevant {
		want[r] = true
	}
	hit := 0
	for _, p := range ranked[:k] {
		if want[p] {
			hit++
			delete(want, p)
		}
	}
	return float64(hit) / float64(len(relevant))
}

// reciprocalRank is 1/rank of the first relevant path in ranked (1-based), or
// 0 when none is ranked. Its mean over a set is MRR.
func reciprocalRank(ranked, relevant []string) float64 {
	want := map[string]bool{}
	for _, r := range relevant {
		want[r] = true
	}
	for i, p := range ranked {
		if want[p] {
			return 1 / float64(i+1)
		}
	}
	return 0
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestRecallAtK(t *testing.T) {
	ranked := []string{"a", "b", "c", "d", "e", "f"}
	cases := []struct {
		rel  []string
		k    int
		want float64
	}{
		{[]string{"a"}, 5, 1},
		{[]string{"f"}, 5, 0},
		{[]string{"b", "f"}, 5, 0.5},
		{[]string{"b", "e"}, 5, 1},
		{[]string{"z"}, 5, 0},
		{nil, 5, 0},
		{[]string{"f"}, 50, 1}, // k beyond the list is clamped
	}
	for _, c := range cases {
		if got := recallAtK(ranked, c.rel, c.k); !approx(got, c.want) {
			t.Errorf("recallAtK(%v,k=%d)=%v want %v", c.rel, c.k, got, c.want)
		}
	}
	if got := recallAtK([]string{"a", "a"}, []string{"a", "b"}, 5); !approx(got, 0.5) {
		t.Errorf("duplicate ranked path counted twice: %v", got)
	}
}

func TestReciprocalRank(t *testing.T) {
	ranked := []string{"a", "b", "c"}
	cases := []struct {
		rel  []string
		want float64
	}{
		{[]string{"a"}, 1},
		{[]string{"c"}, 1.0 / 3},
		{[]string{"c", "b"}, 0.5}, // first relevant wins
		{[]string{"z"}, 0},
	}
	for _, c := range cases {
		if got := reciprocalRank(ranked, c.rel); !approx(got, c.want) {
			t.Errorf("reciprocalRank(%v)=%v want %v", c.rel, got, c.want)
		}
	}
}

func TestQuerySet_RejectsUnknownField(t *testing.T) {
	_, err := decodeQuerySet([]byte(`{"bundle":"x","queries":[{"id":"q","query":"q","kind":"paraphrase","relevant":["a.md"],"grade":3}]}`))
	if err == nil {
		t.Fatal("unknown field accepted; the format must stay closed so a typo is not silently dropped")
	}
}

// TestQuerySet_CheckedInFilesWellFormed runs always (no corpus needed): every
// checked-in set parses strictly, names the bundle its file is named for, and
// has unique ids, a known kind and at least one relevant path per query.
func TestQuerySet_CheckedInFilesWellFormed(t *testing.T) {
	for name, qs := range loadQuerySets(t) {
		if qs.Bundle != name {
			t.Errorf("%s.json: bundle %q does not match file name", name, qs.Bundle)
		}
		if len(qs.Queries) == 0 {
			t.Errorf("%s: no queries", name)
		}
		seen := map[string]bool{}
		kinds := map[string]int{}
		for _, q := range qs.Queries {
			if q.ID == "" || seen[q.ID] {
				t.Errorf("%s: empty or duplicate id %q", name, q.ID)
			}
			seen[q.ID] = true
			if strings.TrimSpace(q.Query) == "" {
				t.Errorf("%s/%s: empty query", name, q.ID)
			}
			if q.Kind != kindExactTerm && q.Kind != kindParaphrase {
				t.Errorf("%s/%s: kind %q not exact-term|paraphrase", name, q.ID, q.Kind)
			}
			kinds[q.Kind]++
			if len(q.Relevant) == 0 {
				t.Errorf("%s/%s: no relevant paths", name, q.ID)
			}
			for _, r := range q.Relevant {
				if !strings.HasSuffix(r, ".md") || strings.HasPrefix(r, "/") || strings.Contains(r, `\`) {
					t.Errorf("%s/%s: relevant %q is not a bundle-relative .md path", name, q.ID, r)
				}
			}
		}
		if kinds[kindExactTerm] == 0 || kinds[kindParaphrase] == 0 {
			t.Errorf("%s: needs both kinds (exact-term %d, paraphrase %d)", name, kinds[kindExactTerm], kinds[kindParaphrase])
		}
	}
}

// metricSum accumulates recall@5 and reciprocal rank over a group of queries.
type metricSum struct {
	n          int
	recall, rr float64
}

func (m *metricSum) add(recall, rr float64) { m.n++; m.recall += recall; m.rr += rr }

func (m metricSum) String() string {
	if m.n == 0 {
		return "n=0"
	}
	return fmt.Sprintf("n=%-3d recall@5=%.3f MRR=%.3f", m.n, m.recall/float64(m.n), m.rr/float64(m.n))
}

func lexPaths(ss []lexical.Scored) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Path
	}
	return out
}

func rankLabel(ranked, relevant []string) string {
	rr := reciprocalRank(ranked, relevant)
	if rr == 0 {
		return "-"
	}
	return fmt.Sprintf("%d", int(math.Round(1/rr)))
}

// TestEval_HybridQuerySet is the Increment 5 measurement: recall@5 and MRR for
// the lexical (BM25F), semantic and hybrid (RRF) rankers over every checked-in
// query set, per kind, per bundle and pooled. It is skipped unless
// OKFCTL_EVAL_KB_ROOT names a directory holding one bundle per set (by the
// set's bundle name); the corpus is not vendored. OKFCTL_TEST_MODEL_DIR selects
// a model2vec model for the semantic side; without it the hash embedder stands
// in and the run is labelled "hash (not semantic)"—its numbers say nothing
// about whether hybrid should be promoted. The index is built in memory and
// nothing is written into the bundles. Run:
//
//	OKFCTL_EVAL_KB_ROOT=<dir> OKFCTL_TEST_MODEL_DIR=<model> \
//	  go test ./internal/search/ -run TestEval_HybridQuerySet -v
func TestEval_HybridQuerySet(t *testing.T) {
	root := os.Getenv("OKFCTL_EVAL_KB_ROOT")
	if root == "" {
		t.Skip("set OKFCTL_EVAL_KB_ROOT to a directory of bundles to run the query-set measurement")
	}
	var e Embedder
	label := "hash (not semantic)"
	if dir := os.Getenv("OKFCTL_TEST_MODEL_DIR"); dir != "" {
		m, err := LoadModel2VecEmbedder(dir)
		if err != nil {
			t.Fatalf("load model %s: %v", dir, err)
		}
		e, label = m, m.Name()
	} else {
		e = NewHashEmbedder()
	}
	t.Logf("embedder: %s; RRF k=%d; cutoff=%d", label, RRFK, evalCutoff)

	rankers := []string{"lexical", "semantic", "hybrid"}
	pooled := map[string]map[string]*metricSum{} // ranker -> kind ("all" too)
	for _, r := range rankers {
		pooled[r] = map[string]*metricSum{"all": {}, kindExactTerm: {}, kindParaphrase: {}}
	}

	sets := loadQuerySets(t)
	names := make([]string, 0, len(sets))
	for n := range sets {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		qs := sets[name]
		b, err := okf.Load(filepath.Join(root, qs.Bundle))
		if err != nil {
			t.Fatalf("load bundle %s: %v", qs.Bundle, err)
		}
		for _, q := range qs.Queries {
			for _, r := range q.Relevant {
				if _, ok := b.Nodes[r]; !ok {
					t.Fatalf("%s/%s: relevant %q is not a node of the bundle", name, q.ID, r)
				}
			}
		}
		s := BuildIndex(b, e, nil)
		docs := okf.LexicalDocuments(b)

		per := map[string]map[string]*metricSum{}
		for _, r := range rankers {
			per[r] = map[string]*metricSum{"all": {}, kindExactTerm: {}, kindParaphrase: {}}
		}
		t.Logf("== %s (%d nodes, %d queries)", name, len(b.Nodes), len(qs.Queries))
		t.Logf("   %-28s %-10s %5s %5s %5s", "id", "kind", "lex", "sem", "hyb")
		for _, q := range qs.Queries {
			sem, err := QueryWith(s, e, q.Query, 0, QueryOptions{})
			if err != nil {
				t.Fatal(err)
			}
			hyb, err := HybridQuery(s, e, b, q.Query, 0, QueryOptions{})
			if err != nil {
				t.Fatal(err)
			}
			ranked := map[string][]string{
				"lexical":  lexPaths(lexical.Rank(docs, q.Query, lexical.DefaultWeights)),
				"semantic": paths(sem),
				"hybrid":   paths(hyb),
			}
			for _, r := range rankers {
				rec := recallAtK(ranked[r], q.Relevant, evalCutoff)
				rr := reciprocalRank(ranked[r], q.Relevant)
				for _, k := range []string{"all", q.Kind} {
					per[r][k].add(rec, rr)
					pooled[r][k].add(rec, rr)
				}
			}
			t.Logf("   %-28s %-10s %5s %5s %5s", q.ID, q.Kind,
				rankLabel(ranked["lexical"], q.Relevant), rankLabel(ranked["semantic"], q.Relevant), rankLabel(ranked["hybrid"], q.Relevant))
		}
		for _, k := range []string{kindExactTerm, kindParaphrase, "all"} {
			for _, r := range rankers {
				t.Logf("   %-10s %-8s %s", k, r, per[r][k])
			}
		}
	}
	t.Logf("== pooled over %d bundles (embedder: %s)", len(names), label)
	for _, k := range []string{kindExactTerm, kindParaphrase, "all"} {
		for _, r := range rankers {
			t.Logf("   %-10s %-8s %s", k, r, pooled[r][k])
		}
	}
	h, sm := pooled["hybrid"]["all"], pooled["semantic"]["all"]
	beats := h.recall > sm.recall && h.rr > sm.rr
	t.Logf("promotion rule (hybrid beats semantic on BOTH recall@5 and MRR): %v", beats)
	if label == "hash (not semantic)" {
		t.Logf("NOTE: hash embedder is not semantic; this run cannot decide promotion")
	}
}
