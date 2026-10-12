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

package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cwest/okfctl/internal/fsutil"
)

func readFileT(t *testing.T, p string) string {
	t.Helper()
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func countLinkedLogLines(t *testing.T, dir string) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "log.md"))
	if err != nil {
		return 0
	}
	return strings.Count(string(raw), "linked a.md -> wine/b.md")
}

// Positive control: the link is added once and exactly one §9 log entry is
// appended. Negative control: a rerun is a byte-identical no-op with no new
// log line.
func TestNodeLink_AddsOnceAndLogs_S9(t *testing.T) {
	dir := t.TempDir()
	writeRaw(t, dir, "a.md", nodeDoc("A")+"Body.\n")
	writeRaw(t, dir, "wine/b.md", nodeDoc("Bee"))

	out, err := runOKF(t, "node", "link", "a", "wine/b", "--bundle", dir)
	if err != nil {
		t.Fatalf("node link: %v", err)
	}
	if !strings.Contains(out, "Linked a.md -> wine/b.md") {
		t.Fatalf("output = %q", out)
	}
	want := nodeDoc("A") + "Body.\n\n## Related\n\n* [Bee](wine/b.md)\n"
	if got := readFileT(t, filepath.Join(dir, "a.md")); got != want {
		t.Fatalf("a.md =\n%q\nwant\n%q", got, want)
	}
	if n := countLinkedLogLines(t, dir); n != 1 {
		t.Fatalf("log entries after link = %d, want 1", n)
	}
	logBefore := readFileT(t, filepath.Join(dir, "log.md"))

	out, err = runOKF(t, "node", "link", "a.md", "wine/b.md", "--bundle", dir)
	if err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if !strings.Contains(out, "already links") {
		t.Fatalf("rerun output = %q, want no-op report", out)
	}
	if got := readFileT(t, filepath.Join(dir, "a.md")); got != want {
		t.Fatalf("rerun changed a.md: %q", got)
	}
	if got := readFileT(t, filepath.Join(dir, "log.md")); got != logBefore {
		t.Fatal("rerun (no-op) appended to log.md")
	}
	if out, err := runOKF(t, "validate", dir); err != nil {
		t.Fatalf("bundle invalid after link: %v\n%s", err, out)
	}
}

// Negative control: an existing link in another spelling (with a #fragment)
// is detected; nothing is written and no log line appears.
func TestNodeLink_DifferentSpellingIsNoOp(t *testing.T) {
	dir := t.TempDir()
	src := nodeDoc("A") + "See [b](./wine/b.md#tasting).\n"
	writeRaw(t, dir, "a.md", src)
	writeRaw(t, dir, "wine/b.md", nodeDoc("B"))
	out, err := runOKF(t, "node", "link", "a", "wine/b", "--bundle", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "already links") {
		t.Fatalf("output = %q", out)
	}
	if got := readFileT(t, filepath.Join(dir, "a.md")); got != src {
		t.Fatalf("a.md rewritten: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "log.md")); err == nil {
		t.Fatal("no-op created log.md")
	}
}

// Negative control: --dry-run prints the change and touches nothing (bytes
// and mtime of the node; no log.md).
func TestNodeLink_DryRunTouchesNothing(t *testing.T) {
	dir := t.TempDir()
	writeRaw(t, dir, "a.md", nodeDoc("A"))
	writeRaw(t, dir, "wine/b.md", nodeDoc("Bee"))
	a := filepath.Join(dir, "a.md")
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(a, old, old); err != nil {
		t.Fatal(err)
	}
	before := readFileT(t, a)

	out, err := runOKF(t, "node", "link", "a", "wine/b", "--bundle", dir, "--dry-run", "--section", "See also")
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	for _, w := range []string{"link a.md -> wine/b.md", `"## See also" (new section)`, "+ * [Bee](wine/b.md)"} {
		if !strings.Contains(out, w) {
			t.Errorf("dry-run output missing %q:\n%s", w, out)
		}
	}
	if got := readFileT(t, a); got != before {
		t.Fatal("dry-run wrote the node")
	}
	if st, _ := os.Stat(a); !st.ModTime().Equal(old) {
		t.Fatalf("dry-run touched mtime: %v != %v", st.ModTime(), old)
	}
	if _, err := os.Stat(filepath.Join(dir, "log.md")); err == nil {
		t.Fatal("dry-run created log.md")
	}
}

// §3: neither end may leave the bundle root; the error wraps
// fsutil.ErrEscapesRoot. §3.1: reserved files are refused. Self-links and a
// missing target are refused. None of these writes anything.
func TestNodeLink_Refusals_S3_S3_1(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "bundle")
	writeRaw(t, dir, "a.md", nodeDoc("A"))
	writeRaw(t, parent, "outside.md", nodeDoc("Outside"))

	for _, args := range [][]string{{"a", "../outside"}, {"../outside", "a"}} {
		_, err := runOKF(t, append([]string{"node", "link"}, append(args, "--bundle", dir)...)...)
		if !errors.Is(err, fsutil.ErrEscapesRoot) {
			t.Errorf("node link %v: err = %v, want ErrEscapesRoot", args, err)
		}
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"a", "index"}, "reserved"},
		{[]string{"log", "a"}, "reserved"},
		{[]string{"a", "a"}, "itself"},
		{[]string{"a", "nope"}, "not found"},
	} {
		_, err := runOKF(t, append([]string{"node", "link"}, append(c.args, "--bundle", dir)...)...)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("node link %v: err = %v, want %q", c.args, err, c.want)
		}
	}
	if got := readFileT(t, filepath.Join(dir, "a.md")); got != nodeDoc("A") {
		t.Fatalf("refused link wrote a.md: %q", got)
	}
	if got := readFileT(t, filepath.Join(parent, "outside.md")); got != nodeDoc("Outside") {
		t.Fatal("file outside the bundle was touched")
	}
	if _, err := os.Stat(filepath.Join(dir, "log.md")); err == nil {
		t.Fatal("a refused link created log.md")
	}
}
