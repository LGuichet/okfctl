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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// §3: a bundle is its directory tree. `node edit ../x` must not open, touch,
// or rewrite a file outside the bundle root.
func TestNodeEdit_RefusesPathOutsideBundle_Section3(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "bundle")
	writeRaw(t, dir, "a.md", nodeDoc("A"))
	writeRaw(t, parent, "outside.md", nodeDoc("Outside"))
	before, _ := os.ReadFile(filepath.Join(parent, "outside.md"))
	marker := filepath.Join(parent, "ran.marker")
	t.Setenv("OKFCTL_EDITOR", fakeEditor(t, parent, "", 0, marker))

	for _, p := range []string{"../outside", "../outside.md", "sub/../../outside"} {
		_, err := runOKF(t, "node", "edit", p, "--bundle", dir)
		if err == nil || !strings.Contains(err.Error(), "escapes the bundle root") {
			t.Errorf("node edit %s: err = %v, want an escapes-the-bundle-root refusal", p, err)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the editor was launched on a path outside the bundle")
	}
	if after, _ := os.ReadFile(filepath.Join(parent, "outside.md")); string(after) != string(before) {
		t.Fatal("a file outside the bundle root was rewritten")
	}
}
