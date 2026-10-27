// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package okf

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cwest/okfctl/internal/fsutil"
)

// snapshotTree returns every file under root keyed by slash path, so a test
// can prove a refused or failed write left the tree byte-identical.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p) //nolint:gosec // G304: test-owned temp tree
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func assertTreeUnchanged(t *testing.T, root string, before map[string]string) {
	t.Helper()
	if after := snapshotTree(t, root); !reflect.DeepEqual(before, after) {
		t.Fatalf("tree changed:\nbefore %v\nafter  %v", before, after)
	}
}

// ---- node mv ----------------------------------------------------------------

func moveFixture(t *testing.T) (string, *Bundle) {
	return mkMoveBundle(t, map[string]string{
		"a.md":        nodeSrc("A") + "See [x](wine/foo.md).\n",
		"b.md":        nodeSrc("B") + "And [y](wine/foo.md).\n",
		"wine/c.md":   nodeSrc("C") + "Near [z](foo.md).\n",
		"wine/foo.md": nodeSrc("Foo"),
	})
}

// Positive control: a linking node edited after the load refuses the move
// before any rewrite lands, and the error names the edited node.
func TestApplyMove_RefusesLinkerEditedSinceLoad(t *testing.T) {
	root, b := moveFixture(t)
	rw, err := PlanMove(b, "wine/foo.md", "cellar/foo.md")
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "wine/c.md", nodeSrc("C")+"Near [z](foo.md). Edited.\n")
	before := snapshotTree(t, root)

	err = ApplyMove(root, b, "wine/foo.md", "cellar/foo.md", rw)
	if !errors.Is(err, fsutil.ErrChanged) || !strings.Contains(err.Error(), "wine/c.md") {
		t.Fatalf("err = %v, want ErrChanged naming wine/c.md", err)
	}
	assertTreeUnchanged(t, root, before)
}

// Positive control: the moved node itself edited after the load is refused too.
func TestApplyMove_RefusesMovedNodeEditedSinceLoad(t *testing.T) {
	root, b := moveFixture(t)
	rw, err := PlanMove(b, "wine/foo.md", "cellar/foo.md")
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "wine/foo.md", nodeSrc("Foo")+"New paragraph.\n")
	before := snapshotTree(t, root)

	if err := ApplyMove(root, b, "wine/foo.md", "cellar/foo.md", rw); !errors.Is(err, fsutil.ErrChanged) {
		t.Fatalf("err = %v, want ErrChanged", err)
	}
	assertTreeUnchanged(t, root, before)
}

// Positive control: a move whose destination directory cannot be created
// leaves every inbound link and the node exactly as they were. Before the
// transaction, on Windows the rewrites landed first and stayed when the
// directory step failed, leaving links to a node that never moved.
func TestApplyMove_FailedDirectoryLeavesTreeUnchanged(t *testing.T) {
	root, b := moveFixture(t)
	writeTestFile(t, root, "blocker", "a file where a directory is needed\n")
	rw, err := PlanMove(b, "wine/foo.md", "blocker/foo.md")
	if err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, root)

	if err := ApplyMove(root, b, "wine/foo.md", "blocker/foo.md", rw); err == nil {
		t.Fatal("expected the move to fail")
	}
	assertTreeUnchanged(t, root, before)
}

// Negative control: an untouched bundle moves and rewrites every link exactly
// as before the hash check existed.
func TestApplyMove_UnchangedBundleStillMoves(t *testing.T) {
	root, b := moveFixture(t)
	rw, err := PlanMove(b, "wine/foo.md", "cellar/foo.md")
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyMove(root, b, "wine/foo.md", "cellar/foo.md", rw); err != nil {
		t.Fatalf("ApplyMove: %v", err)
	}
	if got := readBody(t, root, "a.md"); !strings.Contains(got, "(cellar/foo.md)") {
		t.Errorf("a.md not rewritten: %q", got)
	}
	if got := readBody(t, root, "b.md"); !strings.Contains(got, "(cellar/foo.md)") {
		t.Errorf("b.md not rewritten: %q", got)
	}
	if got := readBody(t, root, "wine/c.md"); !strings.Contains(got, "(../cellar/foo.md)") {
		t.Errorf("wine/c.md not rewritten: %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "cellar", "foo.md")); err != nil {
		t.Fatalf("node not moved: %v", err)
	}
}

// Negative control: a node built in memory carries no hash, and the move is
// not refused for want of one.
func TestApplyMove_InMemoryNodeSkipsHashCheck(t *testing.T) {
	root, b := moveFixture(t)
	for _, n := range b.Nodes {
		n.SHA256 = ""
	}
	rw, err := PlanMove(b, "wine/foo.md", "cellar/foo.md")
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyMove(root, b, "wine/foo.md", "cellar/foo.md", rw); err != nil {
		t.Fatalf("ApplyMove: %v", err)
	}
}

// ---- migrate --apply ----------------------------------------------------------

func legacyMigrateFixture(t *testing.T) string {
	node := func(title string) string {
		return "---\ntype: Concept\ntitle: " + title + "\ntimestamp: '2026-05-28T22:53:05+00:00'\n---\n\n# " + title + "\n"
	}
	return mkMigrateBundle(t, map[string]string{
		".okf":     "okf_version: 0.1\n",
		"index.md": "---\nokf_version: \"0.1\"\n---\n\n# KB\n",
		"log.md":   "# Log\n",
		"a.md":     node("A"),
		"b.md":     node("B"),
		"c.md":     node("C"),
	})
}

// §13.1 migration plans carry the hash of each node they were computed from.
func TestMigratePlan_RecordsNodeHashes(t *testing.T) {
	dir := legacyMigrateFixture(t)
	plan, err := PlanMigration(loadMigrate(t, dir), "casey")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Nodes) != 3 {
		t.Fatalf("plan nodes = %d, want 3", len(plan.Nodes))
	}
	for _, nm := range plan.Nodes {
		raw, _ := os.ReadFile(filepath.Join(dir, nm.Path))
		if nm.SHA256 != fsutil.SHA256Hex(raw) {
			t.Errorf("%s: plan sha256 %q does not match the file", nm.Path, nm.SHA256)
		}
	}
	if got := UnhashedMigrateNodes(plan); len(got) != 0 {
		t.Errorf("UnhashedMigrateNodes = %v, want none", got)
	}
}

