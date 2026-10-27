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
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// A transient access-denied on Windows (another process holds the target
// open) is retried; elsewhere, and for any other error, it is not.
func TestWriteFileAtomic_RenameRetry(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.md")
	calls := 0
	rename = func(from, to string) error {
		calls++
		if calls <= 2 {
			return &os.LinkError{Op: "rename", Old: from, New: to, Err: fs.ErrPermission}
		}
		return os.Rename(from, to)
	}
	t.Cleanup(func() { rename = os.Rename })

	err := WriteFileAtomic(p, []byte("x"), 0o644)
	if runtime.GOOS == "windows" {
		if err != nil || calls != 3 {
			t.Fatalf("windows: err = %v after %d calls, want success on the 3rd", err, calls)
		}
	} else if err == nil || calls != 1 {
		t.Fatalf("%s: err = %v after %d calls, want one failing call", runtime.GOOS, err, calls)
	}
	noTemps(t, dir)
}

// Negative control: a non-permission failure is never retried.
func TestWriteFileAtomic_OtherRenameErrorNotRetried(t *testing.T) {
	dir := t.TempDir()
	calls := 0
	rename = func(string, string) error { calls++; return errors.New("disk on fire") }
	t.Cleanup(func() { rename = os.Rename })
	if err := WriteFileAtomic(filepath.Join(dir, "a.md"), []byte("x"), 0o644); err == nil {
		t.Fatal("expected the injected failure")
	}
	if calls != 1 {
		t.Fatalf("rename called %d times, want 1", calls)
	}
	noTemps(t, dir)
}

// ResolveInRoot accepts an in-bundle directory link, so the writer must work
// through one—a junction on unprivileged Windows, where an O_EXCL create under
// a junction fails. Both an existing and a new file are written.
func TestWriteFileAtomic_ThroughDirectoryLink(t *testing.T) {
	real := t.TempDir()
	if err := os.WriteFile(filepath.Join(real, "b.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "sub")
	if err := linkDir(real, link); err != nil {
		t.Skipf("directory links unsupported: %v", err)
	}
	for _, name := range []string{"b.md", "c.md"} {
		if err := WriteFileAtomic(filepath.Join(link, name), []byte("new"), 0o644); err != nil {
			t.Fatalf("write %s through a directory link: %v", name, err)
		}
		if got, _ := os.ReadFile(filepath.Join(real, name)); string(got) != "new" {
			t.Fatalf("%s = %q, want new", name, got)
		}
	}
	noTemps(t, real)
}
