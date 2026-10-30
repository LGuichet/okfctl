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
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// The fuzz targets below drive the WRITE paths, where frontmatter_fuzz_test.go
// drives the read paths. Each one asserts the write-safety property the audit
// fixed or confirmed: an untrusted string handed to a writer can change the
// value it is meant to change and nothing else—no extra §4 frontmatter key, no
// lost key, no rewritten body, no forged §9 log heading, no leftover temp file.

// sortedKeys returns the keys of a parsed frontmatter map, sorted.
func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// assertNoTempFiles fails if an atomic write left a `.<base>.tmp-*` file behind.
func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("atomic write left a temp file behind: %s", e.Name())
		}
	}
}

// FuzzNewNodeFrontmatter: `node new --type --title` hands two user strings to
// the §4 frontmatter writer. Whatever they hold—a `---` fence, a newline
// followed by `key: value`, YAML flow syntax—the written block must parse and
// carry exactly type, title (when non-blank), created and modified: no
// injected key.
func FuzzNewNodeFrontmatter(f *testing.F) {
	seeds := [][2]string{
		{"Reference", "Widgets"},
		{"Reference", "Before --- after"},
		{"Reference", "line\n---\ninjected: true"},
		{"Reference", "line\r---\rinjected: true"},
		{"Ref\ninjected: true", "T"},
		{"Reference", "{a: b}"},
		{"Reference", "  "},
		{"Reference", "#comment"},
	}
	for _, s := range seeds {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, typ, title string) {
		if strings.TrimSpace(typ) == "" {
			return // refused by design (§4 type is required)
		}
		root := t.TempDir()
		abs, err := NewNode(root, "n.md", typ, title)
		if err != nil {
			t.Fatalf("NewNode(%q, %q): %v", typ, title, err)
		}
		raw, err := os.ReadFile(abs) //nolint:gosec // G304: test-owned temp file
		if err != nil {
			t.Fatal(err)
		}
		fm, _, err := ParseFrontmatter(raw)
		if err != nil {
			t.Fatalf("written frontmatter does not parse: %v\n%s", err, raw)
		}
		want := []string{"created", "modified", "type"}
		if strings.TrimSpace(title) != "" {
			want = append(want, "title")
		}
		sort.Strings(want)
		if got := sortedKeys(fm); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("frontmatter keys = %v, want %v\n%s", got, want, raw)
		}
		assertNoTempFiles(t, root)
	})
}

// FuzzTouchModifiedRoundTrip: the `modified` refresh rewrites only the §4
// frontmatter block of an arbitrary node file. Either it refuses and leaves the
// file byte-identical, or it keeps every original key and the body verbatim.
func FuzzTouchModifiedRoundTrip(f *testing.F) {
	seeds := []string{
		"---\ntype: Reference\ntitle: Widgets\n---\n\n# Body\n",
		"---\ntype: Reference\nmodified: 2020-01-01T00:00:00Z\n---\nbody --- with a rule\n\n---\n",
		"# no frontmatter\n",
		"---\r\ntype: Reference\r\n---\r\n\r\nCRLF body\r\n",
		"---\ntitle: |\n  line\n  ---\n  injected: true\n---\n",
		"---\n- a list\n---\n",
		"---\n? \n---\n",           // an empty null key: re-encoded as '' it became a new key
		"---\nype: 0\nype:\n---\n", // a duplicate key: the parser rejects it, so touch must too
		"",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	at := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, src string) {
		dir := t.TempDir()
		abs := filepath.Join(dir, "n.md")
		if err := os.WriteFile(abs, []byte(src), 0o644); err != nil { //nolint:gosec // G306: test fixture
			t.Fatal(err)
		}
		werr := TouchModifiedFile(abs, at)
		got, err := os.ReadFile(abs) //nolint:gosec // G304: test-owned temp file
		if err != nil {
			t.Fatal(err)
		}
		assertNoTempFiles(t, dir)
		if werr != nil {
			if !bytes.Equal(got, []byte(src)) {
				t.Fatalf("refused touch still changed the file: %v", werr)
			}
			return
		}
		_, after, ok := splitFrontmatterRaw(got)
		if !ok {
			t.Fatalf("touched file has no frontmatter block:\n%q", got)
		}
		if _, origAfter, had := splitFrontmatterRaw([]byte(src)); had {
			if !bytes.Equal(after, origAfter) {
				t.Fatalf("body changed:\nwas %q\nnow %q", origAfter, after)
			}
		} else if !bytes.HasSuffix(got, []byte(src)) {
			t.Fatalf("body changed when a block was prepended:\n%q", got)
		}
		newFM, _, err := ParseFrontmatter(got)
		if err != nil {
			t.Fatalf("touched frontmatter does not parse: %v\n%q", err, got)
		}
		if _, ok := newFM["modified"]; !ok {
			t.Fatalf("modified missing after touch:\n%q", got)
		}
		if oldFM, _, err := ParseFrontmatter([]byte(src)); err == nil {
			for k := range oldFM {
				if _, ok := newFM[k]; !ok {
					t.Fatalf("key %q lost by touch:\n%q", k, got)
				}
			}
			if len(newFM) > len(oldFM)+1 {
				t.Fatalf("touch added keys: %v -> %v", sortedKeys(oldFM), sortedKeys(newFM))
			}
		}
	})
}

