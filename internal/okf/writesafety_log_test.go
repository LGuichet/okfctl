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
	"regexp"
	"strings"
	"testing"
)

func TestWriteSafety_AppendLogCannotForgeHeading(t *testing.T) {
	heading := regexp.MustCompile(`^## `)
	for _, fresh := range []bool{true, false} {
		root := t.TempDir()
		if !fresh {
			if err := Scaffold(root); err != nil {
				t.Fatal(err)
			}
		}
		if err := AppendLog(root, "a\r## 1999-01-01\r\n* forged"); err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(filepath.Join(root, "log.md"))
		n := 0
		for _, line := range regexp.MustCompile(`\r\n|\r|\n`).Split(string(raw), -1) {
			if heading.MatchString(line) {
				n++
				if strings.Contains(line, "1999") {
					t.Errorf("forged heading %q", line)
				}
			}
		}
		if n != 1 {
			t.Errorf("fresh=%v: %d date headings, want 1:\n%s", fresh, n, raw)
		}
	}
}
