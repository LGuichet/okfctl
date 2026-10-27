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
	"path/filepath"
	"strings"
	"testing"
)

func writeTx(t *testing.T, dir, name, content string) (string, string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p, SHA256Hex([]byte(content))
}

func readTx(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p) //nolint:gosec // G304: test temp file
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Negative control: an untouched set of files commits every write and the
// rename, exactly as staged.
func TestTx_CommitsWhenNothingChanged(t *testing.T) {
	dir := t.TempDir()
	a, ha := writeTx(t, dir, "a.md", "A")
	b, hb := writeTx(t, dir, "b.md", "B")
	m, hm := writeTx(t, dir, "m.md", "M")
	to := filepath.Join(dir, "new", "deep", "m.md")

	var tx Tx
	tx.Write(a, []byte("A2"), 0o644, ha)
	tx.Write(b, []byte("B2"), 0o644, hb)
	tx.Rename(m, to, hm)
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if readTx(t, a) != "A2" || readTx(t, b) != "B2" || readTx(t, to) != "M" {
		t.Fatal("staged steps not all applied")
	}
	if _, err := os.Stat(m); !os.IsNotExist(err) {
		t.Fatalf("rename source still present: %v", err)
	}
	noTemps(t, dir)
}

// Positive control: a target edited after the plan refuses the whole Tx before
// any write, and the error names the changed file.
func TestTx_RefusesDriftBeforeAnyWrite(t *testing.T) {
	dir := t.TempDir()
	a, ha := writeTx(t, dir, "a.md", "A")
	b, hb := writeTx(t, dir, "b.md", "B")
	if err := os.WriteFile(b, []byte("B edited"), 0o644); err != nil {
		t.Fatal(err)
	}

	var tx Tx
	tx.Write(a, []byte("A2"), 0o644, ha)
	tx.Write(b, []byte("B2"), 0o644, hb)
	err := tx.Commit()
	if !errors.Is(err, ErrChanged) {
		t.Fatalf("err = %v, want ErrChanged", err)
	}
	if got := err.Error(); !strings.Contains(got, b) {
		t.Fatalf("error %q does not name %s", got, b)
	}
	if readTx(t, a) != "A" || readTx(t, b) != "B edited" {
		t.Fatal("a refused Tx wrote a file")
	}
}

// Positive control: a rename that fails after two writes landed restores both
// writes byte-for-byte and leaves the source where it was.
func TestTx_RollsBackWritesWhenRenameFails(t *testing.T) {
	dir := t.TempDir()
	a, ha := writeTx(t, dir, "a.md", "A")
	b, hb := writeTx(t, dir, "b.md", "B")
	m, hm := writeTx(t, dir, "m.md", "M")
	to := filepath.Join(dir, "new", "m.md")
	txRename = func(string, string) error { return errors.New("disk on fire") }
	t.Cleanup(func() { txRename = os.Rename })

	var tx Tx
	tx.Write(a, []byte("A2"), 0o644, ha)
	tx.Write(b, []byte("B2"), 0o644, hb)
	tx.Rename(m, to, hm)
	err := tx.Commit()
	if err == nil || !strings.Contains(err.Error(), "disk on fire") {
		t.Fatalf("err = %v, want the injected failure", err)
	}
	if readTx(t, a) != "A" || readTx(t, b) != "B" || readTx(t, m) != "M" {
		t.Fatal("rollback left a file changed")
	}
	if _, err := os.Stat(filepath.Join(dir, "new")); !os.IsNotExist(err) {
		t.Fatalf("rollback left the created directory: %v", err)
	}
	noTemps(t, dir)
}

// Positive control: a failed directory creation rolls back the writes before it.
func TestTx_RollsBackWritesWhenMkdirFails(t *testing.T) {
	dir := t.TempDir()
	a, ha := writeTx(t, dir, "a.md", "A")
	m, hm := writeTx(t, dir, "m.md", "M")
	txMkdirAll = func(string, os.FileMode) error { return errors.New("no space") }
	t.Cleanup(func() { txMkdirAll = os.MkdirAll })

	var tx Tx
	tx.Write(a, []byte("A2"), 0o644, ha)
	tx.Rename(m, filepath.Join(dir, "new", "m.md"), hm)
	if err := tx.Commit(); err == nil {
		t.Fatal("expected the injected failure")
	}
	if readTx(t, a) != "A" || readTx(t, m) != "M" {
		t.Fatal("rollback left a file changed")
	}
}

// A write that creates a new file is undone by removing it.
func TestTx_RollbackRemovesCreatedFile(t *testing.T) {
	dir := t.TempDir()
	created := filepath.Join(dir, "created.md")
	m, hm := writeTx(t, dir, "m.md", "M")
	txRename = func(string, string) error { return errors.New("disk on fire") }
	t.Cleanup(func() { txRename = os.Rename })

	var tx Tx
	tx.Write(created, []byte("new"), 0o644, "")
	tx.Rename(m, filepath.Join(dir, "m2.md"), hm)
	if err := tx.Commit(); err == nil {
		t.Fatal("expected the injected failure")
	}
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		t.Fatalf("rollback left the created file: %v", err)
	}
}

// Negative control: an existing directory that the rename did not create is
// never removed by a rollback, even when it ends up empty.
func TestTx_RollbackKeepsPreexistingDir(t *testing.T) {
	dir := t.TempDir()
	keep := filepath.Join(dir, "keep")
	if err := os.Mkdir(keep, 0o755); err != nil {
		t.Fatal(err)
	}
	m, hm := writeTx(t, dir, "m.md", "M")
	txRename = func(string, string) error { return errors.New("disk on fire") }
	t.Cleanup(func() { txRename = os.Rename })

	var tx Tx
	tx.Rename(m, filepath.Join(keep, "m.md"), hm)
	if err := tx.Commit(); err == nil {
		t.Fatal("expected the injected failure")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("rollback removed a directory it did not create: %v", err)
	}
}

// A rename onto an existing file is refused before anything is written.
func TestTx_RefusesExistingRenameDestination(t *testing.T) {
	dir := t.TempDir()
	a, ha := writeTx(t, dir, "a.md", "A")
	m, hm := writeTx(t, dir, "m.md", "M")
	taken, _ := writeTx(t, dir, "taken.md", "T")

	var tx Tx
	tx.Write(a, []byte("A2"), 0o644, ha)
	tx.Rename(m, taken, hm)
	if err := tx.Commit(); err == nil {
		t.Fatal("expected a refusal")
	}
	if readTx(t, a) != "A" || readTx(t, taken) != "T" {
		t.Fatal("a refused Tx wrote a file")
	}
}
