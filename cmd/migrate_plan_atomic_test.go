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
	"testing"
)

// `migrate --plan` over an existing plan file replaces it by rename.
func TestMigrate_PlanWriteIsAtomic(t *testing.T) {
	dir := mkPromoteCLIBundle(t, migrateFixtureFiles())
	planPath := filepath.Join(t.TempDir(), "migrate-plan.json")
	if err := os.WriteFile(planPath, []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	held := filepath.Join(t.TempDir(), "held")
	if err := os.Link(planPath, held); err != nil {
		t.Skipf("hard links unsupported: %v", err)
	}
	if out, err := runOKF(t, "migrate", dir, "--plan", planPath, "--generated-by", "human:casey"); err != nil {
		t.Fatalf("migrate --plan: err=%v out=%q", err, out)
	}
	if got, _ := os.ReadFile(held); string(got) != "stale\n" {
		t.Fatal("plan file was rewritten in place (not by atomic rename)")
	}
	if got, _ := os.ReadFile(planPath); string(got) == "stale\n" {
		t.Fatal("plan file was not rewritten")
	}
}
