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
	"reflect"
	"testing"
)

func paths(s []Scored) []string {
	out := make([]string, len(s))
	for i, x := range s {
		out[i] = x.Path
	}
	return out
}

// filler returns n distinct-ish body words that never collide with a test term.
func filler(n int) string {
	words := []string{"lorem", "ipsum", "dolor", "amet", "consectetur", "adipiscing", "elit", "sed"}
	out := ""
	for i := 0; i < n; i++ {
		out += words[i%len(words)] + " "
	}
	return out
}

func TestIDF_Formula(t *testing.T) {
	// log(1 + (N - df + 0.5)/(df + 0.5)) — never negative, even at df == N.
	if got, want := IDF(10, 1), math.Log(1+9.5/1.5); math.Abs(got-want) > 1e-12 {
		t.Fatalf("IDF(10,1) = %v want %v", got, want)
	}
	if got := IDF(10, 10); got <= 0 {
		t.Fatalf("IDF(N,N) = %v, want > 0 (the +1 inside the log)", got)
	}
	if IDF(10, 1) <= IDF(10, 5) {
		t.Fatal("a rarer term must carry more IDF")
	}
}

func TestConstants_Named(t *testing.T) {
	if K1 != 1.2 || B != 0.75 {
		t.Fatalf("K1=%v B=%v, want 1.2 and 0.75", K1, B)
	}
	w := DefaultWeights
	if !(w.Title > w.Tags && w.Tags > w.Type && w.Type > w.Body && w.Body > 0) {
		t.Fatalf("DefaultWeights %+v must order title > tags > type > body > 0", w)
	}
}

// Positive control: a node whose TITLE names the term outranks a node that
// mentions it once in passing in its body.
func TestRank_TitleOutranksSingleBodyMention(t *testing.T) {
	docs := []Document{
		{Path: "a-body.md", Title: "Receipts overview", Body: filler(60) + "signature " + filler(60)},
		{Path: "z-title.md", Title: "Signature format", Body: filler(120)},
		{Path: "m-none.md", Title: "Closing", Body: filler(120)},
	}
	got := paths(Rank(docs, "signature", DefaultWeights))
	want := []string{"z-title.md", "a-body.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rank = %v want %v", got, want)
	}
}

// Positive control: a two-letter acronym gets a non-trivial IDF because DF is
// counted on TOKENS. Under raw-substring containment `ci` is "in" decision,
// specification and certificate—every document—and its IDF would collapse.
func TestDF_TwoLetterAcronymCountsTokensNotSubstrings(t *testing.T) {
	docs := []Document{
		{Path: "a.md", Title: "CI pipeline", Body: "the ci job runs"},
		{Path: "b.md", Title: "Decision record", Body: "a specification decision"},
		{Path: "c.md", Title: "Certificate", Body: "certificate specification"},
		{Path: "d.md", Title: "Officials", Body: "social policies"},
	}
	ix := NewIndex(docs, DefaultWeights)
	if got := ix.DF("ci"); got != 1 {
		t.Fatalf("DF(ci) = %d want 1 (token DF, not substring)", got)
	}
	if idf := IDF(ix.N(), ix.DF("ci")); idf < 1 {
		t.Fatalf("IDF(ci) = %v, want a non-trivial (>= 1) weight", idf)
	}
	if got := paths(ix.Rank("CI")); !reflect.DeepEqual(got, []string{"a.md"}) {
		t.Fatalf("rank(CI) = %v want [a.md]", got)
	}
}

// Negative control: a stopword-only query carries no content term, so NOTHING
// is ranked.
func TestRank_StopwordOnlyQueryRanksNothing(t *testing.T) {
	docs := []Document{{Path: "a.md", Title: "The thing", Body: "the of and the"}}
	for _, q := range []string{"the", "the of and", "", "   "} {
		if got := Rank(docs, q, DefaultWeights); len(got) != 0 {
			t.Fatalf("Rank(%q) = %v, want nothing ranked", q, got)
		}
	}
}

func TestRank_TermFrequencySaturates(t *testing.T) {
	one := Scores1(t, "hash")
	many := Scores1(t, "hash hash hash hash hash hash hash hash")
	if !(many > one) {
		t.Fatalf("more occurrences must score higher: one=%v many=%v", one, many)
	}
	// Saturation: the score is bounded by IDF·(K1+1) whatever the tf.
	if many >= IDF(2, 1)*(K1+1) {
		t.Fatalf("score %v exceeded the saturation ceiling", many)
	}
}

// Scores1 scores body against "hash" in a two-doc index (the other doc has no
// hash), so DF and N are fixed.
func Scores1(t *testing.T, body string) float64 {
	t.Helper()
	docs := []Document{{Path: "a.md", Body: body}, {Path: "b.md", Body: "unrelated words here"}}
	return NewIndex(docs, DefaultWeights).Scores("hash")["a.md"]
}

