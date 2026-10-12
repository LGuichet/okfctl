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
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwest/okfctl/internal/fsutil"
)

// planAndApply plans a link and applies it, failing the test on any error.
func planAndApply(t *testing.T, root string, b *Bundle, from, to, section string) *LinkPlan {
	t.Helper()
	p, err := PlanLink(root, b, from, to, section)
	if err != nil {
		t.Fatalf("PlanLink(%s -> %s): %v", from, to, err)
	}
	if err := ApplyLink(p); err != nil {
		t.Fatalf("ApplyLink: %v", err)
	}
	return p
}

func readRel(t *testing.T, root, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// Positive control: a link lands as a bullet under a new "## Related" section
// at the end of the body, and the written destination resolves back to <to>
// through the shared resolver (so lint and the graph see the new edge).
func TestPlanLink_AddsUnderRelated(t *testing.T) {
	root, b := mkMoveBundle(t, map[string]string{
		"a.md": nodeSrc("A") + "Body of A.\n",
		"b.md": nodeSrc("Bee"),
	})
	p := planAndApply(t, root, b, "a.md", "b.md", "")
	if p.Exists || !p.CreatesSection || p.Section != DefaultLinkSection {
		t.Fatalf("plan = %+v, want new Related section", p)
	}
	want := nodeSrc("A") + "Body of A.\n\n## Related\n\n* [Bee](b.md)\n"
	if got := readRel(t, root, "a.md"); got != want {
		t.Fatalf("a.md =\n%q\nwant\n%q", got, want)
	}
	b2, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := b2.OutboundLinks("a.md"); len(got) != 1 || got[0] != "b.md" {
		t.Fatalf("OutboundLinks(a.md) = %v, want [b.md]", got)
	}
}

func TestPlanLink_AppendsToExistingSection(t *testing.T) {
	root, b := mkMoveBundle(t, map[string]string{
		"a.md": nodeSrc("A") + "Intro.\n\n## Related\n\n- [C](c.md)\n\n## Notes\n\nTail.\n",
		"b.md": nodeSrc("B"),
		"c.md": nodeSrc("C"),
	})
	p := planAndApply(t, root, b, "a.md", "b.md", "")
	if p.CreatesSection {
		t.Fatal("existing ## Related must be reused, not re-created")
	}
	// The section's own bullet marker ("-") is kept, and the bullet joins the
	// list before the next heading.
	want := nodeSrc("A") + "Intro.\n\n## Related\n\n- [C](c.md)\n- [B](b.md)\n\n## Notes\n\nTail.\n"
	if got := readRel(t, root, "a.md"); got != want {
		t.Fatalf("a.md =\n%q\nwant\n%q", got, want)
	}
}

func TestPlanLink_CustomSection(t *testing.T) {
	root, b := mkMoveBundle(t, map[string]string{
		"a.md": nodeSrc("A") + "## See also\n\nSome prose.\n",
		"b.md": nodeSrc("B"),
		"c.md": nodeSrc("C"),
	})
	planAndApply(t, root, b, "a.md", "b.md", "See also")
	want := nodeSrc("A") + "## See also\n\nSome prose.\n\n* [B](b.md)\n"
	if got := readRel(t, root, "a.md"); got != want {
		t.Fatalf("existing custom section:\n%q\nwant\n%q", got, want)
	}
	b2, _ := Load(root)
	p := planAndApply(t, root, b2, "a.md", "c.md", "Siblings")
	if !p.CreatesSection {
		t.Fatal("absent --section heading must be created")
	}
	if got := readRel(t, root, "a.md"); !strings.HasSuffix(got, "* [B](b.md)\n\n## Siblings\n\n* [C](c.md)\n") {
		t.Fatalf("new custom section not appended: %q", got)
	}
}

// §6.1 relative link: a nested node gets a "../" destination that resolves
// back to <to> from <from>'s directory.
func TestPlanLink_RelativeDestination_S6_1(t *testing.T) {
	root, b := mkMoveBundle(t, map[string]string{
		"x/deep/a.md": nodeSrc("A"),
		"y/b.md":      nodeSrc("B"),
	})
	p := planAndApply(t, root, b, "x/deep/a.md", "y/b.md", "")
	if p.Dest != "../../y/b.md" {
		t.Fatalf("Dest = %q, want ../../y/b.md", p.Dest)
	}
	b2, _ := Load(root)
	if got := b2.OutboundLinks("x/deep/a.md"); len(got) != 1 || got[0] != "y/b.md" {
		t.Fatalf("OutboundLinks = %v, want [y/b.md]", got)
	}
}

// A dir-relative destination that the shared resolver would read as a
// DIFFERENT root-level node falls back to the §6.1 "/"-absolute form, so the
// written link never lands on the wrong node.
func TestPlanLink_AmbiguousRelativeFallsBackToAbsolute_S6_1(t *testing.T) {
	root, b := mkMoveBundle(t, map[string]string{
		"sub/a.md": nodeSrc("A"),
		"sub/b.md": nodeSrc("Sub B"),
		"b.md":     nodeSrc("Root B"),
	})
	p := planAndApply(t, root, b, "sub/a.md", "sub/b.md", "")
	if p.Dest != "/sub/b.md" {
		t.Fatalf("Dest = %q, want /sub/b.md", p.Dest)
	}
	b2, _ := Load(root)
	if got := b2.OutboundLinks("sub/a.md"); len(got) != 1 || got[0] != "sub/b.md" {
		t.Fatalf("OutboundLinks = %v, want [sub/b.md]", got)
	}
}

// Negative control: every spelling of an existing link to <to> is detected,
// and the plan is a no-op that ApplyLink does not write.
func TestPlanLink_IdempotentSpellings(t *testing.T) {
	for _, link := range []string{
		"[b](../y/b.md)",
		"[b](./../y/b.md)",
		"[b](y/b.md)",
		"[b](/y/b.md)",
		"[b](../y/b.md#section)",
		"[b](/y/b.md#frag \"Title\")",
		"[b](../y/b.md \"Title\")",
	} {
		t.Run(link, func(t *testing.T) {
			src := nodeSrc("A") + "See " + link + ".\n"
			root, b := mkMoveBundle(t, map[string]string{
				"x/a.md": src,
				"y/b.md": nodeSrc("B"),
			})
			before, _ := os.Stat(filepath.Join(root, "x", "a.md"))
			p, err := PlanLink(root, b, "x/a.md", "y/b.md", "")
			if err != nil {
				t.Fatal(err)
			}
			if !p.Exists || p.Existing == "" {
				t.Fatalf("existing link %s not detected: %+v", link, p)
			}
			if err := ApplyLink(p); err != nil {
				t.Fatal(err)
			}
			if got := readRel(t, root, "x/a.md"); got != src {
				t.Fatalf("no-op rewrote the file: %q", got)
			}
			after, _ := os.Stat(filepath.Join(root, "x", "a.md"))
			if !after.ModTime().Equal(before.ModTime()) {
				t.Fatal("no-op touched the file mtime")
			}
		})
	}
}

// Positive half of the idempotency control: a link to a DIFFERENT node, a
// pure "#fragment" link, and an image of <to> are not mistaken for a link.
func TestPlanLink_SimilarButNotALinkStillAdds(t *testing.T) {
	root, b := mkMoveBundle(t, map[string]string{
		"a.md": nodeSrc("A") + "See [c](c.md), [here](#b), and ![pic](b.md).\n",
		"b.md": nodeSrc("B"),
		"c.md": nodeSrc("C"),
	})
	p, err := PlanLink(root, b, "a.md", "b.md", "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Exists {
		t.Fatalf("false already-linked: %+v", p)
	}
}

// Negative control: "## Related" inside a fenced code block is code, not a
// heading (§4.2 body is Markdown), so the real section is created after it.
func TestPlanLink_FencedHeadingIgnored(t *testing.T) {
	for _, fence := range []string{"```", "~~~"} {
		body := "Example:\n\n" + fence + "md\n## Related\n\n* not a list\n" + fence + "\n"
		root, b := mkMoveBundle(t, map[string]string{
			"a.md": nodeSrc("A") + body,
			"b.md": nodeSrc("B"),
		})
		p := planAndApply(t, root, b, "a.md", "b.md", "")
		if !p.CreatesSection {
			t.Fatalf("%s: fenced heading was taken as the section", fence)
		}
		want := nodeSrc("A") + body + "\n## Related\n\n* [B](b.md)\n"
		if got := readRel(t, root, "a.md"); got != want {
			t.Fatalf("%s:\n%q\nwant\n%q", fence, got, want)
		}
	}
}

// §3: both ends resolve inside the bundle root, with the error wrapped so a
// caller can errors.Is it. Nothing is written outside (or inside) the root.
func TestPlanLink_RefusesTraversal_S3(t *testing.T) {
	parent, root := nestedRoot(t)
	writeTestFile(t, root, "a.md", nodeSrc("A"))
	writeTestFile(t, parent, "out.md", nodeSrc("Out"))
	b, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ from, to string }{
		{"a.md", "../out.md"},
		{"../out.md", "a.md"},
		{"a.md", "sub/../../out.md"},
		{"a.md", filepath.Join(parent, "out.md")},
	} {
		_, err := PlanLink(root, b, c.from, c.to, "")
		if !errors.Is(err, fsutil.ErrEscapesRoot) {
			t.Errorf("PlanLink(%q -> %q) err = %v, want ErrEscapesRoot", c.from, c.to, err)
		}
	}
	_, err = PlanLink(root, b, "a.md", "b\nc.md", "")
	if !errors.Is(err, fsutil.ErrInvalidPath) {
		t.Errorf("control char path err = %v, want ErrInvalidPath", err)
	}
	if got := readRel(t, root, "a.md"); got != nodeSrc("A") {
		t.Fatalf("refused link wrote: %q", got)
	}
}

