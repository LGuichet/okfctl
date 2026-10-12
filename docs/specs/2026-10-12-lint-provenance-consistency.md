# okfctl—lint provenance consistency: spec gate and decision

**Status:** Decided at the spec gate—no check ships · **Owner:** Casey West · **License:** Apache-2.0
**Source of truth:** OKF spec v0.2 §4.2, §5.1, §5.2, §11, §13.1 · **Roadmap:** Increment 3 of
`docs/plans/2026-10-12-agent-memory-ports.md`

## Purpose

The roadmap proposed two `lint` findings that cross-check a node's provenance
claims against each other:

- `verified-before-generated`—a `verified[].at` earlier than `generated.at`.
- `unknown-citation`—a body citation key that matches no `sources[].id`.

Both were gated on the spec: implement only what OKF v0.2 supports, drop what it
contradicts, and stop on what it leaves ambiguous. This file records the gate.
**Outcome: zero checks ship.** `verified-before-generated` is dropped because the
spec contradicts it; `unknown-citation` is held because the spec is ambiguous on
the one question the check depends on. No code changes; no `validate` change.

## Spec gate

Upstream fetched from
`https://raw.githubusercontent.com/GoogleCloudPlatform/knowledge-catalog/main/okf/SPEC.md`;
header reads **Version 0.2**. The roadmap's "§5.2 (provenance)" is a v0.1-era
pointer: in v0.2, `sources` is **§5.1 Provenance** and `generated`/`verified` is
**§5.2 Trust**.

### `verified-before-generated`—dropped (the spec contradicts it)

§5.2 defines the two axes and then states, of their relation:

- `generated.at` marks "the content's last meaningful change" (§5.2).
- "`verified` is independent of `generated.at`" (§5.2).
- "content can change without re-confirmation" (§5.2).

A `verified[].at` earlier than `generated.at` is exactly the state "content
changed after the last confirmation"—a state §5.2 names as legitimate. Flagging it
as a contradiction would turn a permissive clause strict (AGENTS.md
over-conformance). The useful signal it gestures at—"verification is older than
the content"—is a freshness question, and `analyze` already answers it from
`verified[].at` (`docs/specs/2026-08-12-analyze-freshness-verified-basis.md`).
**Dropped, not deferred.**

### `unknown-citation`—held (the spec is ambiguous)

What §5.1 defines:

- Per-claim attribution: "use a markdown footnote whose label is a `sources[].id`" (§5.1).
- "The footnote label is the join key into `sources`" (§5.1).
- `id` "SHOULD be present when the body cites the source" (§5.1).
- §4.2 restates it: attribution "uses markdown footnotes keyed to `sources` entries".

What §5.1 does not define: whether **every** markdown footnote in a concept is a
citation. The spec defines the form of a *resolved* citation (a footnote whose
label *is* an id); it gives no syntax that marks a footnote as *intended* to be a
citation. A footnote `[^x]` whose label matches no `sources[].id` is therefore
either:

1. a citation with a broken or non-conforming key (the defect the check targets), or
2. an ordinary Markdown footnote—an aside or a definition—that cites nothing,
   which nothing in §4.2, §5.1, or §11 forbids.

The two are byte-identical in shape (`[^label]` plus `[^label]: prose`); the
spec's own §5.1 example definition is prose. §5.1 also tells consumers to resolve
attribution "not by parsing the footnote prose", so the definition text cannot
be the discriminator either. Any rule that picks (1) over (2) is an
interpretation the spec does not make, and the brief for this increment says an
ambiguity is surfaced, not resolved in code.

## Real-corpus evidence

Read-only survey of the three real bundles, using the production loader
(`Node.Sources()`, `Node.Verified()`, `Node.Generated()`), footnote references
taken from the body outside fenced and inline code:

| Bundle | Nodes | Nodes with `verified` | Footnote nodes | Resolved labels | Unmatched labels (nodes) | Legacy `# Citations` |
|---|---|---|---|---|---|---|
| ifpos-interface-cases | 7 | 0 | 2 | 9 | 0 (0) | 0 |
| nf525-lne | 71 | 0 | 69 | 133 | 6 (5) | 0 |
| plateforme-agree | 70 | 0 | 21 | 67 | 40 (4) | 0 |

