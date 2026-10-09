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

// shapeFixture builds the bundle every test here reads:
//
//	wine/tannin.md        Reference   tags: tannin, structure
//	wine/oak.md           Reference   tags: oak, structure
//	wine/red/nebbiolo.md  Grape       tags: grape, tannin
//	wine/red/barolo.md    Reference   tags: grape
//	lifting/squat.md      Playbook    (no tags)
//
// so `wine` has 2 own concepts and 4 in its subtree; among wine's OWN concepts
// only `structure` is shared, while `tannin` reaches two nodes only by counting
// the subtree.
func shapeFixture(t *testing.T) *Bundle {
	t.Helper()
	dir := t.TempDir()
	_ = Scaffold(dir)
	writeNodeTags(t, dir, "wine/tannin.md", "Reference", "Tannin", []string{"tannin", "structure"})
	writeNodeTags(t, dir, "wine/oak.md", "Reference", "Oak", []string{"oak", "structure"})
	writeNodeTags(t, dir, "wine/red/nebbiolo.md", "Grape", "Nebbiolo", []string{"grape", "tannin"})
	writeNodeTags(t, dir, "wine/red/barolo.md", "Reference", "Barolo", []string{"grape"})
	writeNodeTags(t, dir, "lifting/squat.md", "Playbook", "Squat", nil)
	b, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestShape_CountsOwnAndSubtree pins the two counts: Own is the directory's
// direct concepts, Subtree includes every descendant; a leaf directory has both
// equal; the root's subtree is the whole bundle.
func TestShape_CountsOwnAndSubtree(t *testing.T) {
	b := shapeFixture(t)
	opts := DefaultShapeOptions()
	if s := Shape(b, "wine", opts); s.Own != 2 || s.Subtree != 4 {
		t.Errorf("wine: own=%d subtree=%d, want 2/4", s.Own, s.Subtree)
	}
	if s := Shape(b, "wine/red", opts); s.Own != 2 || s.Subtree != 2 {
		t.Errorf("wine/red (leaf): own=%d subtree=%d, want 2/2", s.Own, s.Subtree)
	}
	if s := Shape(b, "", opts); s.Own != 0 || s.Subtree != 5 {
		t.Errorf("root: own=%d subtree=%d, want 0/5", s.Own, s.Subtree)
	}
}

// TestShape_TypesAndSharedTagsAreOwnConcepts pins the vocabulary rules, which
// are the index suffix's (§8): types and tags come from the directory's OWN
// concepts. Positive control: `structure`, on both own concepts, is shared.
// Negative controls: `oak` (one node) is not; `tannin` and the `Grape` type,
// which only the subtree carries, do not leak up.
func TestShape_TypesAndSharedTagsAreOwnConcepts(t *testing.T) {
	s := Shape(shapeFixture(t), "wine", DefaultShapeOptions())
	if want := []string{"Reference"}; !reflect.DeepEqual(s.Types, want) {
		t.Errorf("types = %v, want %v", s.Types, want)
	}
	if want := []string{"structure"}; !reflect.DeepEqual(s.SharedTags, want) {
		t.Errorf("shared tags = %v, want %v", s.SharedTags, want)
	}
	if s.TagsTruncated {
		t.Errorf("one qualifying tag under the default cap must not report truncation")
	}
}

// TestShape_TagCapReportsTruncation pins that TagMax caps SharedTags
// most-shared-first and that the cut is reported, so a consumer can mark it
// the way the index suffix does with its ellipsis.
func TestShape_TagCapReportsTruncation(t *testing.T) {
	b := shapeFixture(t)
	opts := DefaultShapeOptions()
	opts.TagMax = 1
	// wine/red's own concepts share only `grape`; widen the pool by lowering
	// the threshold so two tags qualify and the cap of 1 must cut one.
	opts.TagMin = 1
	s := Shape(b, "wine/red", opts)
	if want := []string{"grape"}; !reflect.DeepEqual(s.SharedTags, want) {
		t.Errorf("shared tags = %v, want %v (most-shared first)", s.SharedTags, want)
	}
	if !s.TagsTruncated {
		t.Errorf("cap below the qualifying count must report truncation")
	}
}

// TestShape_IsTheIndexSuffixData pins parity with the §8 index: the suffix
// `index build` writes for a subdirectory is rendered from Shape, so the data a
// summary command reads and the text a reader sees cannot disagree.
func TestShape_IsTheIndexSuffixData(t *testing.T) {
	b := shapeFixture(t)
	opts := DefaultShapeOptions()
	if got, want := dirShape(b, "wine", opts), " (2 concepts · Reference · shared tags: structure) · 4 in subtree"; got != want {
		t.Errorf("wine suffix\n got: %q\nwant: %q", got, want)
	}
	root := RenderDirIndex(b, "")
	if !strings.Contains(root, "* [Wine](wine/) (2 concepts · Reference · shared tags: structure) · 4 in subtree\n") {
		t.Errorf("root index must carry wine's shape; got:\n%s", root)
	}
}

// TestWriteIndex_WithShape_ValidatesAndStaysInSync pins the two properties the
// shape must not break: the generated tree still passes the spec floor (§8 index
// rules are untouched—the shape lives in entry text), and `index check` is
// clean immediately after `index build` (deterministic output).
func TestWriteIndex_WithShape_ValidatesAndStaysInSync(t *testing.T) {
	b := shapeFixture(t)
	if err := WriteIndex(b); err != nil {
		t.Fatal(err)
	}
	b2, err := Load(b.Root)
	if err != nil {
		t.Fatal(err)
	}
	if f := Validate(b2); len(f) != 0 {
		t.Errorf("index tree with shapes must validate clean; findings: %v", f)
	}
	if ok, report := IndexInSync(b2); !ok {
		t.Errorf("index check must be clean right after build; report: %s", report)
	}
}

// TestShapes_CoversEveryIndexDir pins that Shapes enumerates exactly IndexDirs,
// in order—the contract a summary command relies on.
func TestShapes_CoversEveryIndexDir(t *testing.T) {
	b := shapeFixture(t)
	got := Shapes(b, DefaultShapeOptions())
	dirs := IndexDirs(b)
	if len(got) != len(dirs) {
		t.Fatalf("Shapes returned %d entries for %d index dirs", len(got), len(dirs))
	}
	for i, d := range dirs {
		if got[i].Dir != d {
			t.Errorf("Shapes[%d].Dir = %q, want %q", i, got[i].Dir, d)
		}
	}
}
