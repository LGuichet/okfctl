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
	"math"
	"strings"
	"testing"
	"time"

	"github.com/cwest/okfctl/internal/lexical"
	"github.com/cwest/okfctl/internal/okf"
)

// fakeEmbedder maps every query to the unit vector [1, 0]. Paired with
// fakeStore it gives a test full control over the semantic order without a
// model: the semantic ranking is exactly the order the test lists the paths in.
type fakeEmbedder struct{}

func (fakeEmbedder) Name() string { return "fake-embedder" }
func (fakeEmbedder) Dim() int     { return 2 }
func (fakeEmbedder) Encode(texts []string) [][]float64 {
	out := make([][]float64, len(texts))
	for i := range texts {
		out[i] = []float64{1, 0}
	}
	return out
}

// fakeStore builds a passage-less store whose cosine against the fake query
// strictly decreases along semanticOrder: the i-th path sits at angle 0.1·i, so
// its cosine is cos(0.1·i) and the semantic ranking is semanticOrder itself.
func fakeStore(semanticOrder ...string) *Store {
	s := &Store{Model: fakeEmbedder{}.Name(), Dim: 2}
	for i, p := range semanticOrder {
		th := 0.1 * float64(i)
		s.Entries = append(s.Entries, Entry{Path: p, Vector: []float64{math.Cos(th), math.Sin(th)}})
	}
	return s
}

func paths(rs []Result) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Path
	}
	return out
}

func rankOf(rs []Result, path string) int {
	for i, r := range rs {
		if r.Path == path {
			return i + 1
		}
	}
	return 0
}

func lexRankOf(ls []lexical.Scored, path string) int {
	for i, s := range ls {
		if s.Path == path {
			return i + 1
		}
	}
	return 0
}

func TestRRFK_IsTheLiteratureDefault(t *testing.T) {
	if RRFK != 60 {
		t.Fatalf("RRFK = %d, want 60", RRFK)
	}
}

// TestFuseRRF_ScoresAndDeterministicTieBreak pins the fusion arithmetic: ranks
// are 1-based, a node in one list scores 1/(60+rank), a node in both sums the
// two, and equal fused scores break by path ascending.
func TestFuseRRF_ScoresAndDeterministicTieBreak(t *testing.T) {
	sem := []Result{{Path: "a.md", Score: 0.9}, {Path: "b.md", Score: 0.8}, {Path: "c.md", Score: 0.7}}
	lex := []lexical.Scored{{Path: "c.md", Score: 4}, {Path: "d.md", Score: 2}}
	got := FuseRRF(sem, lex, RRFK)

	want := []struct {
		path  string
		score float64
	}{
		{"c.md", 1.0/63 + 1.0/61},
		{"a.md", 1.0 / 61},
		{"b.md", 1.0 / 62}, // ties with d.md at 1/62; path breaks the tie
		{"d.md", 1.0 / 62},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d results %v, want %d", len(got), paths(got), len(want))
	}
	for i, w := range want {
		if got[i].Path != w.path || math.Abs(got[i].Score-w.score) > 1e-15 {
			t.Errorf("rank %d = %s %.6f, want %s %.6f", i+1, got[i].Path, got[i].Score, w.path, w.score)
		}
	}
}

// TestFuseRRF_SnippetFromSemanticSide: the snippet is the semantic side's best
// passage; a lexical-only hit has none.
func TestFuseRRF_SnippetFromSemanticSide(t *testing.T) {
	sem := []Result{{Path: "a.md", Score: 0.9, Snippet: "passage a"}}
	lex := []lexical.Scored{{Path: "b.md", Score: 1}, {Path: "a.md", Score: 0.5}}
	got := FuseRRF(sem, lex, RRFK)
	for _, r := range got {
		switch r.Path {
		case "a.md":
			if r.Snippet != "passage a" {
				t.Errorf("a.md snippet = %q, want the semantic passage", r.Snippet)
			}
		case "b.md":
			if r.Snippet != "" {
				t.Errorf("lexical-only b.md snippet = %q, want empty", r.Snippet)
			}
		}
	}
}

