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

func TestWriteSafety_ApplyMoveRefusesEscape(t *testing.T) {
	parent, root := nestedRoot(t)
	writeTestFile(t, root, "a.md", nodeSrc("A"))
	writeTestFile(t, root, "b.md", nodeSrc("B")+"See [A](a.md).\n")
	b, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, dst := range []string{"../x.md", "../../x.md", "sub/../../x.md", "a\nb.md", "a\r## x.md"} {
		rw, _ := PlanMove(b, "a.md", dst)
		if err := ApplyMove(root, b, "a.md", dst, rw); err == nil {
			t.Errorf("ApplyMove(a.md -> %q) must be refused", dst)
		}
	}
	if _, err := os.Stat(filepath.Join(parent, "x.md")); err == nil {
		t.Fatal("node moved outside the bundle root")
	}
	if raw, _ := os.ReadFile(filepath.Join(root, "b.md")); string(raw) != nodeSrc("B")+"See [A](a.md).\n" {
		t.Fatalf("inbound link rewritten by a refused move: %q", raw)
	}
}

// Negative control: a nested in-bundle destination still moves.
func TestWriteSafety_ApplyMoveNestedStillWorks(t *testing.T) {
	root, b := mkMoveBundle(t, map[string]string{"a.md": nodeSrc("A")})
	rw, err := PlanMove(b, "a.md", "a/b/c.md")
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyMove(root, b, "a.md", "a/b/c.md", rw); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "a", "b", "c.md")); err != nil {
		t.Fatal(err)
	}
}
