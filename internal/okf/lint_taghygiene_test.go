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
	"reflect"
	"strings"
	"testing"
)

// lintDocTags builds a node with a type, title, and a YAML tags list, so tests
// can exercise the tag-hygiene fold the way the real corpus declares tags.
func lintDocTags(typ, title string, tags []string, body string) string {
	tl := "[" + strings.Join(tags, ", ") + "]"
	return "---\ntype: " + typ + "\ntitle: " + title + "\ntags: " + tl + "\n---\n\n# " + title + "\n\n" + body + "\n"
}

// The fold tables are the contract. canonFold is the type fold (case, trim,
// single trailing 's'). Tags fold through tagSurface (case, trim, separators)
// and tagKeys (KStem's guarded plural rules, validated against the bundle's own
// tag vocabulary).
func TestCanonFold_SharedBaseFold(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Concept", "concept"},
		{"concept", "concept"},
		{"Concepts", "concept"},  // single trailing 's' dropped
		{"  Wine  ", "wine"},     // trim
		{"WINE", "wine"},         // case
		{"runbooks", "runbook"},  // trailing 's'
		{"run-book", "run-book"}, // base fold does NOT strip separators
		{"s", "s"},               // len==1 guard: don't strip the only char
		{"gas", "ga"},            // trailing 's' dropped even mid-word (existing type behavior)
		{"", ""},
	}
	for _, c := range cases {
		if got := canonFold(c.in); got != c.want {
			t.Errorf("canonFold(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTagSurface_SeparatorInsensitive(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Wine", "wine"},
		{"  wine ", "wine"},
		{"runbooks", "runbooks"}, // no suffix is touched at this layer
		{"run-book", "runbook"},  // hyphen
		{"run_book", "runbook"},  // underscore
		{"Run Book", "runbook"},  // space + case
		{"ci/cd", "cicd"},        // slash
		{"node.js", "nodejs"},    // full stop between letters (UAX #29)
		{"asp.net", "aspnet"},
		{".net", ".net"}, // a leading full stop is kept: .net is not net
		{"v1.2", "v1.2"}, // a full stop between digits is kept: v1.2 is not v12
		{"c++", "c++"},
		{"-", ""},
	}
	for _, c := range cases {
		if got := tagSurface(c.in); got != c.want {
			t.Errorf("tagSurface(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// KStem's plural candidates, in order, with its guards: not letters-only, two
// letters or fewer, invariant forms, and a plain 's' after <= 3 letters, a double
// 's', or 'ous' produce no candidate.
func TestTagPluralCandidates_KStemRules(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"policies", []string{"policie", "policy"}},
		{"patches", []string{"patche", "patch"}},
		{"processes", []string{"process"}}, // double 's' before "es": no "-e" candidate
		{"caches", []string{"cache", "cach"}},
		{"runbooks", []string{"runbook"}},
		{"nodejs", []string{"nodej"}}, // only ever used if "nodej" is itself a tag
		{"aws", nil},                  // plain 's' after <= 3 letters
		{"ops", nil},
		{"ios", nil},
		{"js", nil},     // two letters
		{"k8s", nil},    // not letters-only
		{"news", nil},   // Porter2 invariant form
		{"atlas", nil},  // Porter2 invariant form
		{"class", nil},  // double 's'
		{"famous", nil}, // 'ous'
		{"wine", nil},   // no final 's'
	}
	for _, c := range cases {
		if got := tagPluralCandidates(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("tagPluralCandidates(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// tagKeys folds only onto an attested spelling, groups by connected component,
// and is independent of input order.
func TestTagKeys_AttestedFold(t *testing.T) {
	raws := []string{"policies", "policy", "caches", "cache", "nodejs", "node.js", "news", "new", "aws", "aw", "Wine", "wine", "lonely-plurals"}
	keys := tagKeys(raws)
	for _, p := range [][2]string{{"policies", "policy"}, {"caches", "cache"}, {"nodejs", "node.js"}, {"Wine", "wine"}} {
		if keys[p[0]] != keys[p[1]] {
			t.Errorf("%q and %q must share a key; got %q / %q", p[0], p[1], keys[p[0]], keys[p[1]])
		}
	}
	for _, p := range [][2]string{{"news", "new"}, {"aws", "aw"}} {
		if keys[p[0]] == keys[p[1]] {
			t.Errorf("%q and %q must NOT share a key; both got %q", p[0], p[1], keys[p[0]])
		}
	}
	// No attested singular: the plural keeps its own surface form.
	if keys["lonely-plurals"] != "lonelyplurals" {
		t.Errorf("unattested plural must not be stemmed; got %q", keys["lonely-plurals"])
	}
	// Order independence: reversed input, identical keys.
	rev := make([]string, len(raws))
	for i, r := range raws {
		rev[len(raws)-1-i] = r
	}
	if got := tagKeys(rev); !reflect.DeepEqual(got, keys) {
		t.Errorf("tagKeys must not depend on input order:\n%v\n%v", keys, got)
	}
}

// The tag fold must NOT change the type path. Types still fold with canonType
// (== canonFold), which is separator-SENSITIVE. Proven by table: a hyphenated
// and an unhyphenated type do NOT collapse under the type fold.
func TestCanonType_UnchangedBySeparatorRule(t *testing.T) {
	if canonType("run-book") == canonType("runbook") {
		t.Fatalf("type fold must stay separator-sensitive: run-book and runbook must NOT collapse")
	}
	if canonType("Concepts") != canonType("concept") {
		t.Fatalf("type fold must still be case+plural: Concepts and concept must collapse")
	}
}

// POSITIVE controls: each fires exactly one tag-hygiene finding.
func TestLint_TagHygiene_Positive(t *testing.T) {
	cases := []struct {
		name string
		tagA string
		tagB string
	}{
		{"case", "Wine", "wine"},
		{"plural", "runbook", "runbooks"},
		{"separator-hyphen", "run-book", "runbook"},
		{"separator-space", "Play Book", "playbook"},
		{"plural-es", "patch", "patches"},
		{"plural-sibilant-es", "process", "processes"},
		{"plural-ies", "policy", "policies"},
		{"separator-slash", "ci/cd", "cicd"},
		{"separator-dot", "node.js", "nodejs"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := mkLintBundle(t, map[string]string{
				"index.md": "---\ntype: Index\ntitle: Index\n---\n\n# Index\n\n- [A](a.md)\n- [B](b.md)\n",
				"a.md":     lintDocTags("Concept", "A", []string{c.tagA}, "Body."),
				"b.md":     lintDocTags("Concept", "B", []string{c.tagB}, "Body."),
			})
			th := findingsFor(Lint(b, LintOptions{}), "tag-hygiene")
			if len(th) != 1 {
				t.Fatalf("%s: expected one tag-hygiene finding for %q/%q, got %+v", c.name, c.tagA, c.tagB, th)
			}
			// Message lists both variants with node counts, and is bundle-level.
			if th[0].Path != "" {
				t.Fatalf("%s: tag-hygiene must be bundle-level (Path==\"\"), got %q", c.name, th[0].Path)
			}
			m := lc(th[0].Message)
			if !strings.Contains(m, lc(c.tagA)) || !strings.Contains(m, lc(c.tagB)) {
				t.Fatalf("%s: message must name both variants: %q", c.name, th[0].Message)
			}
		})
	}
}

// The message carries per-node counts and sorts the variants.
func TestLint_TagHygiene_MessageCountsAndSort(t *testing.T) {
	b := mkLintBundle(t, map[string]string{
		"index.md": "---\ntype: Index\ntitle: Index\n---\n\n# Index\n\n- [A](a.md)\n- [B](b.md)\n- [C](c.md)\n",
		// runbook x1 (a), runbooks x1 (b), run-book x1 (c) -> one folded group.
		"a.md": lintDocTags("Concept", "A", []string{"runbook"}, "Body."),
		"b.md": lintDocTags("Concept", "B", []string{"runbooks"}, "Body."),
		"c.md": lintDocTags("Concept", "C", []string{"run-book"}, "Body."),
	})
	th := findingsFor(Lint(b, LintOptions{}), "tag-hygiene")
	if len(th) != 1 {
		t.Fatalf("expected one folded tag-hygiene finding, got %+v", th)
	}
	const want = "tag-hygiene: near-duplicate tag values likely refer to one tag: run-book (1 node), runbook (1 node), runbooks (1 node)"
	if th[0].Message != want {
		t.Fatalf("tag-hygiene message:\n got: %q\nwant: %q", th[0].Message, want)
	}
}

// NEGATIVE controls: legitimately similar-looking tags stay silent.
func TestLint_TagHygiene_Negative(t *testing.T) {
	cases := []struct {
		name string
		tagA string
		tagB string
	}{
		{"version-digit", "v1", "v2"},
		{"oauth-digit", "oauth1", "oauth2"},
		{"distinct-words", "oncall", "incident"},
		{"leading-dot", ".net", "net"}, // a leading full stop is not a word-internal separator
		// Each pair below folded together under the previous bare trailing-'s'
		// rule; KStem's guards keep them apart.
		{"invariant-news", "new", "news"},
		{"short-ops", "op", "ops"},
		{"short-aws", "aw", "aws"},
		{"short-ios", "io", "ios"},
		{"short-dns", "dn", "dns"},
		{"short-bus", "bu", "bus"},
		{"two-letter-js", "j", "js"},
		{"digit-k8s", "k8", "k8s"},
		{"invariant-atlas", "atla", "atlas"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := mkLintBundle(t, map[string]string{
				"index.md": "---\ntype: Index\ntitle: Index\n---\n\n# Index\n\n- [A](a.md)\n- [B](b.md)\n",
				"a.md":     lintDocTags("Concept", "A", []string{c.tagA}, "Body."),
				"b.md":     lintDocTags("Concept", "B", []string{c.tagB}, "Body."),
			})
			if n := len(findingsFor(Lint(b, LintOptions{}), "tag-hygiene")); n != 0 {
				t.Fatalf("%s: %q/%q are distinct; expected 0 tag-hygiene findings, got %d", c.name, c.tagA, c.tagB, n)
			}
		})
	}
}

// A single tag spread over many nodes is not drift — it must stay silent.
func TestLint_TagHygiene_SingleTagManyNodesNoFinding(t *testing.T) {
	b := mkLintBundle(t, map[string]string{
		"index.md": "---\ntype: Index\ntitle: Index\n---\n\n# Index\n\n- [A](a.md)\n- [B](b.md)\n- [C](c.md)\n",
		"a.md":     lintDocTags("Concept", "A", []string{"wine"}, "Body."),
		"b.md":     lintDocTags("Concept", "B", []string{"wine"}, "Body."),
		"c.md":     lintDocTags("Concept", "C", []string{"wine"}, "Body."),
	})
	if n := len(findingsFor(Lint(b, LintOptions{}), "tag-hygiene")); n != 0 {
		t.Fatalf("one tag on many nodes is not drift; expected 0 tag-hygiene findings, got %d", n)
	}
}

// The shared-fold extraction must leave type-hygiene byte-identical. This pins
// the exact message string the check emitted before the refactor, proving the
// type path did not move when canonType became a thin canonFold wrapper.
func TestLint_TypeHygiene_MessageByteIdenticalAfterFoldExtraction(t *testing.T) {
	b := mkLintBundle(t, map[string]string{
		"index.md": "---\ntype: Index\ntitle: Index\n---\n\n# Index\n\n- [A](a.md)\n- [B](b.md)\n",
		"a.md":     lintDoc("Concept", "A", "Body."),
		"b.md":     lintDoc("Concepts", "B", "Body."),
	})
	th := findingsFor(Lint(b, LintOptions{}), "type-hygiene")
	if len(th) != 1 {
		t.Fatalf("expected one type-hygiene finding, got %+v", th)
	}
	const want = "type-hygiene: near-duplicate type values likely refer to one type: Concept, Concepts"
	if th[0].Message != want {
		t.Fatalf("type-hygiene message changed by fold extraction:\n got: %q\nwant: %q", th[0].Message, want)
	}
	// And a separator-only type pair must NOT collapse (types stay
	// separator-sensitive; the tag-only rule must not leak into the type path).
	b2 := mkLintBundle(t, map[string]string{
		"index.md": "---\ntype: Index\ntitle: Index\n---\n\n# Index\n\n- [A](a.md)\n- [B](b.md)\n",
		"a.md":     lintDoc("run-book", "A", "Body."),
		"b.md":     lintDoc("runbook", "B", "Body."),
	})
	if n := len(findingsFor(Lint(b2, LintOptions{}), "type-hygiene")); n != 0 {
		t.Fatalf("type-hygiene must stay separator-sensitive: run-book/runbook types must NOT fold, got %d findings", n)
	}
}