// FuzzRootIndexMarker: `migrate --apply` writes the §12 okf_version marker into
// the bundle-root index.md from a plan the user may edit. Whatever the target
// string, the rewrite keeps the key set and the body, and a target with no
// control character is read back by Load as declared.
func FuzzRootIndexMarker(f *testing.F) {
	// "\t\n0": yaml.v3 picks a literal block for it, whose tab-led line no parser
	// reads back.
	for _, s := range []string{"0.2", "0.3", "1.0", "0.2\ninjected: true", "0.2\r---\r", "'", "\"", "- x", "{a: b}", " 0.2 ", "\t\n0"} {
		f.Add(s)
	}
	const body = "\n# Index\n\n---\n\nA rule above.\n"
	f.Fuzz(func(t *testing.T, version string) {
		root := t.TempDir()
		src := "---\nokf_version: \"0.1\"\ntitle: Root\n---\n" + body
		p := filepath.Join(root, "index.md")
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil { //nolint:gosec // G306: test fixture
			t.Fatal(err)
		}
		if err := bumpRootIndexMarker(root, version); err != nil {
			t.Fatalf("bumpRootIndexMarker(%q): %v", version, err)
		}
		got, err := os.ReadFile(p) //nolint:gosec // G304: test-owned temp file
		if err != nil {
			t.Fatal(err)
		}
		assertNoTempFiles(t, root)
		_, after, ok := splitFrontmatterRaw(got)
		if !ok || string(after) != body {
			t.Fatalf("body not preserved:\n%q", got)
		}
		fm, _, err := ParseFrontmatter(got)
		if err != nil {
			t.Fatalf("index.md frontmatter does not parse: %v\n%q", err, got)
		}
		if keys := strings.Join(sortedKeys(fm), ","); keys != "okf_version,title" {
			t.Fatalf("keys = %s, want okf_version,title\n%q", keys, got)
		}
		if s, isStr := fm["okf_version"].(string); isStr && strings.TrimSpace(version) != "" &&
			!strings.ContainsFunc(version, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			if s != version {
				t.Fatalf("okf_version = %q, want %q", s, version)
			}
		}
	})
}

// FuzzAppendLog: `log append` takes a free-text message. It must add exactly
// one `* ` entry and at most one `## <date>` heading (§9): a message cannot
// forge a heading or a second entry, whatever line breaks it carries.
func FuzzAppendLog(f *testing.F) {
	for _, s := range []string{"did a thing", "a\n## 2020-01-01\n* forged", "a\r## 2020-01-01", "a\r\n* b", "## 2020-01-01", "  ", " x"} {
		f.Add(s)
	}
	at := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	prev := nowUTC
	nowUTC = func() time.Time { return at }
	f.Cleanup(func() { nowUTC = prev })
	const existing = "# Change Log\n\n## 2026-10-11\n* earlier entry\n"
	lines := func(s string) []string {
		return strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' })
	}
	count := func(s, prefix string) int {
		n := 0
		for _, l := range lines(s) {
			if strings.HasPrefix(l, prefix) {
				n++
			}
		}
		return n
	}
	f.Fuzz(func(t *testing.T, msg string) {
		root := t.TempDir()
		p := filepath.Join(root, "log.md")
		if err := os.WriteFile(p, []byte(existing), 0o644); err != nil { //nolint:gosec // G306: test fixture
			t.Fatal(err)
		}
		werr := AppendLog(root, msg)
		got, err := os.ReadFile(p) //nolint:gosec // G304: test-owned temp file
		if err != nil {
			t.Fatal(err)
		}
		assertNoTempFiles(t, root)
		if werr != nil {
			if string(got) != existing {
				t.Fatalf("refused append still changed the log: %v", werr)
			}
			return
		}
		if d := count(string(got), "## ") - count(existing, "## "); d != 1 {
			t.Fatalf("heading count changed by %d, want 1:\n%s", d, got)
		}
		if d := count(string(got), "* ") - count(existing, "* "); d != 1 {
			t.Fatalf("entry count changed by %d, want 1:\n%s", d, got)
		}
	})
}
