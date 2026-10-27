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

//go:build unix

package fsutil

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// A new file takes perm through the umask, as os.WriteFile's did. Not
// parallel: the umask is process-wide.
func TestWriteFileAtomic_NewFileHonoursUmask(t *testing.T) {
	old := syscall.Umask(0o027)
	t.Cleanup(func() { syscall.Umask(old) })

	p := filepath.Join(t.TempDir(), "a.md")
	if err := WriteFileAtomic(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(p)
	if got := fi.Mode().Perm(); got != 0o640 {
		t.Fatalf("mode = %v, want 0640 (0644 &^ umask 027)", got)
	}
	// Negative control: an existing file keeps its own mode, umask or not.
	if err := os.Chmod(p, 0o604); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(p, []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, _ = os.Stat(p)
	if got := fi.Mode().Perm(); got != 0o604 {
		t.Fatalf("existing mode = %v, want 0604 kept", got)
	}
}
