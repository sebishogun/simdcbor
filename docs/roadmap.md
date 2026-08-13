# Roadmap: from JSON-shaped subset to full RFC 8949 codec

The shipped package is the JSON-shaped subset, deliberately bounded. This
roadmap is the approved path to the full RFC 8949 codec, in the order the
design mandates. The ordering rationale is short: a small consistency fix
first (Stage 0), then **safety, then data model, then streaming, then the
surface features** — every later phase builds on the value model, and no
phase ships on an unsafe base.

The full design lives in
`docs/plans/2026-08-13-simdcbor-production-design.md`; the executable
plan in `docs/plans/2026-08-13-simdcbor-production.md`; the LLDs in
`docs/lld/` pin the details this roadmap only summarizes.

## Phase 0 — Skip/Unmarshal consistency (Stage 0)

Scope: close the shipped divergence where `Skip` accepts simple values
(`ai` 0–19, `0xf8` form) that `Unmarshal` rejects with `ErrMalformed`
(recorded in `docs/wrong.md`). Accept sets become **policy-driven** —
the shipped subset rejects those values consistently (both paths), and
the full simple-value model arrives with the value-model and decoder
phases, where both paths accept consistently. The direction is the full
model, not "add the same rejection to Skip and stop".

Exit: a head-byte enumeration test — `Skip` and `Unmarshal` agree on
accept/reject and span for every head byte `0x00`–`0xFF` (plus the
`0xf8`+byte form) — is green; no shipped `Unmarshal` accept/reject
decision changes. Corpus and assertion work (simple values in the
generator, both errors asserted in the random-input loop) is scheduled
in the decoder phase, not here.

## Phase 1 — Safety hardening (no behavior change)

Scope: fuzz the shipped decoder with a real corpus (not only random
bytes), assert the no-panic/no-hang property under `-race`, and pin the
existing limits (depth 64, pre-flight bounds, presize cap) with tests
that would have caught the presize bug the fuzzer found. Fix the
`bench-check` Makefile target — it pipes `go test` through `tee` without
`pipefail`, so a failure would launder green; the gate must fail
loudly. Add `Limits`-shaped internal checks only where they cannot
change accept behavior.

Exit: `go test -fuzz` runs clean for a bounded budget; every truncation of
every corpus item errors; race-clean; `bench-check` fails loudly on a
red run.

## Phase 2 — Value model (`simdcbor/value`)

Scope: the exact value model from `docs/lld/data-model.md` — kinds for
every major/simple/float type with bit fidelity (`NegInt` over the full
CBOR range `-1`..`-2^64` via its `uint64` magnitude, including the
`-2^64` endpoint that `int64` cannot hold; `Float16`/`Float32`/`Float64`
as distinct kinds), tags as `Tag{Number, Value}`, arbitrary keys with
canonical-encoding equality, the full simple-value space (values 0–19
short form, `0xf8` forms 32–255, named `false`/`true`/`null`/`undefined`,
`break` reserved), `[]KeyValue` ordered maps, duplicate policies, and the
two ordering comparators — `CoreDeterministic` (RFC 8949 §4.2.1,
bytewise) and `LengthFirst` (RFC 8949 §4.2.3 legacy, length-first then
bytewise) — plus shortest-form rules. Nothing here touches the wire: it
is pure data structures plus ordering/hashing logic, so it is the
cheapest phase to get exactly right.

Exit: value-model property tests (fidelity round-trips, key equality,
order comparators) against fxamacker as oracle where the model overlaps.

## Phase 3 — Streaming decoder (`internal/codec`)

Scope: the decoder LLD's state machine — definite and indefinite
containers, **indefinite byte/text strings** (chunked, concatenated, the
concatenation validated as UTF-8), break handling, **the full
simple-value model on decode**, arbitrary keys, duplicate policies, tags
(keep/discard), limits, ownership/scratch, and `Skip` built on the same
machine with the parity invariant enforced by test. The corpus and
assertion work deferred from Phase 0 lands here: simple values in the
generator, both errors asserted in the random-input loop, head-byte
enumeration kept green. Reader-backed refill and sequence `Next`/`More`
from the streaming LLD.

