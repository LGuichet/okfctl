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

package okfconfig

import (
	"os"
	"path/filepath"
	"testing"
)

// Save replaces config.json by rename: a reader holding the old file (here, a
// hard link) never sees a truncated or half-written config.
func TestSave_IsAtomic(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OKFCTL_CONFIG_HOME", dir)
	if err := Save(map[string]string{"k": "old"}); err != nil {
		t.Fatal(err)
	}
	held := filepath.Join(t.TempDir(), "held")
	if err := os.Link(Path(), held); err != nil {
		t.Skipf("hard links unsupported: %v", err)
	}
	before, _ := os.ReadFile(held)
	if err := Save(map[string]string{"k": "new"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(held); string(got) != string(before) {
		t.Fatal("config.json was rewritten in place (not by atomic rename)")
	}
	m, err := Load()
	if err != nil || m["k"] != "new" {
		t.Fatalf("Load after Save = %v, %v; want k=new", m, err)
	}
}
