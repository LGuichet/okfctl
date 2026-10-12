# fix(write)—write-safety audit: atomic, confined, injection-free writes

**Status:** Approved (design) · **Owner:** Casey West · **License:** Apache-2.0
**Source of truth:** OKF `SPEC.md` **v0.2** (upstream
`GoogleCloudPlatform/knowledge-catalog`, header confirmed `**Version 0.2**`)—§3
(a bundle is a directory tree), §3.1 (reserved filenames), §4 / §4.1 (the
frontmatter block is delimited by `---` on its own line; unknown keys are
preserved), §6.1 (a `/`-leading link is bundle-root-relative), §8 (index
files), §9 (log files), §12 (versioning, `okf_version`). No new spec
behavior—this hardens how okfctl writes, it adds no `validate` rule.
**Base:** `next` @ `634eeb1`  **Branch:** `fix/write-safety-audit`

## Problem

okfctl writes bundle content—nodes, indexes, the log, the `.okf` sidecar—plus
its own state (the search vector store, the user config, the migration plan).
Three properties are each assumed somewhere and enforced nowhere in one place:

- **Atomic:** a write lands whole or not at all. A crash (or a full disk)
  mid-`os.WriteFile` truncates the target and leaves a half-written node,
  index, log, or vector store behind.
- **Confined:** a user-supplied path resolves inside the bundle root (§3: a
  bundle *is* its directory tree). `..`, an OS-absolute path, and a symlink
  that points out of the root are refused.
- **No frontmatter injection:** a value written into a YAML (or line-keyed)
  block goes through an encoder, so a value carrying `\n---\n` or a newline
  cannot close the §4 block early or open a new key.

## Audit—path × property

Every non-test call to `os.WriteFile` / `os.Create` / `os.Rename` /
`os.CreateTemp`, plus the user-path entry points that reach them. "Safe" means
the property holds today and the code is left alone; "gap" is fixed by this
change, one commit per gap.

