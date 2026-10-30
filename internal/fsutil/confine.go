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

package fsutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrEscapesRoot reports a path that would resolve outside the bundle root:
// a ".." that climbs above it, an OS-absolute path with a volume, or a
// symlinked directory that points out of it.
var ErrEscapesRoot = errors.New("path escapes the bundle root")

// ErrInvalidPath reports a path that names no writable location in the bundle:
// empty, the root itself, or one carrying a control character.
var ErrInvalidPath = errors.New("invalid bundle path")

// ResolveInRoot confines a user-supplied, bundle-relative path to root and
// returns its absolute location, or refuses it. It is the single confinement
// gate for every command that writes to a path the user typed (node new, node
// edit, node mv, node promote, migrate apply) and is meant to be reused by any
// future one.
//
// OKF §3 defines a bundle as its directory tree, so a write must land inside
// it. rel is refused when it:
//   - is empty or cleans to the root itself (ErrInvalidPath);
//   - contains a control character—C0 or DEL (ErrInvalidPath). Paths are
//     echoed into link text by node mv / node promote, so a line break here
//     would be written raw into a node;
//   - carries a volume name, e.g. `C:\x` or `\\server\share` (ErrEscapesRoot);
//   - climbs above root with ".." after cleaning (ErrEscapesRoot);
//   - passes, via its deepest existing ancestor, through a symlink that
//     resolves outside root (ErrEscapesRoot). A symlink that stays inside
//     the bundle is fine.
//
// A leading "/" (or, on Windows, "\") is bundle-root-relative, matching the
// §6.1 "/"-absolute link form: "/a/b.md" resolves to <root>/a/b.md.
//
// The returned path is the lexical one (root joined with the cleaned rel),
// not the symlink-resolved one, so callers write where the user pointed. The
// check runs before the write; a symlink swapped in between check and write
// is outside the threat model of a local, single-user CLI.
func ResolveInRoot(root, rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("%w: empty path", ErrInvalidPath)
	}
	for _, r := range rel {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("%w: %q contains a control character", ErrInvalidPath, rel)
		}
	}
	native := filepath.FromSlash(rel)
	if filepath.VolumeName(native) != "" {
		return "", fmt.Errorf("%w: %s", ErrEscapesRoot, rel)
	}
	native = strings.TrimLeftFunc(native, func(r rune) bool { return r == '/' || r == filepath.Separator })
	clean := filepath.Clean(native)
	if clean == "." || native == "" {
		return "", fmt.Errorf("%w: %q names the bundle root", ErrInvalidPath, rel)
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %s", ErrEscapesRoot, rel)
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve bundle root %s: %w", root, err)
	}
	joined := filepath.Join(absRoot, clean)

	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", fmt.Errorf("resolve bundle root %s: %w", root, err)
	}
	// Resolve the deepest ancestor that exists; everything below it is new
	// and, being ".."-free after Clean, cannot climb back out.
	existing := joined
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		existing = parent
	}
	realExisting, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", fmt.Errorf("%w: %s (%w)", ErrEscapesRoot, rel, err)
	}
	if !within(realRoot, realExisting) {
		return "", fmt.Errorf("%w: %s resolves through a symlink to %s", ErrEscapesRoot, rel, realExisting)
	}
	// filepath.EvalSymlinks does not follow Windows directory junctions (mount
	// points), which an unprivileged user can create, so also check every
	// existing component below the root: any link-like entry (symlink or other
	// reparse point) must itself resolve inside the bundle.
	if existing != absRoot {
		suffix, err := filepath.Rel(absRoot, existing)
		if err != nil {
			return "", fmt.Errorf("%w: %s", ErrEscapesRoot, rel)
		}
		cur := absRoot
		for _, part := range strings.Split(suffix, string(filepath.Separator)) {
			cur = filepath.Join(cur, part)
			fi, err := os.Lstat(cur)
			if err != nil {
				return "", fmt.Errorf("%w: %s (%w)", ErrEscapesRoot, rel, err)
			}
			if fi.Mode()&(os.ModeSymlink|os.ModeIrregular) == 0 {
				continue
			}
			target, err := os.Readlink(cur)
			if err != nil {
				return "", fmt.Errorf("%w: %s passes through an unresolvable link %s", ErrEscapesRoot, rel, cur)
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(cur), target)
			}
			realTarget, err := filepath.EvalSymlinks(target)
			if err != nil || !within(realRoot, realTarget) {
				return "", fmt.Errorf("%w: %s passes through a link to %s", ErrEscapesRoot, rel, target)
			}
		}
	}
	return joined, nil
}

// within reports whether path (already link-resolved) is root or below it.
func within(root, path string) bool {
	inside, err := filepath.Rel(root, path)
	if err != nil || filepath.IsAbs(inside) {
		return false
	}
	return inside != ".." && !strings.HasPrefix(inside, ".."+string(filepath.Separator))
}
