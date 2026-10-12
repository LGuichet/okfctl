# Spec: hybrid search—`okfctl-search --hybrid` fuses BM25 and semantic rankings

Roadmap: `2026-10-12-agent-memory-ports`, Increment 5.
Branch: `feat/search-hybrid`  Base: `feat/search-bm25` (Increment 2), itself on
`next` @ 634eeb1

## Problem

`okfctl-search --semantic` ranks by embedding similarity. A static embedding
blurs exactly the tokens a curator searches for most: an identifier
(`BT-8`, `MOB_REQ`, `isSalesProspectingForbidden`), a rule code (`BR-FR`), an
acronym (`GTPCA`, `ISCA`). The node that defines the identifier can sit far
down the semantic list while BM25 (Increment 2) puts it first. The reverse is
also true: a paraphrase ("how long must the sales data be kept") shares few
tokens with the node that answers it, and semantic wins.

`--lexical-gate` (#66) already addresses the first case, but it is a
filter-and-append over the semantic list driven by a substring match set, not a
ranking. Increment 2 gives the plugin a real lexical **ranking**; this
increment fuses the two.

## Design

### Ranking is consumer-side; the spec floor is untouched

OKF v0.2 §1 lists "prescribing storage, serving, or query infrastructure" as a
non-goal, so how a consumer orders search hits is not spec-defined. This change
adds no `validate` rule and no lint rule. It reads only what the spec defines:
`title`, `tags` and `type` (§4.1—`type` has an open vocabulary and an unknown
value is ranked like any other) and the Markdown body (§4.2). Reserved files
(`index.md`, `log.md`, §3.1) are never ranked, on either side: the semantic
index never embeds them and `okf.LexicalDocuments` never returns them.

### Reciprocal rank fusion

```
hybrid(d) = Σ_r∈{semantic, lexical}  1 / (RRFK + rank_r(d))     RRFK = 60
```

- `rank_r(d)` is 1-based. A node absent from a list contributes nothing from
  that list.
- `RRFK = 60` is the literature default (Cormack, Clarke & Büttcher, 2009), a
  named constant (`search.RRFK`). No score normalisation: cosine and BM25 live
  on unrelated scales, and RRF reads only ranks.
- Ties on the fused score break by path ascending, so the order is total and
  deterministic. (A tie is common, not rare: a node at semantic rank 3 alone and
  one at lexical rank 3 alone both score `1/63`.)
- The printed score is the fused score (at most `2/61 ≈ 0.0328`), not a cosine.
  The snippet is the semantic side's best passage; a node that only the
  lexical side found has no snippet line.

### The two rankings

- **Semantic:** `QueryWith` with an unbounded k and the caller's options, so
  recency decay and `MinRelevance` keep their exact current semantics: the
  floor cuts on RAW cosine before decay re-scores, and the semantic rank is the
  rank **after** decay. A node cut by `MinRelevance` is absent from the semantic
  list; it can still enter the fused list through the lexical one. That is
  deliberate—`MinRelevance` is a floor on embedding similarity, and a node
  that names the query's rare term is exactly the case where the embedding is
  not to be trusted. A caller that wants a hard cut on both sides uses filters.
- **Lexical:** BM25F (Increment 2: `lexical.K1`, `lexical.B`,
  `lexical.DefaultWeights`, the shared analysis chain) over the bundle's
  concept nodes. Term-wise: every node with a positive score, nothing else. An
  all-stopword query has an empty lexical list, so hybrid reduces to the
  semantic order exactly.
- **Filters apply before both rankings.** The same `Filter.keep` predicate and
  the same per-node metadata (§4.1 type/tags resolved from the live bundle)
  select the candidates on both sides, so a node a filter drops can enter
  neither list. Because the lexical side is filtered *before* ranking, BM25's
  `N`, `df` and average field lengths are computed over the candidate set—the
  documented Increment 2 contract ("a caller with candidate filters filters
  `LexicalDocuments` first and calls `lexical.Rank`").

### Surface

- `okfctl-search --semantic "<q>" --hybrid [bundle-dir]`—opt-in, off by
  default. Without `--hybrid` the output is byte-identical to today.
- `--hybrid` composes with `--k`, `--path/--type/--tag/--not-*`,
  `--half-life`, `--decay-floor`, `--min-relevance`, `--embedder`,
  `--model-path`.
- `--hybrid` with `--lexical-gate` is a usage error. Both re-order the semantic
  list with lexical evidence; stacking them would apply that evidence twice
  with no defined meaning.
- `--hybrid` always loads the bundle (the lexical side needs the live prose).
- The HTTP `/api/v1/search` surface is unchanged in this increment; it can gain
  a `hybrid` field later on the same `search.HybridQuery`.

### Library API

`internal/search`:

```go
const RRFK = 60
func FuseRRF(semantic []Result, lex []lexical.Scored, k int) []Result
func HybridQuery(s *Store, e Embedder, b *okf.Bundle, q string, k int, opts QueryOptions) ([]Result, error)
```

`HybridQuery` refuses `opts.LexicalGate != nil` and a nil bundle with an
error, and cuts to `k` after fusion (`k <= 0` returns everything).

## Measurement—the query set

An offline benchmark, never run at query time, living with the existing
retrieval eval (`internal/search/eval_test.go`).

### Format

One JSON file per bundle under `internal/search/testdata/queryset/`, named
after the bundle directory:

```json
{
  "bundle": "nf525-lne",
  "queries": [
    {
      "id": "nf525-gtpca",
      "query": "GTPCA",
      "kind": "exact-term",
      "relevant": ["infocert-referential/grand-totals.md"]
    }
  ]
}
```

- `kind` is `exact-term` (a rare identifier or acronym) or `paraphrase` (a
  question that shares little vocabulary with the answer). Metrics are
  reported per kind as well as overall, so a gain on one kind cannot hide a
  loss on the other.
- `relevant` lists the bundle-relative paths of every node that answers the
  query—hand-vetted against the corpus, usually one or two. Unknown fields are
  rejected so a typo (`relevent`) fails loudly instead of silently scoring 0.
- A query set is checked in; the corpus is not. The paths name nodes in the
  `market-fr` knowledge base (the corpus the Increment 2 spec already pins).

### Metrics

- **recall@5** = `|relevant ∩ top-5| / |relevant|`, averaged over queries.
- **MRR** = mean over queries of `1 / rank` of the first relevant node (0 if
  none is ranked).
- Reported for **lexical** (BM25F alone), **semantic** (the embedder alone) and
  **hybrid** (RRF), overall and per kind.

### Running it

```sh
OKFCTL_EVAL_KB_ROOT=<dir holding the bundles> \
OKFCTL_TEST_MODEL_DIR=<model2vec model dir> \
  go test ./internal/search/ -run TestEval_HybridQuerySet -v
```

The test skips without `OKFCTL_EVAL_KB_ROOT`. It builds the semantic index in
memory (nothing is written into the bundle). Without `OKFCTL_TEST_MODEL_DIR` it
falls back to the offline hash embedder and labels every row
`hash (not semantic)`—those numbers exercise the harness and are **never** a
promotion basis.

### Promotion rule

`--hybrid` becomes the default only if, on the real corpus with a real
model2vec model, it beats semantic on **both** recall@5 and MRR. Until then it
stays opt-in. Flipping the default is a separate change; this increment never
does.

## Acceptance criteria

1. **Positive control:** a query whose exact rare term only one node carries,
   which the embedder ranks low, is lifted into the top results by hybrid
   (fake-embedder unit test; real-corpus exact-term rows).
2. **Negative control:** a paraphrase query whose answer semantic ranks first
   is not pushed down by hybrid when the lexical side does not rank another
   node above it in both lists (fake-embedder unit test), and an all-stopword
   or no-match query gives exactly the semantic order.
3. Fusion is deterministic: `RRFK` is 60; ties break by path; a node in one list
   only scores `1/(60+rank)`.
4. Filters apply before both rankings: a filtered-out node never appears, even
   when it is the top BM25 hit.
5. Decay and `MinRelevance` are unchanged on the semantic side; a node below
   `MinRelevance` can still enter through the lexical side.
6. `--hybrid` with `--lexical-gate` is rejected; without `--hybrid`, output is
   byte-identical to the base branch.
7. `validate` and `lint --strict` on the real corpus are byte-identical before
   and after.
8. recall@5 and MRR for lexical, semantic and hybrid are recorded in the PR.

## Known property of plain RRF

Every lexical hit is also in the semantic list (the semantic side ranks every
candidate). A node that semantic ranks first but that has **no** lexical match
scores `1/61`, and any node in both lists with ranks `(s, l)` beats it when
`1/(60+s) + 1/(60+l) > 1/61`—true for every `s, l` up to about 60. So on a
query whose answer shares no token with the query while other nodes do, hybrid
pushes the semantic winner down. Acceptance criterion 2 is scoped accordingly;
the real-corpus paraphrase rows are what show how often this happens.

## Spec citations

- OKF v0.2 §1 (non-goals: query infrastructure)—ranking is consumer-side.
- §3.1 (reserved filenames)—`index.md`/`log.md` are never ranked.
- §4.1 (`type` required, open vocabulary; `tags` optional)—the filter and
  ranking fields, with no new enforcement.
- §5.2 / §13.1 (`generated.at`, legacy `timestamp`)—the recency decay input,
  unchanged.
- §11 (conformance: never reject for an unknown type or key)—unchanged; no
  `validate` rule added.
