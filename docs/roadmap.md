# Roadmap: from JSON-shaped subset to full RFC 8949 codec

The shipped package is the JSON-shaped subset, deliberately bounded. This
roadmap is the approved path to the full RFC 8949 codec, in the order the
design mandates. The ordering rationale is short: **safety first, data
model second, then streaming, then the surface features** — every later
phase builds on the value model, and no phase ships on an unsafe base.

The full design lives in
`docs/plans/2026-08-13-simdcbor-production-design.md`; the executable
plan in `docs/plans/2026-08-13-simdcbor-production.md`; the LLDs in
`docs/lld/` pin the details this roadmap only summarizes.

## Phase 0 — Safety hardening (no behavior change)

Scope: fuzz the shipped decoder with a real corpus (not only random
bytes), assert the no-panic/no-hang property under `-race`, and pin the
existing limits (depth 64, pre-flight bounds, presize cap) with tests
that would have caught the presize bug the fuzzer found. Add
`Limits`-shaped internal checks only where they cannot change accept
behavior.

Exit: `go test -fuzz` runs clean for a bounded budget; every truncation of
every corpus item errors; race-clean.

## Phase 1 — Value model (`simdcbor/value`)

Scope: the exact value model from `docs/lld/data-model.md` — kinds for
every major/simple/float type with bit fidelity, tags as `Tag{Number,
Value}`, arbitrary keys with canonical-encoding equality, `[]KeyValue`
ordered maps, duplicate policies, deterministic/canonical ordering
comparators, shortest-form rules. Nothing here touches the wire: it is
pure data structures plus ordering/hashing logic, so it is the cheapest
phase to get exactly right.

Exit: value-model property tests (fidelity round-trips, key equality,
order comparators) against fxamacker as oracle where the model overlaps.

## Phase 2 — Streaming decoder (`internal/codec`)

Scope: the decoder LLD's state machine — definite and indefinite
containers, break handling, arbitrary keys, duplicate policies, tags
(keep/discard), limits, ownership/scratch, and `Skip` built on the same
machine with the parity invariant enforced by test. Reader-backed refill
and sequence `Next`/`More` from the streaming LLD.

Exit: full decode coverage over the RFC 8949 appendix A vectors; decoder
and fxamacker agree on every accepted input; the shipped subset decodes
identically through the adapter.

## Phase 3 — Streaming encoder (`internal/codec`)

Scope: the encoder LLD — direct-append streaming, container stack,
indefinite emission, tags, shortest heads, and the float rule extension
(`float16` in deterministic/canonical modes). The adapter's `Marshal`
must produce byte-identical output to the shipped encoder.

Exit: encode/decode round-trip on every RFC vector; adapter bytes equal
shipped bytes on the existing round-trip corpus.

## Phase 4 — Canonical and deterministic modes

Scope: the two orderings from the data model LLD (RFC 8949 §4.2.1
length-first; CTAP2 bytewise), applied to encode and to duplicate
detection, with explicit mode selection. The current "sorted bytewise,
no float16" behavior becomes the adapter's fixed mode, unchanged.

Exit: canonical/deterministic profile tests against fxamacker's modes;
the adapter mode's output is byte-identical to today's.

## Phase 5 — Tags

Scope: keep/interpret modes for well-known tags (date, epoch, bignum,
decimal fraction, URI, MIME, self-describe) behind opt-in, generic `Tag`
otherwise; encode side symmetric.

Exit: tag vectors round-trip; interpret mode matches fxamacker's
`TagNumber`/decode options on the documented tag set.

## Phase 6 — Lazy values

Scope: `RawMessage`/lazy `Value` as byte ranges with on-demand
materialization, buffer-backed only (pin-copy mode for reader-backed),
from `docs/lld/streaming-lazy-and-diagnostic.md`. The measurement that
deferred this (allocation-bound shapes: 1.35x–1.84x, narrowing where
boxing dominates; `Skip` covering the filter half at 8.4x) is recorded in
`docs/wrong.md`; the phase re-runs the sweep and records the delta there.

Exit: filter-then-read benchmark shows the allocation drop the design
predicts, measured and recorded — or the finding goes to `docs/wrong.md`.

## Phase 7 — Diagnostic notation (`simdcbor/diag`)

Scope: render and parse RFC 8949 §8 notation, including prefix rendering
for error messages, per the LLD.

Exit: notation round-trips through the value model; error prefixes render.

## Adapter rule (all phases)

The shipped API — `Unmarshal(data) (any, n, error)`, `Marshal(any)`,
`Skip(data)`, `ErrTruncated`/`ErrMalformed` — is the JSON-shaped adapter
over the new core from Phase 2 onward: same shapes, same errors, same
reject boundary, same bytes on encode. It never changes; widening happens
through new APIs. Each phase keeps the full existing test suite green.

## Cross-cutting gates

Every phase ends with: full `go test ./...` + `-race`, `go vet`, fuzz
budget, RFC vectors and fxamacker interop where the phase touches the
wire, benchmarks recorded per the verification rules, and any finding
that argued against a change or a measurement that deferred one appended
to `docs/wrong.md`.
