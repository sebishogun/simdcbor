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

## The gate that could not fail was hiding a red benchmark

**Believed.** `make bench-check` was known to be unable to fail — it pipes
through `tee` with no `pipefail` and then ends in an unconditional `echo`,
so the status `make` records is the echo's. Recorded as an exposure: a
flaw waiting for a regression to launder.

**Actually.** It was already laundering one. `BenchmarkSweep` compares
this decoder against fxamacker on four shapes, and the `deep` shape nests
40 levels while fxamacker's decoder caps nesting at 32 by default. Its arm
had been failing outright — `cbor: exceeded max nested level 32` — for as
long as the shape has existed. The gate reported success every time.

**How it surfaced.** Replacing the target with one that compares against a
committed baseline and lets the status through. Its first run went red on
a benchmark, not on a regression.

**Source.** `Makefile` `bench-check`; `sweep_test.go` `BenchmarkSweep`;
fxamacker `DecOptions.MaxNestedLevels`.

**Consequence.** The comparison arm sets `MaxNestedLevels: 64`, so both
decoders do the same work on the same bytes rather than one of them timing
an error path. `scripts/bench-check.sh` compares minima against
`testdata/bench.txt` with no pipe carrying the verdict, and the baseline
records the load average it was captured at.

The lesson is narrower than "use pipefail": a gate that cannot fail is not
merely unprotected, it is actively hiding whatever is already broken, and
the length of time it has been green says nothing.

## The identical-boundary contract costs more than it looks

**Believed.** `Skip`'s accept/reject boundary is identical to
`Unmarshal`'s (architecture.md), and the one known violation was the
simple-value range.

**Actually.** Four violations, three of them found by the fuzz written to
check the first:

    0xe0-0xf3, 0xf8 xx   simple values outside the value model
    a1 00 00             a map key that is not a string
    61 cd                a text string that is not valid UTF-8
    a1 c9 41 30 30       a map key that is a *tagged* byte string

Each one is `Unmarshal` applying the value model, not CBOR's grammar.
Honouring the contract therefore means `Skip` has to know the value model
too: it now rejects unsupported simple values, validates UTF-8 on text
strings, and peeks through tags to decide whether a key would decode to a
Go string.

**Source.** `skip.go`; `decode.go`'s `map[string]any` and `simd.ValidUTF8`.

**Consequence.** The contract holds, pinned two ways: a test enumerating
all 256 head bytes and all 256 two-byte simple payloads, and a fuzz that
asserts accept-parity and span-parity on every prefix of arbitrary input
(20.5M executions clean after the fourth fix).

What is not yet measured is the cost. `Skip` was described as pure
arithmetic, and UTF-8 validation is a scan of the string it is skipping
past — on text-heavy data that is real work the previous `Skip` never did.
The machine has not been quiet enough this session to measure it. If it
proves expensive, the alternative is a different contract rather than a
different implementation: `Skip` guarantees framing, `Unmarshal`
guarantees representability, and the fuzz asserts one direction instead of
two. That choice is open; this entry is where it starts.

## The plan's example for the two deterministic orders was the mistake it warns about

**Believed.** Task 3 named a key pair that separates RFC 8949's two
orderings: `h'ff'` against `h'0000'` — "bytewise sorts `h'0000'` first
(0x00 < 0xff); length-first sorts `h'ff'` first (shorter)."

**Actually.** Both orderings put `h'ff'` first. The same task says the
comparator runs over "the full encoded keys — head and body", and with
heads included those keys are `41 ff` and `42 00 00`: bytewise decides on
`0x41 < 0x42` and never reaches the contents. The example compared the
contents, which is precisely the error the comparator exists to prevent
and which the neighbouring `"z"` vs `"aa"` example gets right.

**How it surfaced.** Writing the test from the plan. It failed, and the
implementation was correct.

**Source.** RFC 8949 §4.2.1 and §4.2.3; `value/order.go`.

**Consequence.** A pair that does separate them has to cross major types,
because within one major type a shorter encoding almost always carries a
smaller head and the two rules agree. `uint 500` (`19 01 f4`, three bytes)
against `h'ff'` (`41 ff`, two) does it: bytewise takes the uint on
`0x19 < 0x41`, length-first takes the byte string on length. The plan's
pair is kept in the test as well, pinned to what the rules actually do, so
the claim cannot come back.
