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
	"runtime"
	"strings"
	"testing"
)

// §3.1: a user-typed path is recognized as reserved once cleaned the way the
// write resolves it.
func TestWriteSafety_IsReservedPathCleansFirst(t *testing.T) {
	for _, p := range []string{"index.md", "a/log.md", "a/./index.md", "a/b/../index.md"} {
		if !IsReservedPath(p) {
			t.Errorf("IsReservedPath(%q) = false, want true", p)
		}
	}
	// Negative control: look-alikes stay ordinary nodes.
	for _, p := range []string{"index.mdx", "a/index.md.bak", "indexes.md", "a/index/x.md"} {
		if IsReservedPath(p) {
			t.Errorf("IsReservedPath(%q) = true, want false", p)
		}
	}
	// A backslash is a separator only on Windows; elsewhere it is an ordinary
	// file-name byte, so `a\index.md` is a node named that way.
	if got, want := IsReservedPath(`a\index.md`), runtime.GOOS == "windows"; got != want {
		t.Errorf(`IsReservedPath("a\\index.md") = %v, want %v on %s`, got, want, runtime.GOOS)
	}
}

// caseInsensitiveFS reports whether dir's filesystem folds case.
func caseInsensitiveFS(t *testing.T, dir string) bool {
	t.Helper()
	p := filepath.Join(dir, "CaseProbe")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(p) }()
	_, err := os.Stat(filepath.Join(dir, "caseprobe"))
	return err == nil
}

// --name Index on a case-insensitive filesystem names the very index.md being
// promoted; writing it and then removing the old index would lose the content.
func TestWriteSafety_PromoteRefusesCaseFoldedReservedName(t *testing.T) {
	src := dirConceptIndex("Foo", "2026-01-01", "# Foo body\n")
	root, b := mkPromoteBundle(t, map[string]string{"foo/index.md": src})
	if !caseInsensitiveFS(t, root) {
		t.Skip("filesystem is case-sensitive: foo/Index.md is a distinct, legal node")
	}
	for _, name := range []string{"Index", "LOG"} {
		if name == "LOG" {
			writeTestFile(t, root, "foo/log.md", "# Log\n")
		}
		changes, err := PromotePlan(b, name)
		if err != nil {
			continue // refused at plan time is fine too
		}
		if err := PromoteApply(root, b, changes); err == nil {
			t.Errorf("PromoteApply(--name %s) must be refused on a case-insensitive filesystem", name)
		}
	}
	got, err := os.ReadFile(filepath.Join(root, "foo", "index.md"))
	if err != nil || string(got) != src {
		t.Fatalf("index.md lost or altered by a refused promotion: %q, %v", got, err)
	}
}

// PromotePlan only knows loaded nodes; a file that appeared on disk since (or
// was never loaded) refuses PromoteApply before any inbound link is rewritten.
func TestWriteSafety_PromoteRefusesExistingDiskFile(t *testing.T) {
	inbound := nodeSrc("Ref") + "See [Foo](foo/index.md).\n"
	root, b := mkPromoteBundle(t, map[string]string{
		"foo/index.md": dirConceptIndex("Foo", "2026-01-01", "# Foo\n"),
		"ref.md":       inbound,
	})
	changes, err := PromotePlan(b, "")
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "foo/foo.md", "authored meanwhile\n")
	if err := PromoteApply(root, b, changes); err == nil {
		t.Fatal("PromoteApply over an existing file must be refused")
	}
	if got, _ := os.ReadFile(filepath.Join(root, "foo", "foo.md")); string(got) != "authored meanwhile\n" {
		t.Fatalf("existing file overwritten: %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "foo", "index.md")); err != nil {
		t.Fatal("old index removed by a refused promotion")
	}
	if got, _ := os.ReadFile(filepath.Join(root, "ref.md")); string(got) != inbound {
		t.Fatalf("inbound link rewritten by a refused promotion: %q", got)
	}
}

// Negative control: an ordinary promotion with a free destination still runs.
func TestWriteSafety_PromoteFreeDestinationStillWorks(t *testing.T) {
	root, b := mkPromoteBundle(t, map[string]string{
		"foo/index.md": dirConceptIndex("Foo", "2026-01-01", "# Foo\n"),
	})
	changes, err := PromotePlan(b, "overview")
	if err != nil {
		t.Fatal(err)
	}
	if err := PromoteApply(root, b, changes); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "foo", "overview.md")); err != nil {
		t.Fatal(err)
	}
}

// §3: a node rewritten by a move is written too, so a node that is a symlink
// out of the bundle must not be written through.
func TestWriteSafety_ApplyMoveRefusesRewriteThroughSymlink(t *testing.T) {
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "b.md")
	outsideSrc := nodeSrc("Outside") + "See [A](../a.md).\n"
	if err := os.WriteFile(outside, []byte(outsideSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	root, b := mkMoveBundle(t, map[string]string{
		"a.md":     nodeSrc("A"),
		"sub/b.md": nodeSrc("B") + "See [A](../a.md).\n",
	})
	rw, err := PlanMove(b, "a.md", "moved.md")
	if err != nil {
		t.Fatal(err)
	}
	// Swap sub/ for a directory link out of the bundle after planning (a
	// junction on unprivileged Windows, which EvalSymlinks does not follow).
	if err := os.RemoveAll(filepath.Join(root, "sub")); err != nil {
		t.Fatal(err)
	}
	if err := linkDirForTest(outsideDir, filepath.Join(root, "sub")); err != nil {
		t.Skipf("directory links unsupported: %v", err)
	}
	if err := ApplyMove(root, b, "a.md", "moved.md", rw); err == nil {
		t.Fatal("ApplyMove must refuse to rewrite a node that links out of the bundle")
	}
	if got, _ := os.ReadFile(outside); string(got) != outsideSrc {
		t.Fatalf("file outside the bundle rewritten: %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "a.md")); err != nil {
		t.Fatal("source moved by a refused move")
	}
}

// Negative control: a rewritten node in a subdirectory is still confined in.
func TestWriteSafety_ApplyMoveNestedRewriteStillWorks(t *testing.T) {
	root, b := mkMoveBundle(t, map[string]string{
		"a.md":      nodeSrc("A"),
		"real/b.md": nodeSrc("B") + "See [A](../a.md).\n",
	})
	rw, err := PlanMove(b, "a.md", "moved.md")
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyMove(root, b, "a.md", "moved.md", rw); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "real", "b.md"))
	if !strings.Contains(string(got), "moved.md") {
		t.Fatalf("inbound link not rewritten: %q", got)
	}
}