`verified-before-generated` would have fired **0** times regardless: no corpus
node carries `verified`.

The 46 unmatched labels fall into three classes, and the corpus holds all three:

- **Prose footnotes—4 labels, 4 nodes (nf525-lne).** `[^tpv]`, `[^scope]` (×2),
  `[^open]` are glossary-style definitions (e.g. `[^tpv]: TPV (Terminal Point de
  Vente): a point-of-sale terminal …`). These are case (2) above; a check that
  fires on them is a false positive, and the roadmap's own negative control
  requires a prose footnote to stay silent.
- **Mismatched citation keys—2 labels, 1 node (nf525-lne `README.md`).**
  `[^nf525-p10]` where the source id is `nf525-v2.3-p10`, and `[^boi]` where it is
  `boi-tva-decla-30-10-30`. These are the true positives the check targets.
- **Locator-suffixed citation keys—40 labels, 4 nodes (plateforme-agree).**
  Labels such as `[^gt5-25]`, `[^z14a-25]`, `[^v14-4-2-1]`: a source id plus a
  case or section locator. The spec has no locator syntax, so these are
  non-joinable under §5.1, but they are a deliberate house convention, not typos.

So the most permissive implementable rule ("an unmatched footnote in a node that
declares `sources`") would report **46** findings, of which **4** are prose
footnotes the spec permits and **40** are a consistent pinpoint convention the
spec neither defines nor forbids. Only **2** are unambiguous key defects.

Note on the roadmap's negative control: "a footnote that is prose (the existing
`lint.go` footnote handling)" refers to `proseBody`'s `[n]: running prose` line
handling—a reference-definition lookalike with a bracketed label, not a `[^label]`
footnote. That handling does not distinguish citation footnotes from prose
footnotes, so it cannot serve as the discriminator.

## Options for a decision (not taken here)

Each option below is an interpretation the spec does not make; picking one is the
review decision this file exists to surface.

- **A. Every unmatched footnote is a finding** (nodes with v0.2 `sources` only;
  skip nodes whose sources come from the legacy `# Citations` list, §13.1).
  Corpus: 46 findings. Treats §5.1 as reserving footnotes for citations; fires on
  the 4 prose footnotes.
- **B. A, but only when the label is a near miss of an id**—a prefix of an id, or
  an id a prefix of it. On the corpus it catches `[^boi]` and the `gt5-*`
  locators, but misses `[^nf525-p10]` (neither string prefixes the other) and the
  locators whose base is not itself an id. The near-miss heuristic is
  okfctl-invented and does not track the defect class well on this corpus.
- **C. A, but only when the node shows it uses footnote attribution**—at least
  one other footnote in the same node resolves to an id. Still fires on the 4
  prose footnotes (all four nodes also carry resolving citations).
- **D. Ship nothing; ask upstream.** Propose to the spec that footnotes in a
  concept with `sources` are citations (or define a locator syntax such as
  `[^id]` plus prose locator), then implement against the clarified text.

Recommendation for the reviewer: **D**, optionally followed by A once upstream
confirms footnotes are reserved for citations. The corpus's 2 genuine key
defects can be fixed by hand today; the 40 locator labels are the bigger
finding and are a corpus-convention question, not a lint rule.

## v0.1 compatibility

No reader changes. The §13.1 fallbacks in `provenance.go`—legacy `timestamp` for
`generated.at`, legacy body `# Citations` for `sources`—are untouched and remain
load-bearing. If option A is later approved, it must skip any node whose
`sources` is absent and whose citations come from the legacy `# Citations` list
(`SourceCitations()` falls back to `citationCount`), so a v0.1 node never fires.

## Out of scope

Any `validate` rule (§11 forbids rejecting a bundle for provenance content);
corpus edits; an okfctl-defined locator syntax.
