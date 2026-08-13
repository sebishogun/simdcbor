# wrong.md

Measurements and findings that argued against a change, or work deferred
on measurement. **Rule: an entry needs a source — the measurement, or the
commit that produced it.** Nothing inferred from implementation shape
belongs here. Entries are records; they are not updated when later work
outgrows them, they are superseded by a new entry.

## Lazy values: deferred on measurement, not on hope

**Status:** deferred, documented in the `decode.go` comment added with
`d8a9077` (the review round that also added Skip, Marshal, and the shape
sweep); the original decode commit `fb05cdd` did not discuss lazy
values.

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
Phase 7 re-runs the sweep and records the delta; if lazy values do not
deliver the predicted allocation drop, that finding belongs here.

## Presize overflow on malformed length headers

**Status:** fixed in `fb05cdd`; bounded in code by the pre-flight check
and presize cap, but **not yet pinned by a test** — the pin
(`TestPresizeBounded`) is scheduled in the plan's Task 2.

The original decoder presized container allocations from the header's
declared length before bounding it by the remaining input. Fuzzing with
random bytes found it: a crafted length header could overflow the presize.
The fix — reject `arg > len(b)-i` (one byte per item, two per pair)
before allocating, and cap capacity at `min(arg, 1024)` — is the standing
warning for every future allocation on the decode path: **bound by the
remaining input before the allocation, always.**

## Canonical claims: bounded by proof

**Status:** record, not a finding against a change.

The encoder sorts keys with `sort.Strings` (content-bytewise over Go
string keys) and writes shortest heads and float32-when-it-round-trips.
The word "canonical" appears in `encode.go`'s doc comment; the test that
pins it proves only same-map-same-bytes. The sort is **not** RFC 8949
§4.2.1 core deterministic: that order compares the full encoded keys,
head included, so text keys of differing encoded lengths order by length
through the head (`"z"` → `61 7a` before `"aa"` → `62 61 61`), while
`sort.Strings` orders by content (`"aa"` first); the two coincide where
the encoded head cannot reverse the content comparison — e.g.
equal-length text keys, whose head bytes are identical. The §4.2.3
length-first legacy ordering and `float16` emission are not implemented
either. The README now scopes the claim to what the code and tests
prove; anyone reading "canonical" in this codebase should read
"canonical within the subset: sorted Go string keys, shortest heads,
float narrowing, no float16, no length-first, no encoded-bytewise
order".

## Skip accepts simple values Unmarshal rejects

**Status:** open bug, scheduled as Stage 0 of the production plan.
Source: `skip.go:31` (`case mtUint, mtNegInt, mtSimple: return j, nil`)
against `decode.go:116-131` (only `ai` 20–23 and 25–27 handled); the
test's blindness is in `skip_test.go:48-56`.

`Skip`'s simple-value case returns the head span for every `ai` the
argument reader accepts — including `ai` 0–19 (`0xe0`–`0xf3`) and the
two-byte `0xf8` form — while `Unmarshal` rejects all of those with
`ErrMalformed`. The doc comment on `skip.go` claims the accept/reject
boundary is identical to `Unmarshal`'s; it is not, and the test that
claims to enforce it cannot see it: the generated corpus never produces
those simple values, and the random-bytes loop assigns both errors to
`_`, asserting nothing about agreement.

The fix direction is not "add the same rejection to Skip": the approved
target supports the full simple-value model, so both paths become
policy-driven — the subset rejects consistently, the full codec accepts
consistently — with corpus and assertion work scheduled in the plan's
decoder task.