Exit: full decode coverage over the RFC 8949 appendix A vectors —
including `3b ffffffffffffffff` → `-18446744073709551616` (`-2^64`) and
the `5f`/`7f` indefinite string examples; decoder and fxamacker agree on
every accepted input; the shipped subset decodes identically through the
adapter.

## Phase 4 — Streaming encoder (`internal/codec`)

Scope: the encoder LLD — direct-append streaming, container stack,
indefinite emission, tags, shortest heads, and the float rule extension
(`float16` in the deterministic modes). The adapter's `Marshal` must
produce byte-identical output to the shipped encoder.

Exit: encode/decode round-trip on every RFC vector; adapter bytes equal
shipped bytes on the existing round-trip corpus.

## Phase 5 — Deterministic modes

Scope: the two orderings from the data model LLD, named unambiguously —
`CoreDeterministic` (RFC 8949 §4.2.1, bytewise) and `LengthFirst`
(RFC 8949 §4.2.3 legacy, length-first then bytewise) — applied to encode
and to duplicate detection, with explicit mode selection. The current
"bytewise sort, no float16" behavior becomes the adapter's fixed mode,
unchanged. Interop pairing is against the installed fxamacker v2.9.2:
`CoreDetEncOptions()` (bytewise `SortCoreDeterministic`) for
`CoreDeterministic`; `CanonicalEncOptions()` (length-first
`SortCanonical` — the "canonical" name there is the legacy ordering) for
`LengthFirst`.

Exit: canonical/deterministic profile tests against fxamacker's modes;
the adapter mode's output is byte-identical to today's.

## Phase 6 — Tags

Scope: keep/interpret modes for well-known tags (date, epoch, bignum,
decimal fraction, URI, MIME, self-describe) behind opt-in, generic `Tag`
otherwise; encode side symmetric.

Exit: tag vectors round-trip; interpret mode matches fxamacker's
`TagNumber`/decode options on the documented tag set.

## Phase 7 — Lazy values

Scope: `RawMessage`/lazy `Value` as byte ranges with on-demand
materialization, buffer-backed only (pin-copy mode for reader-backed),
from `docs/lld/streaming-lazy-and-diagnostic.md`. The measurement that
deferred this (allocation-bound shapes: 1.35x–1.84x, narrowing where
boxing dominates; `Skip` covering the filter half at 8.4x) is recorded in
`docs/wrong.md`; the phase re-runs the sweep and records the delta there.

Exit: filter-then-read benchmark shows the allocation drop the design
predicts, measured and recorded — or the finding goes to `docs/wrong.md`.

## Phase 8 — Diagnostic notation (`simdcbor/diag`)

Scope: render and parse RFC 8949 §8 notation — exact wire examples
including `42 01 02` for `h'0102'`, tag 0 of the 20-byte date-time
string (`c0 74` + payload), `(_ h'...')`/`(_ "…")` for indefinite
strings, `simple(n)` for the numeric simple space — plus prefix
rendering for error messages, per the LLD.

Exit: notation round-trips through the value model; error prefixes render.

## Adapter rule (all phases)

The shipped API — `Unmarshal(data) (any, n, error)`, `Marshal(any)`,
`Skip(data)`, `ErrTruncated`/`ErrMalformed` — is the JSON-shaped adapter
over the new core from Phase 3 onward: same shapes, same errors, same
reject boundary (consistent across `Unmarshal` and `Skip` from Phase 0
onward), same bytes on encode. It never changes; widening happens through
new APIs. Each phase keeps the full existing test suite green.

## Cross-cutting gates

Every phase ends with: full `go test ./...` + `-race`, `go vet`, fuzz
budget, RFC vectors and fxamacker interop where the phase touches the
wire, benchmarks recorded per the verification rules, and any finding
that argued against a change or a measurement that deferred one appended
to `docs/wrong.md`.
