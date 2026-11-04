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

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRankCmdFixture: one node titled with the term (sorts LAST by path), one
// node that mentions it once in a long body (sorts FIRST by path).
func writeRankCmdFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	pad := strings.Repeat("lorem ipsum dolor amet ", 40)
	files := map[string]string{
		"index.md":       "---\ntype: Index\ntitle: Index\n---\n\n# Index\n",
		"a/overview.md":  "---\ntype: Overview\ntitle: Receipts overview\n---\n\n" + pad + "One signature here. " + pad + "\n",
		"z/signature.md": "---\ntype: Field Dictionary\ntitle: Signature format\n---\n\n" + pad + "\n",
		"m/other.md":     "---\ntype: Overview\ntitle: The other one\n---\n\nThe end.\n",
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
	return dir
}

func firstFields(out string) []string {
	var got []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if f := strings.Fields(line); len(f) > 0 {
			got = append(got, f[0])
		}
	}
	return got
}

// Positive control: default order is BM25F relevance, so the titled node wins.
func TestSearchCmd_DefaultRankedByScore(t *testing.T) {
	dir := writeRankCmdFixture(t)
	out, err := runOKF(t, "search", "signature", dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(firstFields(out), " "); got != "z/signature.md a/overview.md" {
		t.Fatalf("default order = %q\n%s", got, out)
	}
}

// Negative control: --sort path is the historical path order and the line
// format is unchanged.
func TestSearchCmd_SortPathIsHistoricalOrder(t *testing.T) {
	dir := writeRankCmdFixture(t)
	out, err := runOKF(t, "search", "signature", dir, "--sort", "path")
	if err != nil {
		t.Fatal(err)
	}
	want := "a/overview.md                            Overview     [body]\n" +
		"z/signature.md                           Field Dictionary [title]\n"
	if out != want {
		t.Fatalf("--sort path output =\n%q\nwant\n%q", out, want)
	}
}

func TestSearchCmd_JSONAddsScore(t *testing.T) {
	dir := writeRankCmdFixture(t)
	out, err := runOKF(t, "search", "signature", dir, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var rs []map[string]any
	if err := json.Unmarshal([]byte(out), &rs); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if len(rs) != 2 || rs[0]["path"] != "z/signature.md" {
		t.Fatalf("json results = %v", rs)
	}
	for _, r := range rs {
		for _, k := range []string{"path", "title", "type", "neighborhood", "matched_on", "score"} {
			if _, ok := r[k]; !ok {
				t.Fatalf("missing key %q in %v", k, r)
			}
		}
		if s, _ := r["score"].(float64); s <= 0 {
			t.Fatalf("score = %v, want > 0", r["score"])
		}
	}
}

func TestSearchCmd_UnknownSortErrors(t *testing.T) {
	dir := writeRankCmdFixture(t)
	if _, err := runOKF(t, "search", "signature", dir, "--sort", "relevance"); err == nil ||
		!strings.Contains(err.Error(), "--sort") {
		t.Fatalf("want an unknown --sort error, got %v", err)
	}
}

// Negative control: a stopword-only query has no content term, so nothing is
// ranked and the substring matches come back in path order.
func TestSearchCmd_StopwordOnlyQueryIsPathOrder(t *testing.T) {
	dir := writeRankCmdFixture(t)
	ranked, err := runOKF(t, "search", "the", dir)
	if err != nil {
		t.Fatal(err)
	}
	byPath, _ := runOKF(t, "search", "the", dir, "--sort", "path")
	if ranked != byPath || ranked == "" {
		t.Fatalf("stopword-only ranked output differs from path order:\n%s\nvs\n%s", ranked, byPath)
	}
}
