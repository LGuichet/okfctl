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
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/cwest/okfctl/internal/fsutil"
)

// DefaultLinkSection is the "##" heading node link files a new cross-reference
// under when no section is given. The placement is an okfctl authoring
// convention, not an OKF rule: §4.2 only says the body is Markdown, and §6.1
// says nothing about where a link lives in it.
const DefaultLinkSection = "Related"

// LinkPlan is the planned result of adding one cross-reference From -> To. It
// is computed without writing; ApplyLink performs it.
type LinkPlan struct {
	From    string // bundle-relative path of the node that gains the link
	To      string // bundle-relative path of the node linked to
	Section string // "##" heading text the bullet goes under

	// Exists reports that From already links to To in some spelling; the plan
	// is then a no-op and Existing is the url of the first such link.
	Exists   bool
	Existing string

	Dest           string // link destination written (relative, or "/"-absolute on ambiguity)
	Line           string // the bullet line added, without its line ending
	CreatesSection bool   // the "## <Section>" heading is new

	abs      string // absolute path of From, confined to the root
	original []byte // From's bytes as planned against
	updated  []byte // From's bytes after the edit
}

// PlanLink plans adding a Markdown link from node from to node to, filed as a
// bullet under the "## <section>" heading of from's body (DefaultLinkSection
// when section is ""). It never writes. root is the bundle root b was loaded
// from.
//
// Refusals: a path that escapes the root or carries a control character (§3,
// wrapped fsutil.ErrEscapesRoot / fsutil.ErrInvalidPath); a reserved index.md
// or log.md at either end (§3.1, §8, §9); a self-link; a from or to that is not
// a loaded node (the target must exist—node link never writes a broken link);
// a path the shared link scanner could not read back as a destination.
func PlanLink(root string, b *Bundle, from, to, section string) (*LinkPlan, error) {
	fromAbs, err := fsutil.ResolveInRoot(root, from)
	if err != nil {
		return nil, fmt.Errorf("link source %s: %w", from, err)
	}
	toAbs, err := fsutil.ResolveInRoot(root, to)
	if err != nil {
		return nil, fmt.Errorf("link target %s: %w", to, err)
	}
	fromRel, err := rootRel(root, fromAbs)
	if err != nil {
		return nil, err
	}
	toRel, err := rootRel(root, toAbs)
	if err != nil {
		return nil, err
	}
	if IsReservedPath(fromRel) || IsReservedPath(toRel) {
		return nil, fmt.Errorf("cannot link reserved file (%s -> %s): index.md and log.md are not concept documents", fromRel, toRel)
	}
	if fromRel == toRel {
		return nil, fmt.Errorf("cannot link %s to itself", fromRel)
	}
	for _, p := range []string{fromRel, toRel} {
		if strings.ContainsAny(p, " \t()<>") {
			return nil, fmt.Errorf("path %q cannot be written as a plain Markdown link destination (whitespace, parentheses or angle brackets)", p)
		}
	}
	fromNode, toNode := b.Nodes[fromRel], b.Nodes[toRel]
	if fromNode == nil {
		return nil, fmt.Errorf("node not found: %s", fromRel)
	}
	if toNode == nil {
		return nil, fmt.Errorf("link target not found: %s", toRel)
	}
	if section == "" {
		section = DefaultLinkSection
	}
	if err := checkSection(section); err != nil {
		return nil, err
	}

	original, err := os.ReadFile(fromAbs) //nolint:gosec // G304: path confined to the bundle root by ResolveInRoot
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", fromRel, err)
	}
	p := &LinkPlan{From: fromRel, To: toRel, Section: section, abs: fromAbs, original: original}

	// Only the body is edited; the §4 frontmatter block (and the separator
	// after it) is kept byte-for-byte as the prefix.
	body := original
	if _, after, ok := splitFrontmatterRaw(original); ok {
		body = after
	}
	prefix := original[:len(original)-len(body)]

	dir := path.Dir(fromRel)
	if url, ok := existingLink(b, dir, string(body), toRel); ok {
		p.Exists, p.Existing = true, url
		return p, nil
	}

	dest, err := linkDest(b, dir, toRel)
	if err != nil {
		return nil, err
	}
	p.Dest = dest
	newBody, line, created := insertLinkBullet(string(body), section, "["+linkText(toNode)+"]("+dest+")")
	p.Line, p.CreatesSection = line, created
	p.updated = append(append([]byte{}, prefix...), newBody...)
	return p, nil
}

