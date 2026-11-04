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

package lexical

import (
	"reflect"
	"testing"
)

func TestAnalyze_KeepsDuplicatesDropsStopwordsStems(t *testing.T) {
	got := Analyze("The hashes and the HASH of receipts")
	want := []string{"hash", "hash", "receipt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Analyze = %v want %v", got, want)
	}
}

func TestTerms_DedupedSortedAndShortTokensKept(t *testing.T) {
	got := Terms("PA ci the CI pa signatures")
	want := []string{"ci", "pa", "signatur"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Terms = %v want %v", got, want)
	}
	if !IsStopword("the") || IsStopword("ci") {
		t.Fatal("stopword set wrong")
	}
}
