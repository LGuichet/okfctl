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
	"strings"
	"unicode"
)

// tagSurface is the context-free half of the tag fold: lower-cased, trimmed,
// and separator-insensitive. A hyphen, underscore, space, or slash is dropped
// wherever it appears (home-lab = homelab, ci/cd = cicd). A full stop is dropped
// only between two letters, where Unicode UAX #29 treats it as word-internal
// (node.js = nodejs); elsewhere it is kept, so `.net` stays apart from `net` and
// `v1.2` from `v12`. No suffix is touched here—plural folding needs the bundle's
// vocabulary (see tagKeys).
func tagSurface(s string) string {
	r := []rune(strings.ToLower(strings.TrimSpace(s)))
	var b strings.Builder
	for i, c := range r {
		switch c {
		case '-', '_', ' ', '/':
			continue
		case '.':
			if i > 0 && i < len(r)-1 && unicode.IsLetter(r[i-1]) && unicode.IsLetter(r[i+1]) {
				continue
			}
		}
		b.WriteRune(c)
	}
	return b.String()
}

// tagInvariant is the Snowball English (Porter2) stemmer's list of invariant
// exceptional forms: words that look inflected but are left as-is. It is what
// keeps `news` from folding onto `new`.
var tagInvariant = map[string]bool{
	"sky": true, "news": true, "howe": true, "atlas": true, "cosmos": true, "bias": true, "andes": true,
}

// tagPluralCandidates returns the singular forms KStem's plural() would try for
// a surface form, in KStem's order (Lucene KStemmer.java, derived from the UMass
// CIIR KStemmer). Its guards are what short tags need:
//
//   - a word that is not letters-only is never stemmed (k8s, v1, oauth2);
//   - a word of two letters or fewer is never stemmed (js);
//   - `-ies` tries `-ie`, then `-y`;
//   - `-es` tries dropping the `s` (unless a double `s` precedes it), then `es`;
//   - a plain final `s` is dropped only when the word is longer than three
//     letters and does not end in `ss` or `ous` (aws, ops, ios, dns stay whole).
//
// KStem chooses among candidates with a dictionary lookup; tagKeys uses the
// bundle's own tag vocabulary for that.
func tagPluralCandidates(w string) []string {
	n := len(w)
	if n <= 2 || tagInvariant[w] || !strings.HasSuffix(w, "s") {
		return nil
	}
	for _, c := range w {
		if !unicode.IsLetter(c) {
			return nil
		}
	}
	switch {
	case strings.HasSuffix(w, "ies"):
		return []string{w[:n-1], w[:n-3] + "y"}
	case strings.HasSuffix(w, "es"):
		j := n - 3 // the letter before "es"
		var out []string
		if j > 0 && !(w[j] == 's' && w[j-1] == 's') {
			out = append(out, w[:n-1])
		}
		return append(out, w[:n-2])
	case n > 3 && w[n-2] != 's' && !strings.HasSuffix(w, "ous"):
		return []string{w[:n-1]}
	}
	return nil
}

// tagKeys maps every raw tag value in raws to its canonical key. Two values
// share a key when their surface forms are equal, or when one's surface form
// is a KStem plural candidate of the other's AND that candidate is itself a
// surface form in raws. Requiring the candidate to be attested in the bundle
// stands in for KStem's dictionary: a non-word stem (cach, nodej) can never
// become a key, and nothing is folded unless both spellings are present—which
// a near-duplicate finding needs anyway.
//
// Classes are connected components (lenses → lens → len joins all three when
// all three are tags), so the result depends only on the SET of values, never
// their order. The key is the component's shortest surface form, ties broken
// byte-wise; it is a grouping handle, not a display spelling. A value whose
// surface form is empty maps to "".
func tagKeys(raws []string) map[string]string {
	parent := map[string]string{}
	for _, r := range raws {
		if s := tagSurface(r); s != "" {
			parent[s] = s
		}
	}
	find := func(x string) string {
		for parent[x] != x {
			x = parent[x]
		}
		return x
	}
	for s := range parent {
		for _, c := range tagPluralCandidates(s) {
			if _, ok := parent[c]; !ok {
				continue
			}
			a, b := find(s), find(c)
			if a != b {
				if len(b) < len(a) || (len(b) == len(a) && b < a) {
					a, b = b, a
				}
				parent[b] = a // the shorter, then byte-wise smaller, root wins
			}
			break
		}
	}
	out := make(map[string]string, len(raws))
	for _, r := range raws {
		if s := tagSurface(r); s != "" {
			out[r] = find(s)
		} else {
			out[r] = ""
		}
	}
	return out
}

// bundleTagKeys is tagKeys over every tag value in the bundle (trimmed), the
// vocabulary both lint's tag-hygiene and analyze's clusters fold against.
func bundleTagKeys(b *Bundle) map[string]string {
	seen := map[string]bool{}
	var raws []string
	for _, n := range b.Nodes {
		for _, t := range nodeTags(n) {
			if t = strings.TrimSpace(t); t != "" && !seen[t] {
				seen[t] = true
				raws = append(raws, t)
			}
		}
	}
	return tagKeys(raws)
}
