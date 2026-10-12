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

func TestWriteSafety_MigrateApplyRefusesPlanPathEscape(t *testing.T) {
	parent, root := nestedRoot(t)
	writeLegacyV01Node(t, parent, "victim.md", "Concept", "Victim", nil)
	before, _ := os.ReadFile(filepath.Join(parent, "victim.md"))
	b, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	plan := MigratePlan{TargetVersion: "0.2", Nodes: []NodeMigration{{
		Path:      "../victim.md",
		Generated: &GeneratedEdit{By: "x", At: "2026-01-02T03:04:05Z"},
	}}}
	if err := MigrateApply(root, b, plan); err == nil {
		t.Fatal("MigrateApply with a plan path outside the root must be refused")
	}
	if after, _ := os.ReadFile(filepath.Join(parent, "victim.md")); string(after) != string(before) {
		t.Fatal("a file outside the bundle root was rewritten")
	}
}

// ---- injection --------------------------------------------------------------

// §12: the .okf sidecar is line-keyed; a target_version carrying a line break
// would smuggle extra sidecar keys.
func TestWriteSafety_MigrateApplyRefusesTargetInjection(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, ".okf", "okf_version: 0.1\n")
	writeTestFile(t, root, "a.md", nodeSrc("A"))
	b, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"0.2\ntemplates: evil", "0.2\rtemplates: evil"} {
		if err := MigrateApply(root, b, MigratePlan{TargetVersion: target}); err == nil {
			t.Errorf("MigrateApply(target %q) must be refused", target)
		}
	}
	if raw, _ := os.ReadFile(filepath.Join(root, ".okf")); string(raw) != "okf_version: 0.1\n" {
		t.Fatalf(".okf changed: %q", raw)
	}
}

// §9: a log entry is one `* ` line; a lone `\r` is a CommonMark line ending,
// so it must not forge a `## <date>` heading.
