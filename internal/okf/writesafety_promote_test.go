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
	"os"
	"path/filepath"
	"testing"
)

func TestWriteSafety_PromotePlanRefusesBadName(t *testing.T) {
	_, b := mkPromoteBundle(t, map[string]string{
		"foo/index.md": dirConceptIndex("Foo", "2026-01-01", "# Foo\n"),
	})
	// "../x" and "a/b" are not one path segment; "log" / "index" would create
	// a §3.1 reserved file; a line break would land raw in rewritten links.
	for _, name := range []string{"../x", "../../x", "a/b", `a\b`, "..", ".", "log", "index", "a\nb"} {
		if _, err := PromotePlan(b, name); err == nil {
			t.Errorf("PromotePlan(--name %q) must be refused", name)
		}
	}
	// Negative control: the default and an ordinary name are accepted.
	for _, name := range []string{"", "foo-overview", "Before --- after"} {
		if _, err := PromotePlan(b, name); err != nil {
			t.Errorf("PromotePlan(--name %q): %v", name, err)
		}
	}
}

func TestWriteSafety_PromoteApplyRefusesEscape(t *testing.T) {
	parent, root := nestedRoot(t)
	writeTestFile(t, root, "foo/index.md", dirConceptIndex("Foo", "2026-01-01", "# Foo\n"))
	b, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	changes := []PromoteChange{{OldPath: "foo/index.md", NewPath: "../x.md"}}
	if err := PromoteApply(root, b, changes); err == nil {
		t.Fatal("PromoteApply to ../x.md must be refused")
	}
	if _, err := os.Stat(filepath.Join(parent, "x.md")); err == nil {
		t.Fatal("promoted concept written outside the bundle root")
	}
	if _, err := os.Stat(filepath.Join(root, "foo", "index.md")); err != nil {
		t.Fatal("old index removed by a refused promotion")
	}
}

// The plan JSON is hand-editable; its node paths are untrusted input.
