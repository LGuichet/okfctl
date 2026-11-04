# analyze freshness: report a passed §5.5 `stale_after`

> A producer that writes `stale_after` is saying "recheck this on that date."
> okfctl parsed the key (`Node.StaleAfter`, `Node.IsStale`) and never reported
> it. `analyze` now lists every node whose `stale_after` has passed.

## Problem

OKF v0.2 §5.5 defines `stale_after` as an optional absolute instant: a concept
is stale when `now >= stale_after`. `analyze`'s two freshness signals both work
from the node's age (`verified[].at` → `generated.at` → `modified` → `created`)
against `--stale-days`. A concept regenerated last week with `stale_after` set
to last month reads as fresh to both, so the one date its producer chose to
declare never reached the report. `IsStale` existed and had conformance tests,
but no command called it. §11 asks consumers to "derive … staleness only from
the fields specified here", and `stale_after` is the field §5.5 specifies.

## Decision

`analyze` gains `freshness.expired`: one `{path, stale_after}` entry per node
where `IsStale(now)` holds. The human report prints it as "Freshness — past
stale_after (§5.5, recheck)".

- **Report, not gate.** `analyze` exits 0 on findings by contract. No `lint`
  check is added: a date passing would turn a green `lint --strict` red with no
  change to the bundle, and §5.5 makes `stale_after` informational, not a
  conformance requirement. `validate` is untouched.
- **No threshold.** The producer set the date; `--stale-days` does not apply.
- **Silent when absent or unparseable.** §11: an optional key never fails a
  bundle, and a value that isn't an instant carries no date to compare.
- **Separate from `stale`.** A node can be expired and young, or old and not
  expired; the lists don't merge.

## Controls

- Positive: a node with `stale_after` in the past and a one-day-old
  `generated.at` is listed, with its declared value, and doesn't appear in
  `stale`. The exact instant (`now == stale_after`) is listed, per `>=`.
- Negative: a future date, one second in the future, an absent key and an
  unparseable value (`next quarter`) are all silent.
- JSON: `expired` is `[]`, never `null`, matching `stale` and `time_sensitive`.