// ApplyLink performs a plan from PlanLink. A no-op plan (Exists) writes
// nothing. The file is re-read first and the write refused if it changed since
// it was planned, so a concurrent edit is never clobbered. The write is atomic
// (fsutil.WriteFileAtomic).
func ApplyLink(p *LinkPlan) error {
	if p == nil || p.Exists {
		return nil
	}
	cur, err := os.ReadFile(p.abs)
	if err != nil {
		return fmt.Errorf("read %s: %w", p.From, err)
	}
	if !bytes.Equal(cur, p.original) {
		return fmt.Errorf("%s changed since the link was planned; rerun", p.From)
	}
	if err := fsutil.WriteFileAtomic(p.abs, p.updated, 0o644); err != nil { //nolint:gosec // G306: a bundle node is a shareable knowledge document; 0o644 is intended
		return fmt.Errorf("write %s: %w", p.From, err)
	}
	return nil
}

// rootRel returns abs as a clean bundle-relative slash path.
func rootRel(root, abs string) (string, error) {
	r, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("bundle root %s: %w", root, err)
	}
	rel, err := filepath.Rel(r, abs)
	if err != nil {
		return "", fmt.Errorf("relpath %s: %w", abs, err)
	}
	return filepath.ToSlash(rel), nil
}

func checkSection(s string) error {
	t := strings.TrimSpace(s)
	switch {
	case t == "":
		return fmt.Errorf("section heading must not be empty")
	case strings.HasPrefix(t, "#"):
		return fmt.Errorf("section %q: give the heading text without leading '#'", s)
	case strings.IndexFunc(s, unicode.IsControl) >= 0:
		return fmt.Errorf("section %q: control characters are not allowed", s)
	}
	return nil
}

// existingLink reports whether body already links to target, in any spelling
// the shared resolver accepts (relative, "./", root-relative, "/"-absolute,
// with or without a CommonMark title) and with any "#fragment" stripped.
// Links inside code fences count, exactly as they do for graph edges and lint.
func existingLink(b *Bundle, dir, body, target string) (string, bool) {
	for _, l := range scanNodeLinks(b, dir, body) {
		if l.resolved == target {
			return l.url, true
		}
	}
	for _, loc := range mdLinkRe.FindAllStringSubmatchIndex(body, -1) {
		if loc[0] > 0 && body[loc[0]-1] == '!' {
			continue // image, not a link
		}
		fields := strings.Fields(body[loc[2]:loc[3]])
		if len(fields) == 0 {
			continue
		}
		url := fields[0]
		i := strings.IndexByte(url, '#')
		if i <= 0 {
			continue // no fragment (already handled above), or a pure "#x" anchor
		}
		if resolved, form := resolveLinkURL(b, dir, url[:i]); form != formNone && resolved == target {
			return url, true
		}
	}
	return "", false
}

// linkDest returns the destination to write for a link from a node in dir to
// target: the §6.1 relative form, unless the shared resolver would read that
// relative path as a DIFFERENT node (it tries root-relative before
// dir-relative), in which case the §6.1 "/"-absolute form is used.
func linkDest(b *Bundle, dir, target string) (string, error) {
	r, err := filepath.Rel(filepath.FromSlash(dir), filepath.FromSlash(target))
	if err != nil {
		return "", fmt.Errorf("relpath %s -> %s: %w", dir, target, err)
	}
	rel := filepath.ToSlash(r)
	if got, _ := resolveLinkURL(b, dir, rel); got == target {
		return rel, nil
	}
	abs := "/" + target
	if got, _ := resolveLinkURL(b, dir, abs); got != target {
		return "", fmt.Errorf("cannot form a link from %s to %s that resolves back to it", dir, target)
	}
	return abs, nil
}