// Positive control: a node edited between phase 1 and --apply refuses the whole
// plan before any write, markers included, and the error names the node.
func TestMigrateApply_RefusesNodeEditedSincePlan(t *testing.T) {
	dir := legacyMigrateFixture(t)
	b := loadMigrate(t, dir)
	plan, err := PlanMigration(b, "casey")
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, dir, "c.md", "---\ntype: Concept\ntitle: C\ntimestamp: '2026-05-28T22:53:05+00:00'\n---\n\n# C\n\nEdited.\n")
	before := snapshotTree(t, dir)

	err = MigrateApply(dir, b, plan)
	if !errors.Is(err, fsutil.ErrChanged) || !strings.Contains(err.Error(), "c.md") {
		t.Fatalf("err = %v, want ErrChanged naming c.md", err)
	}
	if err := CheckMigratePlan(dir, plan); !errors.Is(err, fsutil.ErrChanged) {
		t.Fatalf("CheckMigratePlan err = %v, want ErrChanged", err)
	}
	assertTreeUnchanged(t, dir, before)
}

// Positive control: a plan that fails on its LAST node writes none of the
// earlier ones. Before the transaction, a.md and b.md were rewritten and the
// failure on c.md left the bundle half-migrated.
func TestMigrateApply_LateFailureWritesNothing(t *testing.T) {
	dir := legacyMigrateFixture(t)
	b := loadMigrate(t, dir)
	plan, err := PlanMigration(b, "casey")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "c.md")); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, dir)

	if err := MigrateApply(dir, b, plan); err == nil {
		t.Fatal("expected the apply to fail on the missing node")
	}
	assertTreeUnchanged(t, dir, before)
}

// Negative control: a plan written before per-node hashes existed still
// applies, and is reported as unchecked.
func TestMigrateApply_LegacyPlanWithoutHashesApplies(t *testing.T) {
	dir := legacyMigrateFixture(t)
	b := loadMigrate(t, dir)
	plan, err := PlanMigration(b, "casey")
	if err != nil {
		t.Fatal(err)
	}
	for i := range plan.Nodes {
		plan.Nodes[i].SHA256 = ""
	}
	if got := UnhashedMigrateNodes(plan); len(got) != 3 {
		t.Fatalf("UnhashedMigrateNodes = %v, want all 3", got)
	}
	if err := MigrateApply(dir, b, plan); err != nil {
		t.Fatalf("MigrateApply: %v", err)
	}
	b2 := loadMigrate(t, dir)
	if b2.OkfVersion != "0.2" {
		t.Errorf("okf_version = %q, want 0.2", b2.OkfVersion)
	}
	if f := Validate(b2); len(f) != 0 {
		t.Errorf("validate after migrate: %+v", f)
	}
}

// Negative control: an untouched bundle migrates to a valid v0.2 bundle with
// the hash check in place.
func TestMigrateApply_UnchangedBundleMigrates(t *testing.T) {
	dir := legacyMigrateFixture(t)
	b := loadMigrate(t, dir)
	plan, err := PlanMigration(b, "casey")
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckMigratePlan(dir, plan); err != nil {
		t.Fatalf("CheckMigratePlan: %v", err)
	}
	if err := MigrateApply(dir, b, plan); err != nil {
		t.Fatalf("MigrateApply: %v", err)
	}
	b2 := loadMigrate(t, dir)
	if b2.OkfVersion != "0.2" {
		t.Errorf("okf_version = %q, want 0.2", b2.OkfVersion)
	}
	if f := Validate(b2); len(f) != 0 {
		t.Errorf("validate after migrate: %+v", f)
	}
	if raw, _ := os.ReadFile(filepath.Join(dir, ".okf")); string(raw) != "okf_version: 0.2\n" {
		t.Errorf(".okf = %q", raw)
	}
}