// §3.1/§8/§9: index.md and log.md are reserved at any level and are not
// concept documents—neither end of a link may be one.
func TestPlanLink_RefusesReserved_S3_1(t *testing.T) {
	root, b := mkMoveBundle(t, map[string]string{
		"a.md":         nodeSrc("A"),
		"index.md":     "# Index\n",
		"sub/log.md":   "# Log\n",
		"sub/index.md": "# Sub\n",
	})
	for _, c := range []struct{ from, to string }{
		{"a.md", "index.md"},
		{"index.md", "a.md"},
		{"a.md", "sub/log.md"},
		{"sub/index.md", "a.md"},
	} {
		if _, err := PlanLink(root, b, c.from, c.to, ""); err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Errorf("PlanLink(%s -> %s) err = %v, want reserved refusal", c.from, c.to, err)
		}
	}
}

func TestPlanLink_RefusesSelfMissingAndUnreadablePaths(t *testing.T) {
	root, b := mkMoveBundle(t, map[string]string{
		"a.md": nodeSrc("A"),
		"b.md": nodeSrc("B"),
	})
	cases := []struct{ from, to, want string }{
		{"a.md", "a.md", "itself"},
		{"a.md", "./a.md", "itself"},
		{"a.md", "missing.md", "not found"},
		{"missing.md", "a.md", "not found"},
		{"a.md", "has space.md", "cannot be written"},
		{"a.md", "paren(x).md", "cannot be written"},
	}
	for _, c := range cases {
		_, err := PlanLink(root, b, c.from, c.to, "")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("PlanLink(%s -> %s) err = %v, want %q", c.from, c.to, err, c.want)
		}
	}
	for _, sec := range []string{"## Related", "bad\nsection", "   "} {
		if _, err := PlanLink(root, b, "a.md", "b.md", sec); err == nil {
			t.Errorf("section %q must be refused", sec)
		}
	}
}