| write path (entry point → writer) | atomic | confined | no injection |
|---|---|---|---|
| `node new` → `okf.NewNodeFromTemplate` (`authoring.go`) | **gap**—`os.WriteFile` | `..` safe (lexical check); **gap**—a symlinked directory inside the root that points outside is followed | safe—`yaml.Marshal` of a `yaml.Node`; heading cut at the first newline (body, not frontmatter) |
| `node edit` → `$EDITOR` + `okf.TouchModifiedFile` (`cmd/node.go`, `frontmatter_edit.go`) | **gap**—`os.WriteFile` (both branches) | **gap**—`filepath.Join(bundle, p)` with no check: `node edit ../x` opens and rewrites a file outside the root | safe—re-encodes through `yaml.Encoder`; the no-frontmatter branch writes a fixed-shape RFC3339 stamp |
| `node refresh` → `okf.RefreshApply` → `TouchModifiedFile` | gap (shared with `node edit`, one fix) | safe—path comes from `b.Nodes` (loader-enumerated) | safe (as above) |
| `node mv` → `okf.ApplyMove` (`mutate.go`) | the move is one `os.Rename` (safe); **gap**—inbound link rewrites use `os.WriteFile` | **gap**—`new` is never confined: `node mv a ../../evil` moves the node out of the root | **gap**—the rewritten link text is the destination path verbatim; a destination containing a line break lands raw in the file (and a link inside a frontmatter value is rewritten too) |
| `node rm` → `os.Remove` (`cmd/node.go`) | safe—a single unlink | safe—only a path `PlanRemoveOrphans` found in `b.Nodes` is removed | n/a |
| `node promote --name` → `okf.PromotePlan` / `PromoteApply` (`promote.go`) | **gap**—link rewrites and the new concept file use `os.WriteFile` | **gap**—`--name ../../x` escapes; `--name a/b` targets a directory that does not exist; `--name log` would create a reserved `log.md` concept (§3.1) | **gap**—the basename flows into rewritten link text (same vector as `node mv`) |
| `migrate apply` → `okf.MigrateApply` (`migrate.go`) | **gap**—node writes use `os.WriteFile` | **gap**—`plan.nodes[].path` is read from the hand-editable JSON plan and joined unchecked | safe—`renderMigratedNode` edits a `yaml.Node` and re-encodes |
| `migrate apply` → `bumpOkfSidecar` (`.okf`) | **gap**—`os.WriteFile` | safe—fixed `<root>/.okf` | **gap**—`"okf_version: " + plan.target_version` is concatenated into the line-keyed sidecar; a target containing a newline smuggles extra sidecar keys (e.g. `templates:`) |
| `migrate apply` → `bumpRootIndexMarker` (root `index.md`, §8/§12) | **gap**—`os.WriteFile` | safe—fixed `<root>/index.md` | safe—`setScalar` + `yaml.Encoder` |
| `migrate` (plan phase) → plan JSON (`cmd/migrate.go`) | **gap**—`os.WriteFile` | n/a—`--plan` is an explicit output path the user names, not bundle content | safe—`json.MarshalIndent` |
| `init` → `okf.Scaffold` (`reserved.go`) | **gap**—`os.WriteFile` per file | n/a—`dir` *is* the root being created | safe—fixed content (`SpecVersion` is a constant) |
| `index build` / auto-maintenance → `okf.WriteIndexWithOptions` (`reserved_lifecycle.go`) | **gap**—`os.WriteFile` | safe—every path derives from `IndexDirs(b)` | safe—the only frontmatter is the §12 `okf_version` marker, emitted with `%q` (a double-quoted, fully escaped scalar that YAML reads back as one string); pinned by a new fuzz target |
| `log append` / auto-maintenance → `okf.AppendLog` (§9) | **gap**—`os.WriteFile` | safe—fixed `<root>/log.md` | **gap**—the message is flattened at the first `\n` only; a lone `\r` is a CommonMark line ending, so `msg\r## 1999-01-01` forges a §9 date heading |
| `config set` → `okfconfig.Save` | **gap**—`os.WriteFile` | n/a—fixed user-config path | safe—`json.MarshalIndent` |
| `plugin install` → `plugin.copyExecutable` | safe—`CreateTemp` in the destination dir + `Rename` | safe—the installed name is `filepath.Base(source)`, so it cannot leave the plugin dir | n/a |
| `okfctl-search index` → `search.(*Store).Save` | **gap**—`os.WriteFile` | n/a—fixed `indexPath(dir)` | safe—`json.MarshalIndent` |
| `connect` → `git clone` (`gitsource.go`) | n/a—git owns the checkout | n/a—the destination is the user's explicit target | n/a |
| `cmd/gendocs` (dev tool) | noted, not hardened | noted | noted |

The roadmap names `--section` targets; no such flag exists on `next`, so there
is nothing to confine yet. `node link` (Increment 4) is the first planned
consumer and reuses the helpers below.

## Design

### Two helpers in one neutral package—`internal/fsutil`

The vector store (`internal/search`), the user config (`internal/okfconfig`),
and `cmd` all need the atomic writer, and none should import `internal/okf` for
it, so both helpers live in a tiny leaf package with no okfctl imports.

```go
// internal/fsutil/atomicwrite.go
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error

// internal/fsutil/confine.go
var ErrEscapesRoot = errors.New("path escapes the bundle root")
func ResolveInRoot(root, rel string) (string, error)
```

**`WriteFileAtomic`** writes `data` to a temp file created in the target's own
directory (same filesystem, so the rename is atomic), `Sync`s it, sets the
mode, closes it, and `Rename`s it over `path`. Any failure removes the temp
file and leaves `path` untouched. It mirrors `os.WriteFile` semantics where they
matter: an existing target keeps its permission bits (`perm` applies only on
create), and a target that is a symlink is written *through* (the link's target
is replaced; the link survives). The temp name is `.<base>.tmp-<random>`—it
never ends in `.md`, so a crash-orphaned temp is never loaded as a node.

**`ResolveInRoot`** turns a user-supplied bundle-relative path into an absolute
path inside `root`, or refuses it:

- empty, or the root itself (`.`) → refused;
- a control character (C0 or DEL) anywhere → refused. A path is written into
  link text by `node mv` / `node promote`; a line break there is the
  injection vector in the table above;
