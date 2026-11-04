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
	"encoding/json"
	"strings"
	"testing"
)

// staleAfterDoc is a fresh node (generated.at a day before fixedNow, so the
// age-based signals stay silent) carrying the given stale_after line, if any.
func staleAfterDoc(title, staleAfter string) string {
	fm := "---\ntype: Concept\ntitle: " + title + "\n" +
		"generated: { by: test, at: 2026-07-25T00:00:00Z }\n"
	if staleAfter != "" {
		fm += "stale_after: " + staleAfter + "\n"
	}
	return fm + "---\n\n# " + title + "\n\nBody.\n"
}

func expiredPaths(rep AnalyzeReport) []string {
	var out []string
	for _, e := range rep.Freshness.Expired {
		out = append(out, e.Path)
	}
	return out
}

// Positive control: a passed stale_after is reported, with the declared value,
// even though the node's age basis is a day old and never reaches --stale-days.
func TestAnalyze_Expired_PastStaleAfter_Section5_5(t *testing.T) {
	pinClock(t, fixedNow) // 2026-07-26T12:00Z
	b := mkLintBundle(t, map[string]string{
		"index.md": "---\ntype: Index\ntitle: Index\n---\n\n# Index\n\n- [P](past.md)\n",
		"past.md":  staleAfterDoc("P", "2026-07-01T00:00:00Z"),
	})
	rep := Analyze(b, DefaultAnalyzeOptions())
	if got := expiredPaths(rep); len(got) != 1 || got[0] != "past.md" {
		t.Fatalf("want past.md expired, got %+v", rep.Freshness.Expired)
	}
	if !strings.HasPrefix(rep.Freshness.Expired[0].StaleAfter, "2026-07-01") {
		t.Fatalf("want the declared stale_after echoed, got %q", rep.Freshness.Expired[0].StaleAfter)
	}
	if len(rep.Freshness.Stale) != 0 {
		t.Fatalf("expired must not leak into the age-based stale list, got %+v", rep.Freshness.Stale)
	}
}

// §5.5 is `now >= stale_after`: the exact instant is already stale.
func TestAnalyze_Expired_BoundaryIsStale_Section5_5(t *testing.T) {
	pinClock(t, fixedNow)
	b := mkLintBundle(t, map[string]string{
		"index.md": "---\ntype: Index\ntitle: Index\n---\n\n# Index\n\n- [E](edge.md)\n",
		"edge.md":  staleAfterDoc("E", "2026-07-26T12:00:00Z"),
	})
	if got := expiredPaths(Analyze(b, DefaultAnalyzeOptions())); len(got) != 1 {
		t.Fatalf("want edge.md expired at now == stale_after, got %v", got)
	}
}

// Negative controls: a future date, an absent key and an unparseable value
// stay silent. The last is §11 tolerance: an optional key is never a failure.
func TestAnalyze_Expired_NegativeControls_Section5_5(t *testing.T) {
	pinClock(t, fixedNow)
	b := mkLintBundle(t, map[string]string{
		"index.md":  "---\ntype: Index\ntitle: Index\n---\n\n# Index\n\n- [F](future.md)\n- [N](none.md)\n- [J](junk.md)\n- [S](second.md)\n",
		"future.md": staleAfterDoc("F", "2026-12-31T00:00:00Z"),
		"none.md":   staleAfterDoc("N", ""),
		"junk.md":   staleAfterDoc("J", "next quarter"),
		"second.md": staleAfterDoc("S", "2026-07-26T12:00:01Z"),
	})
	if got := expiredPaths(Analyze(b, DefaultAnalyzeOptions())); len(got) != 0 {
		t.Fatalf("want no expired nodes, got %v", got)
	}
}

// The JSON contract: `expired` is always an array, never null, so a consumer
// can range over it without a nil check (same rule as stale/time_sensitive).
func TestAnalyze_Expired_JSONAlwaysArray(t *testing.T) {
	pinClock(t, fixedNow)
	b := mkLintBundle(t, map[string]string{
		"index.md": "---\ntype: Index\ntitle: Index\n---\n\n# Index\n\n- [N](none.md)\n",
		"none.md":  staleAfterDoc("N", ""),
	})
	raw, err := json.Marshal(Analyze(b, DefaultAnalyzeOptions()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"expired":[]`) {
		t.Fatalf("want \"expired\":[] in JSON, got %s", raw)
	}
}