// Link text comes from <to>'s title. A "]" would end the link text early for
// the shared resolver (mdLinkRe), so square brackets become parentheses; a
// backslash is escaped so it cannot escape the closing "]"; control characters
// become spaces so the bullet stays on one line.
func TestPlanLink_EscapesTitle(t *testing.T) {
	root, b := mkMoveBundle(t, map[string]string{
		"a.md": nodeSrc("A"),
		"b.md": "---\ntype: Concept\ntitle: \"Weird [x] \\\\ \\t name\"\n---\n",
		"c.md": "---\ntype: Concept\n---\n",
	})
	p := planAndApply(t, root, b, "a.md", "b.md", "")
	if want := `* [Weird (x) \\   name](b.md)`; p.Line != want {
		t.Fatalf("Line = %q, want %q", p.Line, want)
	}
	b2, _ := Load(root)
	p2, err := PlanLink(root, b2, "a.md", "c.md", "")
	if err != nil {
		t.Fatal(err)
	}
	if p2.Line != "* [c](c.md)" {
		t.Fatalf("title fallback Line = %q, want file stem", p2.Line)
	}
	if b3, _ := Load(root); len(b3.OutboundLinks("a.md")) != 1 {
		t.Fatalf("escaped title broke the link: %v", b3.OutboundLinks("a.md"))
	}
}

