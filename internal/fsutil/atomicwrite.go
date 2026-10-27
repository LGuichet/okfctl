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
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// rename is the final step of WriteFileAtomic; a variable only so a test can
// inject a failure and prove the target is left untouched.
var rename = os.Rename

// renameRetries bounds how often a Windows rename is retried while another
// process holds the target open (see renameWithRetry).
const renameRetries = 5

// WriteFileAtomic writes data to path so that a reader (or a crash) observes
// either the old content or the new content, never a truncated mix. It writes
// a temp file in path's own directory (same filesystem, so the rename is
// atomic), syncs and closes it, renames it over path, then syncs the directory
// so the rename itself survives a crash. On any error the temp file is removed
// and path is left exactly as it was.
//
// It keeps os.WriteFile's observable semantics where they matter:
//   - perm applies only when path does not exist, and is filtered by the
//     process umask exactly as os.WriteFile's is; an existing file keeps its
//     permission bits.
//   - a path that is a symlink is written through: the link's target is
//     replaced and the link itself survives.
//
// One deliberate difference: a hard-linked path is replaced, not written
// through, so its other links keep the old content. Atomicity requires a new
// inode; a bundle file with several hard links is outside what okfctl manages.
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
	// An existing file keeps its mode; a new one takes perm at creation,
	// through the umask.
	var keepMode os.FileMode
	exists := false
	if fi, err := os.Stat(target); err == nil {
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("write %s: not a regular file", path)
		}
		keepMode, exists = fi.Mode().Perm(), true
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", path, err)
	}

	dir, base := filepath.Split(target)
	if dir == "" {
		dir = "."
	}
	tmp, tmpName, err := createTemp(dir, base, perm)
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
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
	if exists {
		if err := os.Chmod(tmpName, keepMode); err != nil {
			return fmt.Errorf("chmod %s: %w", path, err)
		}
	}
	if err := renameWithRetry(tmpName, target); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	committed = true
	syncDir(dir)
	return nil
}

// createTemp makes ".<base>.tmp-<random>" in dir with perm, so a new file gets
// perm through the umask (os.CreateTemp would force 0600).
//
// On Windows, Go's O_EXCL create fails with "file exists" for any path under a
// directory junction—os.CreateTemp included—and ResolveInRoot accepts an
// in-bundle junction. There the name is checked free with Lstat and opened
// without O_EXCL; 64 random bits make a race with another creator moot.
func createTemp(dir, base string, perm os.FileMode) (*os.File, string, error) {
	flags := os.O_RDWR | os.O_CREATE | os.O_EXCL
	if runtime.GOOS == "windows" {
		flags = os.O_RDWR | os.O_CREATE | os.O_TRUNC
	}
	var lastErr error
	for range 10 {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, "", err
		}
		name := filepath.Join(dir, "."+base+".tmp-"+hex.EncodeToString(b[:]))
		if flags&os.O_EXCL == 0 {
			if _, err := os.Lstat(name); err == nil {
				lastErr = fs.ErrExist
				continue
			}
		}
		f, err := os.OpenFile(name, flags, perm) //nolint:gosec // G304: temp sibling of the caller's own target
		if err == nil {
			return f, name, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, "", err
		}
		lastErr = err
	}
	return nil, "", lastErr
}

// renameWithRetry renames, retrying briefly on Windows: there a rename over a
// file another process holds open (a concurrent okfctl serve, an indexer, an
// antivirus scan) fails with a transient access-denied/sharing error that an
// in-place write would not have hit.
func renameWithRetry(from, to string) error {
	err := rename(from, to)
	if runtime.GOOS != "windows" {
		return err
	}
	delay := 10 * time.Millisecond
	for i := 0; err != nil && errors.Is(err, fs.ErrPermission) && i < renameRetries; i++ {
		time.Sleep(delay)
		delay *= 2
		err = rename(from, to)
	}
	return err
}

// syncDir flushes dir's entries so a completed rename is durable. Best effort:
// Windows cannot open a directory for sync, and the data is already on disk.
func syncDir(dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	if d, err := os.Open(dir); err == nil { //nolint:gosec // G304: the caller's own target directory
		_ = d.Sync()
		_ = d.Close()
	}
}
