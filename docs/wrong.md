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
lazy values would buy, at zero interface cost. R1's lazy-value phase has
landed; the committed baseline was captured under load and CBOR-V1-09 owns the
fresh quiet-host workload decision.

## Presize overflow on malformed length headers

**Historical status:** fixed in `fb05cdd`; the test pin was still scheduled.
**Resolution:** `TestPresizeBounded` now pins the pre-flight check and presize
cap; `TestDepthCap` and `TestTruncationNeverDecodesClean` pin adjacent limits.

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

**Historical status:** open bug, scheduled as Stage 0 of the production plan.
**Resolution:** closed 2026-08-14 by the measured `Skip`/`SkipStrict` split;
see "The identical-boundary contract costs more than it looks" below.
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

**Closed 2026-08-14.** The four divergences are gone, structurally rather
than by repair: the root package's decoder and skipper were replaced by
one walk in `internal/codec` with two build steps, so there is no second
implementation to drift. `Skip` keeps the framing boundary and
`SkipStrict` the decoding one, which is the split the measurement below
forced.


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

**Measured, and it decided the design.** Instructions retired on
`BenchmarkFilterStream/skip-then-decode-1pct`, three interleaved runs of
each build, spread under 0.05% — a load-independent metric, which is what
made it usable on a machine that was not quiet:

    Skip, framing only (before)                  757.7 M
    + value-model checks, no UTF-8 validation    833.8 M   +10.0%
    + UTF-8 validation                         1,458.5 M   +92.5%

Cycles moved with it: 137 M to 274 M. The identical boundary nearly
doubles the cost of the operation whose entire purpose is to be the cheap
arm of a filter, and three quarters of that is validating the contents of
strings the caller is discarding unread.

So the contract splits rather than the implementation. `Skip` judges
framing and accepts a superset of what `Unmarshal` does. `SkipStrict`
carries the identical boundary for callers that need it — the adapter's
case, which is what Stage 0 was protecting. The fuzz asserts both: exact
parity for `SkipStrict`, one direction for `Skip`.

`Skip` lands at 790.5 M, **+4.3%** over the original, and that residual is
the `strict` flag threaded through the recursion: the frame grows from 80
to 96 bytes and the function from 306 to 377 instructions. Duplicating the
traversal into two functions would recover it and was not done. One
boundary with two implementations is the failure this family keeps
finding, and 4.3% is not worth buying that risk.

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

## The indefinite-length sentinel was a legal argument value

**Believed.** A head reader can return "indefinite length" by handing back
a sentinel argument. `^uint64(0)` is the obvious choice: no real length is
that large.

**Actually.** It is not a length, it is an argument, and `1b
ffffffffffffffff` carries exactly `^uint64(0)` as the value of the largest
unsigned integer CBOR can express. With the sentinel in place that integer
decoded as malformed, and so did its negative counterpart `3b
ffffffffffffffff`, the `-2^64` endpoint the value model exists to
represent.

**How it surfaced.** The RFC 8949 appendix A vectors, on their first run
against the new decoder. Two of them, immediately.

**Source.** `internal/codec/decoder.go` `head`.

**Consequence.** Whether a head declares its length is a separate fact
from what the argument says, so it is a separate return value. The lesson
generalizes past CBOR: a sentinel drawn from the same domain as the data
is only safe while the data does not reach it, and "no real length is that
large" was true of lengths and false of arguments — which is the same
mistake in one word.

The vectors found a second defect in the same run: a definite-length text
string was not being validated as UTF-8, only a chunked one was. Both are
pinned by the vector table now.

## Lazy values did not beat skipping, and the entry is the deliverable

**Believed.** Framing items as byte ranges and decoding only the matches
would beat skip-then-decode on a filtering workload: the plan predicted an
allocation drop and asked for the delta.

**Actually.** It is marginally slower and allocates slightly more.
Instructions retired over 2,000 iterations of a 2,000-record stream with a
1% match rate, two runs each, spread under 0.05%:

    decode-all               15,773 M   1,200,008 B/op   8,000 allocs/op
    skip-then-decode-1pct     5,234 M      12,000 B/op      80 allocs/op
    frame-then-decode-1pct    5,374 M      13,920 B/op     100 allocs/op
    frame-only                5,150 M           0 B/op       0 allocs/op

Framing costs 2.7% more instructions than skipping and 20 more allocations
per run, because materializing a frame builds a fresh decoder over its
bytes where the skip path decodes in place. The prediction was that lazy
values would drop allocations; against skip-then-decode there were almost
none left to drop — that work had already been done.

**How it surfaced.** Running the benchmark the plan asked for, on the
implementation the plan asked for.

**Source.** `internal/codec/raw.go`, `internal/codec/bench_test.go`.

**Consequence.** The lazy path stays, but not for the reason it was
proposed. Its value is `frame-only`: 5,150 M instructions and **zero
allocations** to frame every record while keeping each one's encoded bytes.
Skip cannot do that at all — it steps over the bytes and forgets them — so
a caller that needs to forward, hash, store or defer a record has no
cheaper option, and one that only needs to decode a few should keep using
skip.

The 2.7% and the 20 allocations are the price of holding the bytes, not a
regression to fix. What would be a mistake is presenting lazy values as a
speed win over skipping; they are not, and the benchmark is committed so
the claim cannot drift back.

## The adapter through the value model was 2-3x slower, and the gate said so

**Believed.** Reimplementing the shipped API as a projection over the full
codec would cost a little and buy a lot: one walk instead of two, so the
Skip/Unmarshal divergence could not come back.

**Actually.** It cost 2-3x. The first version decoded into `value.Value`
and projected afterwards, which is two allocations and two passes per item
to produce the same answer. `make bench-check` went red on the commit that
landed it:

    BenchmarkSweep/simdcbor/strings     1306 -> 3887 ns/op   +197.6%
    BenchmarkUnmarshal/simdcbor        671.2 -> 2039 ns/op   +203.8%
    BenchmarkSweep/simdcbor/numbers     2931 -> 7321 ns/op   +149.8%
    BenchmarkSweep/simdcbor/hugearray  81877 ->178730 ns/op  +118.3%

This is the gate that could not fail three days ago, catching the first
regression it was ever pointed at.

**How it surfaced.** Running it, on the commit that caused it.

**Source.** `adapter.go`'s `project`; `internal/codec/json.go`.

**Consequence.** The defect that started this refactor was two *walks*
disagreeing about well-formedness, not two *builders*. A second builder on
the same walk shares the head reader, the limits and the accept boundary,
so the disagreement still has nowhere to live, and the intermediate value
is not built at all. `DecodeJSON` is that builder.

Instructions retired against the pre-adapter implementation, two runs
each, spread under 0.5%:

    hugearray   6,228 M -> 4,416 M   -29.1%
    numbers       237.7 M -> 194.0 M -18.4%
    strings       100.1 M ->  87.2 M -12.9%
    deep           98.6 M -> 102.4 M  +3.9%

So the rewrite is faster than the code it replaced on every realistic
shape, and 3.9% slower on 40-level nesting, where per-item overhead
dominates and the walk now pays a call for the head plus the total-item
count. The wall-clock gate reported that row as +23.8%; instructions say
+3.9%, and the difference is the machine — which is why the instruction
count is the number recorded here.

**The `project` path stays** for callers of the value model, which is
where a full-fidelity value is actually wanted. It is simply not on the
adapter's path any more.
