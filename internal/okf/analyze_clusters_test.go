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

import "testing"

// analyze adopts the shared tag fold, so three spellings of one tag over three
// nodes form a single cluster that reaches the default --cluster-min 3—where a
// case-only fold leaves them as three 1-node tags, all invisible. The label is
// the lexicographically-first lower-cased spelling, pinned exactly.
func TestAnalyzeClusters_FoldedVariantsFormOneCluster(t *testing.T) {
	b := mkLintBundle(t, map[string]string{
		"index.md": "---\ntype: Index\ntitle: Index\n---\n\n# Index\n\n- [A](a.md)\n- [B](b.md)\n- [C](c.md)\n",
		"a.md":     lintDocTags("Concept", "A", []string{"runbook"}, "Body."),
		"b.md":     lintDocTags("Concept", "B", []string{"runbooks"}, "Body."),
		"c.md":     lintDocTags("Concept", "C", []string{"run-book"}, "Body."),
	})
	clusters := analyzeClusters(b, DefaultAnalyzeOptions())
	if len(clusters) != 1 {
		t.Fatalf("expected one folded runbook cluster at cluster-min 3, got clusters=%+v", clusters)
	}
	if clusters[0].Tag != "run-book" || len(clusters[0].Nodes) != 3 {
		t.Fatalf("folded runbook cluster should be labelled \"run-book\" over 3 nodes, got %+v", clusters[0])
	}
}

// Case folding already merged Wine/wine before this change; that behavior and
// its lower-cased label must survive the fold swap (regression guard for the
// analyze path: an upper-case spelling sorting first must not relabel it).
func TestAnalyzeClusters_CaseVariantsStillMerge(t *testing.T) {
	b := mkLintBundle(t, map[string]string{
		"index.md": "---\ntype: Index\ntitle: Index\n---\n\n# Index\n\n- [A](a.md)\n- [B](b.md)\n- [C](c.md)\n",
		"a.md":     lintDocTags("Concept", "A", []string{"Wine"}, "Body."),
		"b.md":     lintDocTags("Concept", "B", []string{"wine"}, "Body."),
		"c.md":     lintDocTags("Concept", "C", []string{"WINE"}, "Body."),
	})
	clusters := analyzeClusters(b, DefaultAnalyzeOptions())
	if len(clusters) != 1 || clusters[0].Tag != "wine" || len(clusters[0].Nodes) != 3 {
		t.Fatalf("Wine/wine/WINE must still merge into one 3-node cluster labelled \"wine\", got %+v", clusters)
	}
}

// Distinct tags must not be merged by the fold (negative control for analyze).
func TestAnalyzeClusters_DistinctTagsNotMerged(t *testing.T) {
	b := mkLintBundle(t, map[string]string{
		"index.md": "---\ntype: Index\ntitle: Index\n---\n\n# Index\n\n- [A](a.md)\n- [B](b.md)\n- [C](c.md)\n",
		"a.md":     lintDocTags("Concept", "A", []string{"oauth1"}, "Body."),
		"b.md":     lintDocTags("Concept", "B", []string{"oauth2"}, "Body."),
		"c.md":     lintDocTags("Concept", "C", []string{"incident"}, "Body."),
	})
	clusters := analyzeClusters(b, DefaultAnalyzeOptions())
	for _, c := range clusters {
		if len(c.Nodes) >= 3 {
			t.Fatalf("distinct tags must not be folded into a cluster, got %+v", c)
		}
	}
}

// Clusters are ordered by their reported label. The fold key drops separators
// (e-reporting -> ereporting), so ordering by key would move e-reporting after
// earmark even though nothing was merged.
func TestAnalyzeClusters_OrderedByLabel(t *testing.T) {
	b := mkLintBundle(t, map[string]string{
		"index.md": "---\ntype: Index\ntitle: Index\n---\n\n# Index\n\n- [A](a.md)\n- [B](b.md)\n- [C](c.md)\n",
		"a.md":     lintDocTags("Concept", "A", []string{"e-reporting", "earmark"}, "Body."),
		"b.md":     lintDocTags("Concept", "B", []string{"e-reporting", "earmark"}, "Body."),
		"c.md":     lintDocTags("Concept", "C", []string{"e-reporting", "earmark"}, "Body."),
	})
	clusters := analyzeClusters(b, DefaultAnalyzeOptions())
	if len(clusters) != 2 || clusters[0].Tag != "e-reporting" || clusters[1].Tag != "earmark" {
		t.Fatalf("clusters must be ordered by label (e-reporting, earmark), got %+v", clusters)
	}
}
