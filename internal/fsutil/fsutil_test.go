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

package fsutil

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func noTemps(t *testing.T, dir string) {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("leftover temp file %s", e.Name())
		}
	}
}

func TestWriteFileAtomic_CreatesAndReplaces(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.md")
	if err := WriteFileAtomic(p, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(p, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "two" {
		t.Fatalf("content = %q, want two", got)
	}
	noTemps(t, dir)
}

// The defining property: the write replaces the file by rename, so a reader
// holding the old inode (here, a hard link) never sees a partial or new body.
func TestWriteFileAtomic_ReplacesByRename(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.md")
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "held")
	if err := os.Link(p, link); err != nil {
		t.Skipf("hard links unsupported: %v", err)
	}
	if err := WriteFileAtomic(p, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(link); string(got) != "old" {
		t.Fatalf("old inode was written in place: %q", got)
	}
	if got, _ := os.ReadFile(p); string(got) != "new" {
		t.Fatalf("target = %q, want new", got)
	}
}

func TestWriteFileAtomic_FailureLeavesTargetIntact(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.md")
	if err := os.WriteFile(p, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	rename = func(string, string) error { return errors.New("injected") }
	t.Cleanup(func() { rename = os.Rename })
	if err := WriteFileAtomic(p, []byte("replacement"), 0o644); err == nil {
		t.Fatal("expected the injected failure")
	}
	if got, _ := os.ReadFile(p); string(got) != "original" {
		t.Fatalf("target changed on failure: %q", got)
	}
	noTemps(t, dir)
}

func TestWriteFileAtomic_MissingDirFails(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nope", "a.md")
	if err := WriteFileAtomic(p, []byte("x"), 0o644); err == nil {
		t.Fatal("expected an error writing into a missing directory")
	}
}

func TestWriteFileAtomic_KeepsExistingMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not modelled on Windows")
	}
	p := filepath.Join(t.TempDir(), "cfg.json")
	if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(p, []byte("{1}"), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600 kept", fi.Mode().Perm())
	}
}

func TestWriteFileAtomic_WritesThroughSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.md")
	if err := os.WriteFile(real, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.md")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	if err := WriteFileAtomic(link, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink was replaced by a regular file")
	}
	if got, _ := os.ReadFile(real); string(got) != "new" {
		t.Fatalf("link target = %q, want new", got)
	}
}

func TestResolveInRoot_Accepts(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{
		"a/b/c.md":        filepath.Join(root, "a", "b", "c.md"),
		"new/dir/x.md":    filepath.Join(root, "new", "dir", "x.md"),
		"/deep/x.md":      filepath.Join(root, "deep", "x.md"), // §6.1 "/" = bundle root
		"a/../a/b/c.md":   filepath.Join(root, "a", "b", "c.md"),
		"./a/b/c.md":      filepath.Join(root, "a", "b", "c.md"),
		"a/b/--- note.md": filepath.Join(root, "a", "b", "--- note.md"),
	} {
		got, err := ResolveInRoot(root, in)
		if err != nil {
			t.Errorf("ResolveInRoot(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ResolveInRoot(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveInRoot_Refuses(t *testing.T) {
	root := t.TempDir()
	cases := map[string]error{
		"":               ErrInvalidPath,
		".":              ErrInvalidPath,
		"/":              ErrInvalidPath,
		"a/..":           ErrInvalidPath,
		"a\nb.md":        ErrInvalidPath,
		"a\rb.md":        ErrInvalidPath,
		"a\x7fb.md":      ErrInvalidPath,
		"..":             ErrEscapesRoot,
		"../x.md":        ErrEscapesRoot,
		"../../x.md":     ErrEscapesRoot,
		"sub/../../x.md": ErrEscapesRoot,
		"/../x.md":       ErrEscapesRoot,
		"a/b/../../../x": ErrEscapesRoot,
	}
	for in, want := range cases {
		_, err := ResolveInRoot(root, in)
		if !errors.Is(err, want) {
			t.Errorf("ResolveInRoot(%q) err = %v, want %v", in, err, want)
		}
	}
	if runtime.GOOS == "windows" {
		for _, in := range []string{`..\x.md`, `C:\Windows\x`, `\\srv\share\x`, `C:x.md`} {
			if _, err := ResolveInRoot(root, in); !errors.Is(err, ErrEscapesRoot) {
				t.Errorf("ResolveInRoot(%q) err = %v, want ErrEscapesRoot", in, err)
			}
		}
	}
}

func TestResolveInRoot_SymlinkEscapeRefused(t *testing.T) {
	outside := t.TempDir()
	root := t.TempDir()
	if err := linkDir(outside, filepath.Join(root, "out")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	for _, in := range []string{"out/x.md", "out/new/dir/x.md", "out"} {
		if _, err := ResolveInRoot(root, in); !errors.Is(err, ErrEscapesRoot) {
			t.Errorf("ResolveInRoot(%q) err = %v, want ErrEscapesRoot", in, err)
		}
	}
}

func TestResolveInRoot_InBundleSymlinkAccepted(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := linkDir(filepath.Join(root, "real"), filepath.Join(root, "alias")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	got, err := ResolveInRoot(root, "alias/x.md")
	if err != nil {
		t.Fatalf("in-bundle symlink refused: %v", err)
	}
	if want := filepath.Join(root, "alias", "x.md"); got != want {
		t.Fatalf("got %q, want lexical %q", got, want)
	}
}

// linkDir links a directory: a symlink where the OS allows it, otherwise (an
// unprivileged Windows account) a directory junction, which needs no
// privilege and is followed by filepath.EvalSymlinks just the same.
func linkDir(target, link string) error {
	err := os.Symlink(target, link)
	if err == nil || runtime.GOOS != "windows" {
		return err
	}
	return exec.Command("cmd", "/c", "mklink", "/J", link, target).Run() //nolint:gosec // G204: test-only fixture setup
}