// v0.1 fixture (§12 best-effort consumption): a node carrying the legacy
// `timestamp` key and a body `# Citations` list keeps its frontmatter
// byte-for-byte and its citations untouched; only the appended section is new.
func TestPlanLink_FrontmatterBytePreserved_V01(t *testing.T) {
	fm := "---\r\ntype: Metric\r\ntitle:   'Legacy'   # odd spacing kept\r\ntimestamp: '2026-05-28T22:53:05+00:00'\r\nzz_unknown: [1, 2]\r\n---\r\n"
	body := "\r\nThe metric.\r\n\r\n# Citations\r\n\r\n1. https://example.com/a\r\n"
	root, b := mkMoveBundle(t, map[string]string{
		"m.md": fm + body,
		"n.md": nodeSrc("N"),
	})
	planAndApply(t, root, b, "m.md", "n.md", "")
	got := readRel(t, root, "m.md")
	if !strings.HasPrefix(got, fm+body) {
		t.Fatalf("v0.1 frontmatter/citations not preserved:\n%q", got)
	}
	// CRLF file stays CRLF.
	if tail := got[len(fm+body):]; tail != "\r\n## Related\r\n\r\n* [N](n.md)\r\n" {
		t.Fatalf("appended tail = %q", tail)
	}
	b2, _ := Load(root)
	if _, ok := b2.Nodes["m.md"].Generated(); !ok {
		t.Fatal("legacy timestamp fallback lost after link")
	}
}

// A body that does not end in a newline still gets a well-formed section.
func TestPlanLink_NoTrailingNewline(t *testing.T) {
	root, b := mkMoveBundle(t, map[string]string{
		"a.md": nodeSrc("A") + "Last line",
		"b.md": nodeSrc("B"),
	})
	planAndApply(t, root, b, "a.md", "b.md", "")
	if got := readRel(t, root, "a.md"); got != nodeSrc("A")+"Last line\n\n## Related\n\n* [B](b.md)\n" {
		t.Fatalf("got %q", got)
	}
}

// ApplyLink refuses to clobber a file edited between plan and apply.
func TestApplyLink_RefusesConcurrentEdit(t *testing.T) {
	root, b := mkMoveBundle(t, map[string]string{
		"a.md": nodeSrc("A"),
		"b.md": nodeSrc("B"),
	})
	p, err := PlanLink(root, b, "a.md", "b.md", "")
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "a.md", nodeSrc("A")+"edited\n")
	if err := ApplyLink(p); err == nil {
		t.Fatal("ApplyLink over a changed file must fail")
	}
	if got := readRel(t, root, "a.md"); got != nodeSrc("A")+"edited\n" {
		t.Fatalf("concurrent edit clobbered: %q", got)
	}
}

// A link inside a fenced code block already counts as an edge for the graph
// scanner and so for lint's similar-unlinked; node link agrees and reports a
// no-op rather than adding a second link lint would see as redundant.
func TestPlanLink_FencedLinkCountsAsExisting(t *testing.T) {
	src := nodeSrc("A") + "```md\n[b](b.md)\n```\n"
	root, b := mkMoveBundle(t, map[string]string{
		"a.md": src,
		"b.md": nodeSrc("B"),
	})
	if !edgeExists(b, "a.md", "b.md") {
		t.Fatal("precondition: graph scanner should count the fenced link")
	}
	p, err := PlanLink(root, b, "a.md", "b.md", "")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Exists {
		t.Fatalf("fenced link not treated as existing: %+v", p)
	}
	if got := readRel(t, root, "a.md"); got != src {
		t.Fatalf("file changed: %q", got)
	}
}
