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

package okf

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeRankBundle(t *testing.T) *Bundle {
	t.Helper()
	dir := t.TempDir()
	pad := strings.Repeat("lorem ipsum dolor amet ", 40)
	files := map[string]string{
		"index.md":          "---\ntype: Index\ntitle: Index\nokf_version: \"0.2\"\n---\n\n# Index\n\nsignature signature signature\n",
		"log.md":            "# Log\n\n- signature\n",
		"a/overview.md":     "---\ntype: Overview\ntitle: Receipts overview\n---\n\n" + pad + "One signature here. " + pad + "\n",
		"z/signature.md":    "---\ntype: Field Dictionary\ntitle: Signature format\ntags: [security]\n---\n\n" + pad + pad + "\n",
		"m/decision.md":     "---\ntype: Wholly Unknown Type\ntitle: Decision record\n---\n\nA specification decision.\n",
		"c/ci.md":           "---\ntype: Overview\ntitle: CI pipeline\n---\n\nThe ci job.\n",
		"untitled-thing.md": "---\ntype: Overview\n---\n\nNothing relevant.\n",
	}
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Search keeps its historical contract—substring match set, PATH order—and
// only adds Score.
func TestSearch_PathOrderPreservedScoreFilled(t *testing.T) {
	b := writeRankBundle(t)
	rs := Search(b, "signature", FieldAny)
	if got, want := paths(rs), []string{"a/overview.md", "z/signature.md"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Search order = %v want %v (path order)", got, want)
	}
	for _, r := range rs {
		if r.Score <= 0 {
			t.Fatalf("%s: Score = %v, want > 0", r.Path, r.Score)
		}
	}
}

// Positive control: the node titled with the term outranks the node that
// mentions it once in its body.
func TestRankResults_TitleMatchOutranksBodyMention(t *testing.T) {
	b := writeRankBundle(t)
	rs := Search(b, "signature", FieldAny)
	RankResults(rs)
	if got, want := paths(rs), []string{"z/signature.md", "a/overview.md"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ranked = %v want %v", got, want)
	}
}

// §3.1: reserved index.md/log.md are never ranked, even when they repeat the
// term.
func TestRankBM25_Section3_1_ReservedFilesNeverRanked(t *testing.T) {
	b := writeRankBundle(t)
	for _, s := range RankBM25(b, "signature", FieldAny) {
		if s.Path == "index.md" || s.Path == "log.md" {
			t.Fatalf("reserved file ranked: %v", s)
		}
	}
}

// A substring-only hit (`ci` inside "decision"/"specification") stays in the
// match set with Score 0 and sorts after every scored hit; the token hit wins.
func TestRankResults_SubstringOnlyHitScoresZeroAndSortsLast(t *testing.T) {
	b := writeRankBundle(t)
	rs := Search(b, "ci", FieldAny)
	RankResults(rs)
	got := paths(rs)
	if len(got) < 2 || got[0] != "c/ci.md" {
		t.Fatalf("ranked = %v, want c/ci.md first", got)
	}
	for _, r := range rs[1:] {
		if r.Score != 0 {
			t.Fatalf("%s: substring-only hit scored %v", r.Path, r.Score)
		}
	}
	if !sortedStrings(got[1:]) {
		t.Fatalf("zero-score tail must be in path order: %v", got[1:])
	}
}

// Negative control: a stopword-only query ranks nothing, so the ranked order
// IS path order.
func TestRankResults_StopwordOnlyQueryIsPathOrder(t *testing.T) {
	b := writeRankBundle(t)
	rs := Search(b, "the", FieldAny)
	before := paths(rs)
	RankResults(rs)
	if !reflect.DeepEqual(paths(rs), before) {
		t.Fatalf("stopword-only query reordered: %v -> %v", before, paths(rs))
	}
	if got := RankBM25(b, "the", FieldAny); len(got) != 0 {
		t.Fatalf("RankBM25(the) = %v, want nothing ranked", got)
	}
}

// §4.1: title falls back to the file base name; an unknown type is ranked like
// any other (open vocabulary).
func TestLexicalDocuments_Section4_1_TitleFallbackAndOpenType(t *testing.T) {
	b := writeRankBundle(t)
	if got := RankBM25(b, "untitled", FieldTitle); len(got) != 1 || got[0].Path != "untitled-thing.md" {
		t.Fatalf("title fallback rank = %v", got)
	}
	if got := RankBM25(b, "wholly unknown", FieldType); len(got) != 1 || got[0].Path != "m/decision.md" {
		t.Fatalf("unknown type rank = %v", got)
	}
}

func TestRankBM25_FieldRestriction(t *testing.T) {
	b := writeRankBundle(t)
	got := RankBM25(b, "signature", FieldBody)
	if len(got) != 1 || got[0].Path != "a/overview.md" {
		t.Fatalf("body-only rank = %v want [a/overview.md]", got)
	}
}

func sortedStrings(s []string) bool {
	for i := 1; i < len(s); i++ {
		if s[i-1] > s[i] {
			return false
		}
	}
	return true
}
