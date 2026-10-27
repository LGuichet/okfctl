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

package search

import (
	"os"
	"path/filepath"
	"testing"
)

// Store.Save replaces the index by rename, so a concurrent `search` never
// loads a truncated JSON file.
func TestStore_SaveIsAtomic(t *testing.T) {
	b, _ := fixtureBundle(t)
	s := BuildIndex(b, NewHashEmbedder(), nil)
	p := filepath.Join(t.TempDir(), "index.json")
	if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	held := filepath.Join(t.TempDir(), "held")
	if err := os.Link(p, held); err != nil {
		t.Skipf("hard links unsupported: %v", err)
	}
	if err := s.Save(p); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(held); string(got) != "{}\n" {
		t.Fatal("store was rewritten in place (not by atomic rename)")
	}
	if _, err := Load(p); err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
}
