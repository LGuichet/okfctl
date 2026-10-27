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
	"strings"
	"testing"
	"time"
)

// holdInode hard-links path into a directory outside the bundle and returns a
// check that the held copy still has the old bytes, i.e. that the writer
// replaced path by rename (atomic) rather than truncating it in place.
func holdInode(t *testing.T, path string) func() {
	t.Helper()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	held := filepath.Join(t.TempDir(), "held")
	if err := os.Link(path, held); err != nil {
		t.Skipf("hard links unsupported: %v", err)
	}
	return func() {
		t.Helper()
		got, err := os.ReadFile(held)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(before) {
			t.Errorf("%s was rewritten in place (not by atomic rename)", filepath.Base(path))
		}
		after, _ := os.ReadFile(path)
		if string(after) == string(before) {
			t.Errorf("%s was not rewritten at all", filepath.Base(path))
		}
		ents, _ := os.ReadDir(filepath.Dir(path))
		for _, e := range ents {
			if strings.Contains(e.Name(), ".tmp-") {
				t.Errorf("leftover temp file %s", e.Name())
			}
		}
	}
}

// nestedRoot returns (parent, root) where root is a bundle dir inside parent,
// so a write that escapes root by one ".." lands somewhere the test can see.
func nestedRoot(t *testing.T) (string, string) {
	t.Helper()
	parent := t.TempDir()
	root := filepath.Join(parent, "bundle")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return parent, root
}

func writeTestFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ---- atomic -----------------------------------------------------------------

func TestWriteSafety_TouchModifiedIsAtomic(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a.md", nodeSrc("A")+"body\n")
	writeTestFile(t, root, "plain.md", "no frontmatter\n")
	at := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	for _, rel := range []string{"a.md", "plain.md"} {
		p := filepath.Join(root, rel)
		check := holdInode(t, p)
		if err := TouchModifiedFile(p, at); err != nil {
			t.Fatal(err)
		}
		check()
	}
}

// v0.1 negative control: legacy `timestamp` survives a touch (§13 fallback).
func TestWriteSafety_TouchModifiedKeepsV01Provenance(t *testing.T) {
	root := t.TempDir()
	writeLegacyV01Node(t, root, "legacy.md", "Concept", "Legacy", nil)
	p := filepath.Join(root, "legacy.md")
	if err := TouchModifiedFile(p, time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	fm, body, err := ParseFrontmatter(raw)
	if err != nil {
		t.Fatal(err)
	}
	if fm["timestamp"] == nil || fm["modified"] == nil {
		t.Fatalf("legacy timestamp or modified missing: %v", fm)
	}
	if !strings.Contains(body, "# Citations") {
		t.Fatalf("legacy body citations lost: %q", body)
	}
}

func TestWriteSafety_ApplyMoveRewriteIsAtomic(t *testing.T) {
	root, b := mkMoveBundle(t, map[string]string{
		"a.md": nodeSrc("A"),
		"b.md": nodeSrc("B") + "See [A](a.md).\n",
	})
	rw, err := PlanMove(b, "a.md", "c.md")
	if err != nil {
		t.Fatal(err)
	}
	check := holdInode(t, filepath.Join(root, "b.md"))
	if err := ApplyMove(root, b, "a.md", "c.md", rw); err != nil {
		t.Fatal(err)
	}
	check()
}

func TestWriteSafety_PromoteApplyRewriteIsAtomic(t *testing.T) {
	root, b := mkPromoteBundle(t, map[string]string{
		"foo/index.md": dirConceptIndex("Foo", "2026-01-01", "# Foo\n"),
		"a.md":         nodeSrc("A") + "See [Foo](foo/index.md).\n",
	})
	changes, err := PromotePlan(b, "")
	if err != nil {
		t.Fatal(err)
	}
	check := holdInode(t, filepath.Join(root, "a.md"))
	if err := PromoteApply(root, b, changes); err != nil {
		t.Fatal(err)
	}
	check()
}

// Also the v0.1 control: a legacy node migrates through the atomic writer.
func TestWriteSafety_MigrateApplyIsAtomic(t *testing.T) {
	root := t.TempDir()
	writeLegacyV01Node(t, root, "legacy.md", "Concept", "Legacy", nil)
	writeTestFile(t, root, ".okf", "okf_version: 0.1\n")
	writeTestFile(t, root, "index.md", "---\nokf_version: \"0.1\"\n---\n\n# KB\n")
	b, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanMigration(b, "okfctl-test")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Nodes) == 0 {
		t.Fatal("expected the v0.1 node to be planned")
	}
	checks := []func(){
		holdInode(t, filepath.Join(root, "legacy.md")),
		holdInode(t, filepath.Join(root, ".okf")),
		holdInode(t, filepath.Join(root, "index.md")),
	}
	if err := MigrateApply(root, b, plan); err != nil {
		t.Fatal(err)
	}
	for _, c := range checks {
		c()
	}
	raw, _ := os.ReadFile(filepath.Join(root, "legacy.md"))
	fm, _, _ := ParseFrontmatter(raw)
	if fm["timestamp"] != nil || fm["generated"] == nil {
		t.Fatalf("v0.1 timestamp not migrated to generated.at: %v", fm)
	}
}

func TestWriteSafety_WriteIndexIsAtomic(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "index.md", "# stale\n")
	writeTestFile(t, root, "a.md", nodeSrc("A"))
	b, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	check := holdInode(t, filepath.Join(root, "index.md"))
	if err := WriteIndex(b); err != nil {
		t.Fatal(err)
	}
	check()
}

func TestWriteSafety_AppendLogIsAtomic(t *testing.T) {
	root := t.TempDir()
	if err := Scaffold(root); err != nil {
		t.Fatal(err)
	}
	check := holdInode(t, filepath.Join(root, "log.md"))
	if err := AppendLog(root, "an entry"); err != nil {
		t.Fatal(err)
	}
	check()
}
