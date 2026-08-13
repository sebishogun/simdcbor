# wrong.md

Measurements and findings that argued against a change, or work deferred
on measurement. **Rule: an entry needs a source — the measurement, or the
commit that produced it.** Nothing inferred from implementation shape
belongs here. Entries are records; they are not updated when later work
outgrows them, they are superseded by a new entry.

## Lazy values: deferred on measurement, not on hope

**Status:** deferred (documented in `decode.go` and committed with
`d8a9077`; the reasoning predates it in the original decode commit
`fb05cdd`).

The decode sweep against fxamacker narrows exactly where allocation
dominates:

| shape | ratio | note |
|---|---|---|
| strings | 1.84x | mostly copying, simd helps |
| numbers | 1.67x | mostly copying |
| nested record | 1.55x | mixed |
| 5,000-element array | 1.35x | allocation-bound: a `[]any` of boxed floats is mostly the boxing |

The huge-array row is the narrowest because no faster scan removes the
boxing. The lever simdjson documents for this is a lazy value — keep each
item as a byte range and decode it only when read, the way fastjson does —
which turns a filter-then-read workload into near-zero allocation. That is
a real win and a real interface change (`Value` handles instead of `any`).

It was measured and deferred rather than unmeasured: `Skip` already
delivers the allocation-free traversal for the filtering case — the filter
benchmark (decode 1 record in 100, skip the rest) runs 8.4x faster than
decoding all of them (79 us vs 662 us) — which is the larger half of what
lazy values would buy, at zero interface cost. When the full codec lands,
Phase 6 re-runs the sweep and records the delta; if lazy values do not
deliver the predicted allocation drop, that finding belongs here.

## Presize overflow on malformed length headers

**Status:** fixed in `fb05cdd`; regression-covered by the pre-flight
bound.

The original decoder presized container allocations from the header's
declared length before bounding it by the remaining input. Fuzzing with
random bytes found it: a crafted length header could overflow the presize.
The fix — reject `arg > len(b)-i` (one byte per item, two per pair)
before allocating, and cap capacity at `min(arg, 1024)` — is the standing
warning for every future allocation on the decode path: **bound by the
remaining input before the allocation, always.**

## Canonical claims: bounded by proof

**Status:** record, not a finding against a change.

The encoder sorts keys bytewise and writes shortest heads and
float32-when-it-round-trips. The word "canonical" appears in
`encode.go`'s doc comment; the test that pins it proves only
same-map-same-bytes. It does **not** implement RFC 8949 §4.2.1
length-first ordering and it never emits `float16`. The README now scopes
the claim to what the code and tests prove; anyone reading "canonical"
in this codebase should read "canonical within the subset, bytewise
sort, no float16".
