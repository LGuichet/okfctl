// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Positive control: a node edited between `migrate --plan` and `--apply` is
// refused by the real apply AND by its --dry-run, and the bundle is untouched.
func TestMigrate_ApplyRefusesNodeEditedSincePlan(t *testing.T) {
	dir := mkPromoteCLIBundle(t, migrateFixtureFiles())
	planPath := filepath.Join(t.TempDir(), "migrate-plan.json")
	if out, err := runOKF(t, "migrate", dir, "--plan", planPath, "--generated-by", "human:casey"); err != nil {
		t.Fatalf("plan phase must exit 0: err=%v out=%q", err, out)
	}
	if !strings.Contains(readFileStr(t, planPath), `"sha256": "`) {
		t.Fatalf("plan carries no per-node sha256:\n%s", readFileStr(t, planPath))
	}
	a := filepath.Join(dir, "a.md")
	if err := os.WriteFile(a, []byte(readFileStr(t, a)+"\nEdited after the plan.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := cliTreeHash(t, dir)

	for _, args := range [][]string{
		{"migrate", dir, "--apply", "--plan", planPath, "--dry-run"},
		{"migrate", dir, "--apply", "--plan", planPath},
	} {
		out, err := runOKF(t, args...)
		if err == nil || !strings.Contains(err.Error(), "a.md") || !strings.Contains(err.Error(), "changed since the plan") {
			t.Fatalf("%v: want a refusal naming a.md, got err=%v out=%q", args, err, out)
		}
	}
	if after := cliTreeHash(t, dir); before != after {
		t.Fatal("a refused migrate wrote to the bundle")
	}
}

// Negative control: a plan from before per-node hashes still applies, with a
// warning on stderr that its nodes could not be checked.
func TestMigrate_ApplyLegacyPlanWarnsAndApplies(t *testing.T) {
	dir := mkPromoteCLIBundle(t, migrateFixtureFiles())
	planPath := filepath.Join(t.TempDir(), "migrate-plan.json")
	if out, err := runOKF(t, "migrate", dir, "--plan", planPath, "--generated-by", "human:casey"); err != nil {
		t.Fatalf("plan phase must exit 0: err=%v out=%q", err, out)
	}
	var plan map[string]any
	if err := json.Unmarshal([]byte(readFileStr(t, planPath)), &plan); err != nil {
		t.Fatal(err)
	}
	for _, n := range plan["nodes"].([]any) {
		delete(n.(map[string]any), "sha256")
	}
	data, _ := json.Marshal(plan)
	if err := os.WriteFile(planPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, err := runOKFSplit(t, "migrate", dir, "--apply", "--plan", planPath)
	if err != nil {
		t.Fatalf("a legacy plan must still apply: %v", err)
	}
	if !strings.Contains(stderr, "carry no sha256") {
		t.Errorf("stderr lacks the unchecked-plan warning: %q", stderr)
	}
	if out, err := runOKF(t, "validate", dir); err != nil {
		t.Fatalf("validate must pass after migrate: err=%v out=%q", err, out)
	}
}

// Negative control: a fresh plan applies without the legacy warning.
func TestMigrate_ApplyHashedPlanNoWarning(t *testing.T) {
	dir := mkPromoteCLIBundle(t, migrateFixtureFiles())
	planPath := filepath.Join(t.TempDir(), "migrate-plan.json")
	if out, err := runOKF(t, "migrate", dir, "--plan", planPath, "--generated-by", "human:casey"); err != nil {
		t.Fatalf("plan phase must exit 0: err=%v out=%q", err, out)
	}
	_, stderr, err := runOKFSplit(t, "migrate", dir, "--apply", "--plan", planPath)
	if err != nil {
		t.Fatalf("apply must exit 0: %v", err)
	}
	if strings.Contains(stderr, "sha256") {
		t.Errorf("a hashed plan printed the legacy warning: %q", stderr)
	}
}
