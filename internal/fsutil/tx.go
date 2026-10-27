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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrChanged reports that a file a Tx would write or move no longer holds the
// bytes the caller planned against: someone edited it in between.
var ErrChanged = errors.New("file changed since the plan was made")

// txRename and txMkdirAll are a Tx's directory-entry steps; variables only so
// a test can inject a failure part-way through a commit.
var (
	txRename   = os.Rename
	txMkdirAll = os.MkdirAll
)

// SHA256Hex returns the lowercase hex SHA-256 of data.
func SHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Tx stages several file writes and renames and commits them as one unit: it
// checks every target against the hash the caller planned against before it
// writes anything, then applies the steps in order, and if a step fails it
// undoes the steps already applied, newest first, so the files end as they
// began.
//
// Each write is atomic on its own (WriteFileAtomic). The undo runs in-process
// from in-memory copies of the originals, so it covers a failed step, not a
// crash part-way through a commit.
type Tx struct {
	steps []txStep
}

type txStep struct {
	write  bool
	path   string // write target, or rename source
	to     string // rename destination
	data   []byte
	perm   os.FileMode
	expect string // planned SHA-256 of path's current bytes; "" skips the check
}

// Write stages writing data to path. expect is the SHA-256 (SHA256Hex) of the
// bytes the caller read and planned against; Commit refuses the whole Tx if
// path no longer hashes to it. An empty expect skips the check.
func (t *Tx) Write(path string, data []byte, perm os.FileMode, expect string) {
	t.steps = append(t.steps, txStep{write: true, path: path, data: data, perm: perm, expect: expect})
}

// Rename stages moving from to to, creating to's missing parent directories.
// expect checks from's bytes as Write's does; to must not exist.
func (t *Tx) Rename(from, to, expect string) {
	t.steps = append(t.steps, txStep{path: from, to: to, expect: expect})
}

// Commit checks every staged step, then applies them in order. A check
// failure writes nothing. An apply failure rolls back what was applied and
// returns the failure joined with any rollback error, so a rollback that could
// not finish is never silent.
func (t *Tx) Commit() error {
	for _, s := range t.steps {
		if err := s.check(); err != nil {
			return err
		}
	}
	var undo []func() error
	for _, s := range t.steps {
		// Check again right before the step: the window between the
		// up-front pass and this write is where a concurrent edit would land.
		u, err := s.apply()
		if err != nil {
			return errors.Join(err, rollback(undo))
		}
		undo = append(undo, u...)
	}
	return nil
}

func (s txStep) check() error {
	if s.expect != "" {
		data, err := os.ReadFile(s.path) //nolint:gosec // G304: the caller's own staged target
		if err != nil {
			return fmt.Errorf("check %s: %w", s.path, err)
		}
		if SHA256Hex(data) != s.expect {
			return fmt.Errorf("%s: %w", s.path, ErrChanged)
		}
	}
	if !s.write {
		if _, err := os.Lstat(s.to); err == nil {
			return fmt.Errorf("destination already exists: %s", s.to)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("stat %s: %w", s.to, err)
		}
	}
	return nil
}

// apply runs one step after re-checking it, and returns the closures that
// undo it.
func (s txStep) apply() ([]func() error, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	if s.write {
		return s.applyWrite()
	}
	return s.applyRename()
}

func (s txStep) applyWrite() ([]func() error, error) {
	orig, err := os.ReadFile(s.path) //nolint:gosec // G304: the caller's own staged target
	existed := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", s.path, err)
	}
	if err := WriteFileAtomic(s.path, s.data, s.perm); err != nil {
		return nil, err
	}
	path := s.path
	if !existed {
		return []func() error{func() error { return os.Remove(path) }}, nil
	}
	// WriteFileAtomic keeps the existing file's mode, so restoring the bytes
	// restores the file.
	return []func() error{func() error { return WriteFileAtomic(path, orig, 0o644) }}, nil //nolint:gosec // G306: the mode is kept from the existing file, perm is unused
}

func (s txStep) applyRename() ([]func() error, error) {
	var undo []func() error
	// Record the parents that do not exist yet, outermost first, so the undo
	// can remove exactly the directories this step created.
	var created []string
	for d := filepath.Dir(s.to); ; d = filepath.Dir(d) {
		if _, err := os.Lstat(d); err == nil {
			break
		}
		created = append([]string{d}, created...)
		if filepath.Dir(d) == d {
			break
		}
	}
	// Bundle directories hold shareable knowledge documents; 0o755 is intended.
	if err := txMkdirAll(filepath.Dir(s.to), 0o755); err != nil { //nolint:gosec // G301: shareable bundle content dir
		return nil, errors.Join(fmt.Errorf("mkdir for %s: %w", s.to, err), removeDirs(created))
	}
	if len(created) > 0 {
		undo = append(undo, func() error { return removeDirs(created) })
	}
	if err := txRename(s.path, s.to); err != nil {
		return nil, errors.Join(fmt.Errorf("move %s -> %s: %w", s.path, s.to, err), rollback(undo))
	}
	from, to := s.path, s.to
	undo = append(undo, func() error { return os.Rename(to, from) })
	return undo, nil
}

// removeDirs removes dirs innermost first; os.Remove refuses a directory that
// is not empty, so nothing that holds another file is ever deleted.
func removeDirs(dirs []string) error {
	var errs []error
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Remove(dirs[i]); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// rollback runs undo newest first and joins every failure, wrapped so the
// caller can tell a failed step from a failed restore.
func rollback(undo []func() error) error {
	var errs []error
	for i := len(undo) - 1; i >= 0; i-- {
		if err := undo[i](); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("rollback incomplete: %w", errors.Join(errs...))
}