func TestFuseRRF_EmptyInputs(t *testing.T) {
	if got := FuseRRF(nil, nil, RRFK); len(got) != 0 {
		t.Errorf("empty fusion = %v, want none", got)
	}
}

// genericBody is prose that shares no content token with the control queries.
const genericBody = "This node describes how the till handles daily operations."

// TestHybrid_PositiveControl_ExactRareTermRecovered is the roadmap's positive
// control: a query that is an exact rare identifier the embedder misses (the
// target sits LAST in the semantic order, outside the top 5) is lifted to the
// top by hybrid, because BM25 ranks the only node carrying the term first.
func TestHybrid_PositiveControl_ExactRareTermRecovered(t *testing.T) {
	files := map[string]string{
		"jet/event-codes.md": node("Concept", "Event codes", "The event code XQZ42 marks a closing."),
	}
	order := []string{}
	for _, n := range []string{"n1", "n2", "n3", "n4", "n5", "n6", "n7"} {
		files["ops/"+n+".md"] = node("Concept", "Operations "+n, genericBody)
		order = append(order, "ops/"+n+".md")
	}
	order = append(order, "jet/event-codes.md") // the embedder ranks it last
	b, _ := writeBundle(t, files)
	s := fakeStore(order...)
	e := fakeEmbedder{}

	sem, err := QueryWith(s, e, "XQZ42", 5, QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if r := rankOf(sem, "jet/event-codes.md"); r != 0 {
		t.Fatalf("precondition: semantic top-5 must miss the target, found at %d", r)
	}

	got, err := HybridQuery(s, e, b, "XQZ42", 5, QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if r := rankOf(got, "jet/event-codes.md"); r != 1 {
		t.Fatalf("hybrid rank of the exact-term target = %d, want 1 (got %v)", r, paths(got))
	}
}

// negativeFixture builds a paraphrase case. The query "how long are records
// kept" is answered by retention.md, which semantic ranks FIRST. A distractor,
// till-records.md, is the strongest BM25 hit (it repeats "records kept") but
// semantic ranks it LAST. targetSharesTerm controls whether the answer node
// itself carries a query term.
func negativeFixture(t *testing.T, targetSharesTerm bool) (*okf.Bundle, *Store) {
	t.Helper()
	targetBody := "Sales data is retained for six years."
	if targetSharesTerm {
		targetBody = "Sales data and its records are retained for six years."
	}
	b, _ := writeBundle(t, map[string]string{
		"data/retention.md":    node("Concept", "Retention of sales data", targetBody),
		"ops/a.md":             node("Concept", "Opening", genericBody),
		"ops/b.md":             node("Concept", "Closing", genericBody),
		"ops/c.md":             node("Concept", "Float", genericBody),
		"ops/d.md":             node("Concept", "Backup", genericBody),
		"ops/till-records.md":  node("Concept", "Records kept by the till", "Records kept. The till records are kept as records."),
		"ops/unrelated-zzz.md": node("Concept", "Training mode", genericBody),
	})
	s := fakeStore("data/retention.md", "ops/a.md", "ops/b.md", "ops/c.md", "ops/d.md", "ops/unrelated-zzz.md", "ops/till-records.md")
	return b, s
}

const paraphraseQuery = "how long are records kept"

// TestHybrid_NegativeControl_ParaphraseNotPushedDown is the roadmap's negative
// control: a paraphrase whose answer semantic ranks first stays first under
// hybrid even though another node is the strongest lexical hit, as long as the
// lexical side does not also beat the answer in the semantic list.
func TestHybrid_NegativeControl_ParaphraseNotPushedDown(t *testing.T) {
	b, s := negativeFixture(t, true)
	e := fakeEmbedder{}

	lex := lexical.Rank(okf.LexicalDocuments(b), paraphraseQuery, lexical.DefaultWeights)
	if r := lexRankOf(lex, "ops/till-records.md"); r != 1 {
		t.Fatalf("precondition: distractor must be lexical #1, is %d (%v)", r, lex)
	}
	if r := lexRankOf(lex, "data/retention.md"); r == 0 {
		t.Fatalf("precondition: target must carry a query term (%v)", lex)
	}

	got, err := HybridQuery(s, e, b, paraphraseQuery, 5, QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if r := rankOf(got, "data/retention.md"); r != 1 {
		t.Fatalf("paraphrase answer pushed to %d by hybrid (got %v), want 1", r, paths(got))
	}
}

// TestHybrid_NegativeControl_NoLexicalSignalIsSemanticOrder: when the lexical
// side ranks nothing (an all-stopword query, or terms no node carries), hybrid
// returns exactly the semantic order.
func TestHybrid_NegativeControl_NoLexicalSignalIsSemanticOrder(t *testing.T) {
	b, s := negativeFixture(t, false)
	e := fakeEmbedder{}
	for _, q := range []string{"how are the", "wombat xylophone"} {
		sem, err := QueryWith(s, e, q, 0, QueryOptions{})
		if err != nil {
			t.Fatal(err)
		}
		got, err := HybridQuery(s, e, b, q, 0, QueryOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(paths(got), ",") != strings.Join(paths(sem), ",") {
			t.Errorf("%q: hybrid order %v != semantic order %v", q, paths(got), paths(sem))
		}
	}
}

// TestHybrid_KnownProperty_SemanticOnlyWinnerCanDrop characterizes the plain
// RRF property the spec records: when the answer shares NO token with the query
// while another node is in both lists, that node outranks the semantic winner.
// This test pins the behavior so a change to it is a deliberate decision.
func TestHybrid_KnownProperty_SemanticOnlyWinnerCanDrop(t *testing.T) {
	b, s := negativeFixture(t, false)
	got, err := HybridQuery(s, fakeEmbedder{}, b, paraphraseQuery, 5, QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if r := rankOf(got, "ops/till-records.md"); r != 1 {
		t.Fatalf("both-list distractor at %d, want 1 (got %v)", r, paths(got))
	}
	if r := rankOf(got, "data/retention.md"); r != 2 {
		t.Fatalf("semantic-only winner at %d, want 2 (got %v)", r, paths(got))
	}
}

// TestHybrid_FiltersApplyBeforeBothRankings: a filtered-out node can enter
// neither list, even when it is the only BM25 hit; a filter is the same
// predicate on both sides.
func TestHybrid_FiltersApplyBeforeBothRankings(t *testing.T) {
	b, s := negativeFixture(t, true)
	meta := metaFromBundle(b)
	cases := []struct {
		name   string
		filter Filter
	}{
		{"not-path", Filter{NotPathPrefixes: []string{"ops/till"}}},
		{"path", Filter{PathPrefixes: []string{"data/"}}},
	}
	for _, c := range cases {
		got, err := HybridQuery(s, fakeEmbedder{}, b, paraphraseQuery, 0, QueryOptions{Meta: meta, Filter: c.filter})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range got {
			m := meta[r.Path]
			if !c.filter.keep(r.Path, m, true) {
				t.Errorf("%s: filtered-out node %s appeared in hybrid results", c.name, r.Path)
			}
		}
		if rankOf(got, "ops/till-records.md") != 0 {
			t.Errorf("%s: the top BM25 hit leaked past the filter: %v", c.name, paths(got))
		}
	}
	// Type filter that matches nothing: empty result, never an error.
	got, err := HybridQuery(s, fakeEmbedder{}, b, paraphraseQuery, 0, QueryOptions{Meta: meta, Filter: Filter{Types: []string{"Nope"}}})
	if err != nil || len(got) != 0 {
		t.Errorf("zero-match type filter = %v, %v; want empty, nil", paths(got), err)
	}
}

// TestHybrid_MinRelevanceIsSemanticSideOnly: MinRelevance keeps its RAW-cosine
// semantics on the semantic side, so a sub-floor node with no lexical match is
// gone, while a sub-floor node that carries the query's term enters through the
// lexical list.
func TestHybrid_MinRelevanceIsSemanticSideOnly(t *testing.T) {
	b, s := negativeFixture(t, true)
	// cos(0.1·i): i=0 → 1.0, i=5 → 0.8776, i=6 → 0.8253. A floor of 0.9 keeps
	// only the first five nodes on the semantic side.
	opts := QueryOptions{Meta: metaFromBundle(b), Decay: &DecayOptions{MinRelevance: 0.9, Now: time.Now()}}
	got, err := HybridQuery(s, fakeEmbedder{}, b, paraphraseQuery, 0, opts)
	if err != nil {
		t.Fatal(err)
	}
	if rankOf(got, "ops/unrelated-zzz.md") != 0 {
		t.Errorf("sub-floor node without a lexical match survived: %v", paths(got))
	}
	if rankOf(got, "ops/till-records.md") == 0 {
		t.Errorf("sub-floor node with the query's terms did not enter via BM25: %v", paths(got))
	}
}

// TestHybrid_DecayRanksAfterDecay: the semantic rank fed into the fusion is
// the rank AFTER recency decay re-scores, exactly as QueryWith orders it.
func TestHybrid_DecayRanksAfterDecay(t *testing.T) {
	b, s := negativeFixture(t, false)
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	meta := metaFromBundle(b)
	old := meta["data/retention.md"]
	old.Generated, old.HasGenerated = now.AddDate(-3, 0, 0), true
	meta["data/retention.md"] = old
	fresh := meta["ops/a.md"]
	fresh.Generated, fresh.HasGenerated = now, true
	meta["ops/a.md"] = fresh
	opts := QueryOptions{Meta: meta, Decay: &DecayOptions{HalfLifeDays: 30, Now: now, DecayFloor: 0}}

	const q = "wombat xylophone" // no lexical signal: hybrid order must equal semantic order
	sem, err := QueryWith(s, fakeEmbedder{}, q, 0, opts)
	if err != nil {
		t.Fatal(err)
	}
	if sem[0].Path == "data/retention.md" {
		t.Fatalf("precondition: decay must demote the old node, got %v", paths(sem))
	}
	got, err := HybridQuery(s, fakeEmbedder{}, b, q, 0, opts)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(paths(got), ",") != strings.Join(paths(sem), ",") {
		t.Errorf("hybrid %v did not follow the post-decay semantic order %v", paths(got), paths(sem))
	}
}

func TestHybrid_CutsToK(t *testing.T) {
	b, s := negativeFixture(t, true)
	got, err := HybridQuery(s, fakeEmbedder{}, b, paraphraseQuery, 3, QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("k=3 returned %d results", len(got))
	}
}

func TestHybrid_RejectsLexicalGateAndNilBundle(t *testing.T) {
	b, s := negativeFixture(t, true)
	if _, err := HybridQuery(s, fakeEmbedder{}, b, "x", 5, QueryOptions{LexicalGate: BuildLexicalGate(b, "x")}); err == nil {
		t.Error("hybrid with a lexical gate: want an error")
	}
	if _, err := HybridQuery(s, fakeEmbedder{}, nil, "x", 5, QueryOptions{}); err == nil {
		t.Error("hybrid with a nil bundle: want an error")
	}
}

func TestHybrid_ModelMismatchPropagates(t *testing.T) {
	b, s := negativeFixture(t, true)
	if _, err := HybridQuery(s, NewHashEmbedder(), b, "x", 5, QueryOptions{}); err != ErrModelMismatch {
		t.Errorf("err = %v, want ErrModelMismatch", err)
	}
}
