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

// Package fsutil holds okfctl's write-safety primitives: an atomic file
// writer and a bundle-root confinement check. It is a leaf package (standard
// library only) so every writer—bundle content in internal/okf, the vector
// store in internal/search, the user config in internal/okfconfig, and cmd—can
// share one implementation without importing each other.
package fsutil

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// rename is the final step of WriteFileAtomic; a variable only so a test can
// inject a failure and prove the target is left untouched.
var rename = os.Rename

// WriteFileAtomic writes data to path so that a reader (or a crash) observes
// either the old content or the new content, never a truncated mix. It writes
// a temp file in path's own directory (same filesystem, so the rename is
// atomic), syncs and closes it, then renames it over path. On any error the
// temp file is removed and path is left exactly as it was.
//
// It keeps os.WriteFile's observable semantics where they matter:
//   - perm applies only when path does not exist; an existing file keeps its
//     permission bits.
//   - a path that is a symlink is written through: the link's target is
//     replaced and the link itself survives.
//
// The temp name is ".<base>.tmp-<random>"—it never ends in ".md", so an
// orphan left by a hard crash is never loaded as a bundle node.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	target := path
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		resolved, rerr := filepath.EvalSymlinks(path)
		if rerr != nil {
			return fmt.Errorf("resolve symlink %s: %w", path, rerr)
		}
		target = resolved
	}
	mode := perm
	if fi, err := os.Stat(target); err == nil {
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("write %s: not a regular file", path)
		}
		mode = fi.Mode().Perm()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", path, err)
	}

	dir, base := filepath.Split(target)
	if dir == "" {
		dir = "."
	}
	tmp, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	if err := rename(tmpName, target); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	committed = true
	return nil
}