func TestRank_LengthNormalisation(t *testing.T) {
	docs := []Document{
		{Path: "long.md", Body: "jet " + filler(300)},
		{Path: "short.md", Body: "jet " + filler(10)},
		{Path: "other.md", Body: filler(50)},
	}
	got := paths(Rank(docs, "jet", DefaultWeights))
	if !reflect.DeepEqual(got, []string{"short.md", "long.md"}) {
		t.Fatalf("one mention in a short body must outrank one in a long body: %v", got)
	}
}

func TestRank_FieldWeightOrdering(t *testing.T) {
	// One occurrence of the term in exactly one field each; identical lengths.
	docs := []Document{
		{Path: "1-body.md", Title: "x y", Tags: []string{"p", "q"}, Type: "t u", Body: "audit " + filler(5)},
		{Path: "2-type.md", Title: "x y", Tags: []string{"p", "q"}, Type: "audit u", Body: filler(6)},
		{Path: "3-tags.md", Title: "x y", Tags: []string{"audit", "q"}, Type: "t u", Body: filler(6)},
		{Path: "4-title.md", Title: "audit y", Tags: []string{"p", "q"}, Type: "t u", Body: filler(6)},
	}
	got := paths(Rank(docs, "audit", DefaultWeights))
	want := []string{"4-title.md", "3-tags.md", "2-type.md", "1-body.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rank = %v want %v (title > tags > type > body)", got, want)
	}
}

func TestRank_ZeroWeightFieldIgnored(t *testing.T) {
	docs := []Document{
		{Path: "a.md", Title: "audit"},
		{Path: "b.md", Body: "audit"},
	}
	ix := NewIndex(docs, Weights{Body: 1})
	if got := paths(ix.Rank("audit")); !reflect.DeepEqual(got, []string{"b.md"}) {
		t.Fatalf("body-only weights rank = %v want [b.md]", got)
	}
	if ix.DF("audit") != 1 {
		t.Fatalf("DF must count only weighted fields: %d", ix.DF("audit"))
	}
}

func TestRank_TiesBrokenByPathAndDeterministic(t *testing.T) {
	docs := []Document{
		{Path: "c.md", Body: "vat rate"},
		{Path: "a.md", Body: "vat rate"},
		{Path: "b.md", Body: "vat rate"},
		{Path: "d.md", Body: "other"},
	}
	want := []string{"a.md", "b.md", "c.md"}
	for i := 0; i < 20; i++ {
		got := Rank(docs, "vat", DefaultWeights)
		if !reflect.DeepEqual(paths(got), want) {
			t.Fatalf("run %d: %v want %v", i, paths(got), want)
		}
		if got[0].Score != got[2].Score {
			t.Fatalf("equal documents must score equally: %v", got)
		}
	}
}

func TestRank_StemmedInflectionsMatch(t *testing.T) {
	docs := []Document{{Path: "a.md", Body: "hashes are chained"}, {Path: "b.md", Body: "nothing"}}
	if got := paths(Rank(docs, "hash", DefaultWeights)); !reflect.DeepEqual(got, []string{"a.md"}) {
		t.Fatalf("hash must reach hashes: %v", got)
	}
}

// Scores are rounded to ScoreDecimals so --json and the score-then-path order
// are identical on every platform (FMA fusion changes raw low bits).
func TestScores_RoundedToFixedPrecision(t *testing.T) {
	docs := []Document{
		{Path: "a.md", Title: "signature chain", Body: filler(37) + "signature"},
		{Path: "b.md", Body: filler(11) + "signature signature"},
		{Path: "c.md", Tags: []string{"signature"}, Body: filler(53)},
	}
	scores := NewIndex(docs, DefaultWeights).Scores("signature chain")
	if len(scores) != 3 {
		t.Fatalf("scores = %v, want 3 hits", scores)
	}
	for p, s := range scores {
		if s != math.Round(s*1e6)/1e6 {
			t.Errorf("%s score %v carries more than %d decimals", p, s, ScoreDecimals)
		}
	}
	// Negative control: rounding keeps distinct scores distinct and the
	// title hit first.
	got := paths(Rank(docs, "signature chain", DefaultWeights))
	if got[0] != "a.md" || scores["b.md"] == scores["c.md"] {
		t.Fatalf("rounding changed the ranking: %v %v", got, scores)
	}
}

func TestRoundScore(t *testing.T) {
	for in, want := range map[float64]float64{
		0.4971234449: 0.497123, 0.4971235551: 0.497124, 0: 0, 3.5: 3.5,
	} {
		if got := RoundScore(in); got != want {
			t.Errorf("RoundScore(%v) = %v, want %v", in, got, want)
		}
	}
}
