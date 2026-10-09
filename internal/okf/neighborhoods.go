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
	"sort"
	"strings"
)

// DirShape is the structured form of what lives under a bundle directory: how
// many concept nodes it holds directly and in its whole subtree, which `type`
// values its own concepts use, and which tags recur across them. It is the data
// behind the subdirectory shape suffix that `index build` writes (OKF §8
// progressive disclosure; see dirShape), exposed so a summary command can print
// per-neighborhood counts and vocabulary (PRD §6.1 lists "neighborhoods" as part
// of `bundle info`) without re-walking the tree or re-deriving the rules. Because
// dirShape renders FROM this struct, the index suffix and any other consumer
// cannot drift apart.
type DirShape struct {
	Dir     string // bundle-relative slash path; "" is the bundle root
	Own     int    // concept nodes directly in Dir
	Subtree int    // concept nodes in Dir and every descendant (>= Own)
	// Types are the distinct `type` values of Dir's own concepts, sorted. §7.4
	// leaves the vocabulary open, so every value is kept exactly as written.
	Types []string
	// SharedTags are the §4.1 tags carried by >= opts.TagMin of Dir's own
	// concepts, folded case-insensitively and shown in their dominant casing,
	// most-shared-first with an alphabetical tie-break, capped at opts.TagMax.
	SharedTags []string
	// TagsTruncated reports that more tags qualified than opts.TagMax allowed.
	TagsTruncated bool
}

// Shape computes the DirShape of dir over the loaded bundle, using opts' tag
// threshold and cap (opts.Enabled is ignored: the data exists whether or not an
// index renders it). Output is deterministic.
func Shape(b *Bundle, dir string, opts IndexShapeOptions) DirShape {
	ownPaths := conceptsIn(b, dir) // sorted, deterministic
	s := DirShape{Dir: dir, Own: len(ownPaths), Subtree: subtreeConceptCount(b, dir)}

	typeSet := map[string]bool{}
	perNodeTags := make([][]string, 0, len(ownPaths))
	for _, p := range ownPaths {
		n := b.Nodes[p]
		if t := strings.TrimSpace(n.Type()); t != "" {
			typeSet[t] = true
		}
		perNodeTags = append(perNodeTags, nodeTags(n))
	}
	for t := range typeSet {
		s.Types = append(s.Types, t)
	}
	sort.Strings(s.Types)

	s.SharedTags, s.TagsTruncated = sharedTagList(foldTags(perNodeTags), opts.TagMin, opts.TagMax)
	return s
}

// Shapes returns the DirShape of every directory that carries an index.md
// (IndexDirs), in the same sorted order: the bundle-wide "index of indices"
// view.
func Shapes(b *Bundle, opts IndexShapeOptions) []DirShape {
	dirs := IndexDirs(b)
	out := make([]DirShape, 0, len(dirs))
	for _, d := range dirs {
		out = append(out, Shape(b, d, opts))
	}
	return out
}
