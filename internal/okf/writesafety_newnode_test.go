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
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cwest/okfctl/internal/fsutil"
)

// linkDirForTest links a directory: a symlink where allowed, else (an
// unprivileged Windows account) a directory junction.
func linkDirForTest(target, link string) error {
	err := os.Symlink(target, link)
	if err == nil || runtime.GOOS != "windows" {
		return err
	}
	return exec.Command("cmd", "/c", "mklink", "/J", link, target).Run() //nolint:gosec // G204: test-only fixture setup
}

// ---- confined ---------------------------------------------------------------

// §3: a bundle is its directory tree. A symlinked (or junctioned) directory
// inside the root that points outside must not carry a new node out.
func TestWriteSafety_NewNodeRefusesLinkEscape(t *testing.T) {
	outside := t.TempDir()
	root := t.TempDir()
	if err := Scaffold(root); err != nil {
		t.Fatal(err)
	}
	if err := linkDirForTest(outside, filepath.Join(root, "out")); err != nil {
		t.Skipf("directory links unsupported: %v", err)
	}
	if _, err := NewNode(root, "out/evil", "Concept", "X"); !errors.Is(err, fsutil.ErrEscapesRoot) {
		t.Fatalf("NewNode through an escaping link: err = %v, want ErrEscapesRoot", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "evil.md")); err == nil {
		t.Fatal("a node was written outside the bundle root")
	}
}

// Negative control: an ordinary nested path, and a title carrying `---` in
// prose or a real `\n---\n`, write a node with only the expected keys (§4).
func TestWriteSafety_NewNodeNegativeControls(t *testing.T) {
	root := t.TempDir()
	if err := Scaffold(root); err != nil {
		t.Fatal(err)
	}
	for rel, title := range map[string]string{
		"a/b/c":   "Before --- after",
		"a/b/d":   "line\n---\ninjected: true",
		"/deep/e": "Plain",
	} {
		abs, err := NewNode(root, rel, "Concept", title)
		if err != nil {
			t.Fatalf("NewNode(%q): %v", rel, err)
		}
		want := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(rel, "/")+".md"))
		if abs != want {
			t.Errorf("NewNode(%q) wrote %s, want %s", rel, abs, want)
		}
		raw, _ := os.ReadFile(abs)
		fm, _, err := ParseFrontmatter(raw)
		if err != nil {
			t.Fatalf("parse %s: %v", rel, err)
		}
		if fm["injected"] != nil {
			t.Errorf("%s: title smuggled a frontmatter key: %v", rel, fm)
		}
		if fm["title"] != title {
			t.Errorf("%s: title = %q, want %q", rel, fm["title"], title)
		}
	}
}
