# Architecture

## What this package is

`simdcbor` decodes and encodes CBOR (RFC 8949) with one
head-argument-body walk in `internal/codec`. Different build steps serve
decode, strict skip, framing, streaming, and exact values without a second
grammar that can drift. CBOR makes framing explicit in each head byte. SIMD
enters in exactly one place: validating a text
string's UTF-8 via [simd](https://github.com/sebishogun/simd)'s
`ValidUTF8` kernel. Byte-string copying is a plain memmove, not a simd
kernel; no other simd kernel is used.

Decoded shapes match `encoding/json`'s for the same logical data: objects
become `map[string]any`, arrays `[]any`, numbers `float64`. A program that
consumed JSON into `any` consumes CBOR the same way.

## Shipped scope (current)

The root adapter exports six names in package `simdcbor`:

| symbol | behavior |
|---|---|
| `Unmarshal(data []byte) (any, int, error)` | decode the item at the front of `data`; return value, consumed bytes, error |
| `Marshal(v any) ([]byte, error)` | encode a Go value; direct append, no backpatching |
| `Skip(data []byte) (int, error)` | frame the item at the front; return its span, no allocation |
| `SkipStrict(data []byte) (int, error)` | frame with `Unmarshal`'s accept boundary and span |
| `ErrTruncated`, `ErrMalformed` | the only error values, package-level |

This is the **JSON-shaped subset**, not the full RFC 8949 codec:

- map keys are strings only; anything else is `ErrMalformed`;
- all numbers decode to `float64` (with the usual `2^53` integer precision
  limit, as in `encoding/json`);
- tags are consumed and discarded (their inner item is decoded);
- indefinite-length forms are rejected (`ErrMalformed`);
- duplicate map keys: last value wins (plain map assignment);
- the depth cap is 64, enforced by decrementing per container and tag;
- the marshal type set is `nil`, `bool`, `string`, `[]byte`, `float64`,
  `float32`, `int`, `int64`, `uint64`, `[]any`, `map[string]any` — note
  `uint` and the fixed-width ints other than `int64`/`uint64` are absent;
- `undefined` (`0xf7`) decodes to `nil`, same as `null`;
- `[]byte` marshals as a byte string, but unmarshals to `string` (bytes
  and text share a decode path);
- simple values: `false`/`true`/`null` decode (and `undefined` → `nil`),
  floats `25`/`26`/`27` decode to `float64`; simple values `0`–`19`
  (`0xe0`–`0xf3`) and the two-byte `0xf8` form are rejected by
  `Unmarshal` and `SkipStrict`, while framing-only `Skip` accepts them by
  design (see Skip below and `docs/wrong.md`);
- canonical behavior exists only to the extent the code and tests prove
  it: Go string keys sorted with `sort.Strings` (content-bytewise —
  **not** RFC 8949 §4.2.1 core deterministic, which compares the full
  encoded bytes, head included, so text keys of differing encoded
  lengths order by length through the head: `"z"` → `61 7a` before
  `"aa"` → `62 61 61`, while `sort.Strings` puts `"aa"` first; the two
  coincide where the encoded head cannot reverse the content comparison
  — e.g. equal-length text keys), shortest head forms,
  `float32` when it round-trips — **no** `float16` emission, **no** RFC
  8949 §4.2.3 length-first legacy key order. There is no full-RFC claim
  anywhere.

See `docs/verification.md` for what the tests actually pin.

## Decode pipeline

`Unmarshal` constructs an adapter-configured `internal/codec.Decoder` and
calls `DecodeJSON`. The decoder's one recursive walk reads the head, argument,
and body, applies adapter limits and UTF-8 checks, and builds the JSON-shaped
value directly. Building the exact `value.Value` first and projecting it later
was measured at 2-3x slower because it added allocations and a second pass, so
the shared grammar has multiple build steps instead.

The walk distinguishes truncation from malformed/out-of-model input before
the root adapter maps errors to `ErrTruncated` or `ErrMalformed`. Pre-flight
bounds cap every container allocation by remaining input; depth is capped at
64 in adapter mode. `Unmarshal` returns the decoder offset, so trailing data is
the caller's next item. SIMD enters only through `ValidUTF8`; byte strings use
runtime memmove.

## Skip

`Skip` is the same recursion with the build step removed: frame the head,
walk lengths. It allocates nothing.

There are two arms, and the split is a measurement rather than a taste.
`Skip` judges **framing** — the head is a head, the lengths fit, the
nesting closes — and therefore accepts a superset of what `Unmarshal`
does: an integer map key, a simple value outside the value model, a text
string that is not valid UTF-8. `SkipStrict` carries the **identical
boundary**: a `SkipStrict` that succeeds is an item `Unmarshal` would
decode, with the same span.

Making `Skip` itself carry the identical boundary costs +92.5%
instructions on the filter benchmark, three quarters of it validating the
contents of strings the caller is discarding. `docs/wrong.md` has the
numbers.

**History.** `Skip` used to claim the identical boundary and not have it.
It accepted every simple value the head allows — major 7, `ai` 0–19
(`0xe0`–`0xf3`) and the two-byte `0xf8` form — while `decode.go` rejects
those bytes, and the fuzz written to check that found three more classes:
byte-string map keys, tagged string keys, and invalid UTF-8. The test that
claimed to enforce parity could not see any of it: the generated corpus
never produced those values, and the random-bytes loop discarded both
errors. Recorded and closed in `docs/wrong.md`; the measured result is one
walk with framing-only `Skip` and adapter-boundary `SkipStrict`. Depth cap is
the same 64.

## Marshal

`Marshal` is `appendValue(nil, v)`, a direct append. CBOR's explicit
lengths make encoding the easy half: every item's length is known before
its bytes, so there is no backpatching. Map keys are sorted with
`sort.Strings` (bytewise), so the same map encodes to the same bytes — a
cache key or signature property. Heads are shortest-form. Floats use
`float32` (`0xfa`) when `float64(float32(f)) == f`, else `float64`
(`0xfb`); `NaN` never round-trips through `float32`, so it always encodes
as the double. Unsupported types return `ErrMalformed`.

## Error taxonomy (shipped)

| error | when |
|---|---|
| `ErrTruncated` | buffer ends inside a head, a string's length, or a container's declared items |
| `ErrMalformed` | reserved `ai`, indefinite forms, non-string map key, invalid UTF-8, unknown simple value, unsupported marshal type, depth exceeded |

`ErrMalformed` covers both "not CBOR" and "CBOR outside the subset". That
subset boundary is shared by `Unmarshal` and `SkipStrict`; plain `Skip`
documents its wider framing boundary.

## Full-codec architecture (implemented; adapter migration incomplete)

R1 implemented the architecture designed in
`docs/plans/2026-08-13-simdcbor-production-design.md`; the R2 ledger in the
production plan owns the remaining contracts and release evidence:

- **`simdcbor` (root)** — the JSON-shaped API. `Unmarshal`, `Skip`, and
  `SkipStrict` use the codec walk; `Marshal` remains the direct-append encoder
  until CBOR-V1-03 migrates it with a byte-identity snapshot;
- **`simdcbor/value`** — the exact value model: kinds for every CBOR major
  and simple type, integer/float bit fidelity, tags, arbitrary keys;
- **`simdcbor/diag`** — RFC 8949 §8 diagnostic notation;
- **`internal/codec`** — the streaming `Encoder`/`Decoder` core the other
  three packages share.

Ordering rationale (safety first, then data model, then streaming):
`docs/roadmap.md`.