- an OS-absolute path with a volume (`C:\x`, `\\server\share`) → refused;
- a leading `/` is **kept as bundle-root-relative**, matching §6.1's
  `/`-absolute link form and the behavior `node new` already pins
  (`TestNewNode_RejectsPathEscape`): `/deep/x.md` resolves to `<root>/deep/x.md`;
- after `Clean`, any `..` that climbs above the root → `ErrEscapesRoot`;
- the deepest *existing* ancestor of the result is `EvalSymlinks`-resolved and
  must still sit inside the resolved root, so a symlinked directory pointing out
  of the bundle is refused while an in-bundle symlink is fine.

It returns the lexical absolute path (not the symlink-resolved one), so callers
write where the user pointed. The check is a pre-write gate, not a kernel-level
`openat` confinement: a symlink swapped in between the check and the write is
out of scope for a local, single-user CLI.

### Call-site swaps (one commit per gap)

- `WriteFileAtomic` replaces every `os.WriteFile` marked **gap** above.
- `ResolveInRoot` gates `NewNodeFromTemplate` (replacing its lexical check),
  `node edit` (before the editor launches), `ApplyMove` (`old` and `new`),
  `PromotePlan` / `PromoteApply` (the destination), and `MigrateApply`
  (each `plan.nodes[].path`).
- `PromotePlan` additionally refuses a `--name` that is not a single path
  segment, or that would produce a reserved filename (§3.1).
- `bumpOkfSidecar` refuses a `target_version` containing a control character
  (the sidecar is line-keyed; there is no encoder to route through).
- `AppendLog` flattens the message at the first CommonMark line ending
  (`\n` or `\r`), not just `\n` (§9 date headings stay unforgeable).

### Fuzz targets—write paths that round-trip through a parser

Extending `2026-08-08-fuzz-untrusted-parsers.md`: each target writes through the
real writer, reads the result back through the real parser, and asserts the
injection invariant. All are registered in the CI `fuzz` job.

| target | writer → parser | invariants |
|---|---|---|
| `FuzzNewNodeFrontmatter` | `NewNode(type, title)` → `ParseFrontmatter` | never panics; on success the frontmatter keys are exactly `type`, `created`, `modified` (+ `title` when non-blank)—no smuggled key |
| `FuzzTouchModifiedRoundTrip` | `TouchModifiedFile` → `ParseFrontmatter` | never panics; on error the file is byte-identical (atomicity); on success `modified` is present, every pre-existing key survives (§4.1 preserve unknown keys), and the body is unchanged |
| `FuzzRootIndexMarker` | `rootFrontmatter` (§12 marker) → `ParseFrontmatter` | the block holds exactly one key, `okf_version`; for valid UTF-8 its value round-trips exactly |
| `FuzzAppendLog` | `AppendLog` → CommonMark line split | a fresh log has exactly one `## ` heading—today's (§9) |

## Controls

- **Positive:** `node new ../x`, `node edit ../x`, `node mv a ../../x`,
  `node promote --name ../x`, a plan node path `../x`, a symlinked directory
  escaping the root, and a destination with a newline are each refused with
  nothing written outside the root; a plan `target_version` of
  `0.2\ntemplates: evil` is refused; a log message `a\r## 1999-01-01` forges no
  heading; an interrupted atomic write leaves the original bytes.
- **Negative:** `a/b/c.md` writes at `<root>/a/b/c.md`; `/deep/x.md` stays
  bundle-root-relative; an in-bundle symlinked directory is accepted; a title
  containing `---` in prose (`Before --- after`, and one with a real
  `\n---\n`) writes a node whose frontmatter has only the expected keys and
  whose title round-trips; a v0.1 fixture (legacy `timestamp`, body
  `# Citations`) migrates and touches exactly as before.

## Non-goals

- No new `validate` or `lint` rule; no change to what a bundle may contain.
- No `openat2`/`os.Root`-style kernel confinement (residual TOCTOU is noted
  above).
- `Scaffold` overwriting an existing `index.md`/`log.md` in a non-empty dir is
  pre-existing `init` behavior, not a write-safety property; left alone.