// linkText is to's title (or file stem), made safe as Markdown link text.
func linkText(n *Node) string {
	t := strings.Map(func(r rune) rune {
		switch {
		case r == '[':
			return '('
		case r == ']':
			return ')'
		case unicode.IsControl(r):
			return ' '
		}
		return r
	}, nodeTitle(n))
	t = strings.ReplaceAll(t, `\`, `\\`)
	if strings.TrimSpace(t) == "" {
		return strings.TrimSuffix(path.Base(n.Path), ".md")
	}
	return strings.TrimSpace(t)
}

// insertLinkBullet adds a bullet holding link to the "## <section>" section of
// body, creating the section at the end when absent. Headings inside fenced
// code blocks are ignored. It returns the new body, the bullet line, and
// whether the section was created. The body's line-ending style is kept.
func insertLinkBullet(body, section, link string) (string, string, bool) {
	eol := "\n"
	if strings.Contains(body, "\r\n") {
		eol = "\r\n"
	}
	lines := strings.SplitAfter(body, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	fence := ""
	start, end := -1, len(lines) // section heading index, first line after the section
	for i, raw := range lines {
		ln := strings.TrimRight(raw, "\r\n")
		if f := fenceMarker(ln); f != "" {
			switch {
			case fence == "":
				fence = f
			case f[0] == fence[0] && len(f) >= len(fence) && strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(ln), f[:1])) == "":
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		level, text := atxHeading(ln)
		if level == 0 {
			continue
		}
		if start < 0 {
			if level == 2 && text == strings.TrimSpace(section) {
				start = i
			}
		} else if level <= 2 {
			end = i
			break
		}
	}

	if start < 0 {
		line := "* " + link
		var sb strings.Builder
		sb.WriteString(body)
		if body != "" && !strings.HasSuffix(body, "\n") {
			sb.WriteString(eol)
		}
		if strings.TrimSpace(body) != "" {
			sb.WriteString(eol)
		}
		sb.WriteString("## " + strings.TrimSpace(section) + eol + eol + line + eol)
		return sb.String(), line, true
	}

	last := start // last non-blank line inside the section
	for i := start + 1; i < end; i++ {
		if strings.TrimSpace(lines[i]) != "" {
			last = i
		}
	}
	marker := "* "
	lastText := strings.TrimLeft(strings.TrimRight(lines[last], "\r\n"), " ")
	isItem := false
	for _, m := range []string{"* ", "- ", "+ "} {
		if last != start && strings.HasPrefix(lastText, m) {
			marker, isItem = m, true
		}
	}
	line := marker + link

	var sb strings.Builder
	for i := 0; i <= last; i++ {
		sb.WriteString(lines[i])
	}
	if !strings.HasSuffix(lines[last], "\n") {
		sb.WriteString(eol)
	}
	if !isItem {
		sb.WriteString(eol) // blank line before a new list
	}
	sb.WriteString(line + eol)
	for i := last + 1; i < len(lines); i++ {
		sb.WriteString(lines[i])
	}
	return sb.String(), line, false
}

// fenceMarker returns the opening run of a ``` or ~~~ fence line (indented at
// most three spaces), or "".
func fenceMarker(ln string) string {
	t := strings.TrimLeft(ln, " ")
	if len(ln)-len(t) > 3 {
		return ""
	}
	for _, c := range []byte{'`', '~'} {
		n := 0
		for n < len(t) && t[n] == c {
			n++
		}
		if n >= 3 {
			return t[:n]
		}
	}
	return ""
}

// atxHeading returns the level and text of an ATX heading line, or (0, "").
func atxHeading(ln string) (int, string) {
	t := strings.TrimLeft(ln, " ")
	if len(ln)-len(t) > 3 {
		return 0, ""
	}
	n := 0
	for n < len(t) && t[n] == '#' {
		n++
	}
	if n == 0 || n > 6 || (n < len(t) && t[n] != ' ' && t[n] != '\t') {
		return 0, ""
	}
	text := strings.TrimSpace(t[n:])
	// An optional closing run of '#' (preceded by a space) is not text.
	if s := strings.TrimRight(text, "#"); s != text && (s == "" || strings.HasSuffix(s, " ")) {
		text = strings.TrimSpace(s)
	}
	return n, text
}
