# Full RFC 8949 codec design

## Goal

Grow `simdcbor` from its shipped JSON-shaped subset into a full RFC 8949
codec — streaming decoder and encoder, exact value model, tags, arbitrary
map keys, duplicate policies, deterministic and canonical modes, lazy
values, diagnostic notation — while keeping the shipped API
(`Unmarshal`/`Marshal`/`Skip`, the JSON shapes, the two error values)
byte- and behavior-compatible as an explicit adapter.

## Audience

The primary reader is the engineer implementing `docs/plans/
2026-08-13-simdcbor-production.md`. Secondary readers are users deciding
what the package will offer, and reviewers checking the roadmap order.

## Scope boundary

Shipped today (the exact set, from the source):

- `Unmarshal(data []byte) (any, int, error)`, `Marshal(v any)
  ([]byte, error)`, `Skip(data []byte) (int, error)`, `ErrTruncated`,
  `ErrMalformed`;
- JSON-shaped decode: `map[string]any`, `[]any`, `float64`, string keys
  only, tags discarded, indefinite rejected, duplicate keys last-wins,
  depth cap 64, invalid UTF-8 and non-string keys `ErrMalformed`;
- simple values: `false`/`true`/`null` decode (`undefined`→`nil`), floats
  25–27 → `float64`; simple values 0–19 and the `0xf8` form are rejected
  by `Unmarshal` but **accepted by `Skip`** — a known parity bug, closed
  by Stage 0 of the plan (accept sets become policy-driven; the full
  simple-value model is the end state);
- marshal type set: `nil`, `bool`, `string`, `[]byte`, `float64`,
  `float32`, `int`, `int64`, `uint64`, `[]any`, `map[string]any` (no
  `uint`); sorted keys bytewise, shortest heads, `float32`-if-round-trips,
  never `float16`.

The target adds everything RFC 8949 requires, behind the packages and
modes below. **No shipped behavior changes; widening happens through new
APIs.**

## Package structure

```
simdcbor            root: the shipped API rebuilt as the JSON-shaped adapter
simdcbor/value      exact value model (kinds, fidelity, keys, tags, ordering)
simdcbor/diag       RFC 8949 §8 diagnostic notation (render + parse + error prefixes)
internal/codec      streaming Encoder/Decoder core; the only wire code
```

Dependency rule: `diag` → `internal/codec` → `value`; the root adapter
uses all three. `value` is pure data structures with **no stream or wire
I/O** — its only wire-adjacent output is canonical key encodings and
comparators, which the codec consumes — so it is the
first and cheapest phase to get exactly right, and `diag` never imports
the root, so notation cannot entangle with the adapter.

## Value model summary

See `docs/lld/data-model.md`. The load-bearing decisions:

- kinds for every major/simple/float type, wire bits preserved
  (`Float16`/`Float32`/`Float64` as distinct kinds, NaN payloads intact,
  integers exact — `NegInt` over the full CBOR range `-1`..`-2^64` via
  its `uint64` magnitude, since the `-2^64` endpoint is not an `int64`;
  `float64` conversion is the adapter's explicit lossy step, as in
  `encoding/json`);
- tags as `Tag{Number, Value}`, generic by default, interpret mode for
  the well-known set behind opt-in;
- maps as ordered `[]KeyValue`; keys comparable in Go directly
  (including the `Undefined` named constant), comparable-by-canonical-
  encoding for bytes/floats (NaN-safe), tag keys classifying by their
  tagged value (key-valid iff the value is, equal iff number and value
  are), and
  structural keys (arrays/maps as keys) rejected by default with an
  opt-in canonical-encoding hash; the full simple-value space — numeric
  values 0–19 (short form) and 32–255 (`0xf8` form), the named
  `false`/`true`/`null`/`undefined`, `break` reserved as the indefinite
  terminator only;
- duplicate policies `LastWins` (adapter) / `FirstWins` / `Error`;
- orderings `CoreDeterministic` (RFC 8949 §4.2.1, bytewise over the full
  encoded keys — head and body) and `LengthFirst` (RFC 8949 §4.2.3
  legacy, length-first then bytewise) — the shipped `sort.Strings`
  behavior becomes the adapter's fixed mode; that mode is a scoped
  content-bytewise claim over Go string keys, **not** the
  `CoreDeterministic` comparator, which differs wherever encoded lengths
  differ (`"z"` → `61 7a` sorts before `"aa"` → `62 61 61`);
- shortest forms: heads as today, floats extended to `float16` under the
  modes (the adapter stays at `float32`-then-`float64`, never `0xf9`).

## Streaming design

See `docs/lld/decoder.md`, `docs/lld/encoder.md`,
`docs/lld/streaming-lazy-and-diagnostic.md`. The core decisions:

- decoder: cursor over a buffer, head→argument→body state machine,
  definite and indefinite containers with break handling, pre-flight
  bounds before any allocation, `Limits` (depth, counts, sizes), owned
  buffer refill that distinguishes split-items from true `io.EOF`, and
  `Skip` implemented on the same machine so the accept/span parity
  invariant cannot drift;
- encoder: forward append only, no backpatching (CBOR lengths are known
  before bytes), container stack guarding `End`/`Start`/count misuse,
  indefinite emission via explicit `StartIndefinite*`/`End*`, tags via
  `WriteTag`;
- adapter: `Unmarshal`/`Marshal`/`Skip` map onto the core with the
  adapter's fixed mode and error mapping (`ErrDepth`/`ErrLimit`/
  `ErrUnsupportedKey` → `ErrMalformed`), so the shipped reject boundary
  is preserved exactly — and made consistent between `Unmarshal` and
  `Skip` by Stage 0.

## Security posture

No panic, hang, overflow, or resource exhaustion on any input, fuzzed or
not. Every allocation bounded by the remaining input **before** it
happens — the exact shape of the one bug this code has already had
(`docs/wrong.md`: presize overflow on malformed headers, caught by
fuzzing). Limits are enforced at the framing step, not at build.

## Roadmap mapping

Skip/Unmarshal consistency (Stage 0) → safety hardening → value model →
streaming decoder → streaming encoder → deterministic modes → tags →
lazy values → diagnostic notation, each phase ending green on the full
existing suite plus its new gates, and any measurement that argues
against a phase landing in `docs/wrong.md`. Full ordering and exit
criteria: `docs/roadmap.md`.

## Verification design

`docs/verification.md` gates: RFC 8949 appendix A vectors as committed
testdata, fxamacker interop both directions across modes (paired
correctly: `CoreDetEncOptions` for the bytewise mode, `CanonicalEncOptions`
for the legacy length-first mode), duplicate-policy and deterministic-mode
profile tests, fuzz under `-race` with a seeded corpus, race
and cross-arch (`arm64`, `386`, a big-endian build), the benchmark rules
(one process, shuffled, minimum of ≥3, 8.3% floor, `perf stat
instructions:u,cycles:u` for sub-floor claims, disassembly citations),
and the pipefail rule for every gated command.
