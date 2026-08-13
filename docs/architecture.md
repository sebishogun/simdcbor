# Architecture

## What this package is

`simdcbor` decodes and encodes CBOR (RFC 8949) on the architecture of
[simdjson](https://github.com/sebishogun/simdjson): a first pass over the
head bytes finds where every item begins, then a walk builds values from
that index instead of re-scanning. CBOR makes the framing explicit — every
item's head byte carries its major type and a length — so the first pass is
cheap arithmetic, and homogeneous runs within an item (copying a byte
string, validating a text string's UTF-8) go through
[simd](https://github.com/sebishogun/simd)'s kernels.

Decoded shapes match `encoding/json`'s for the same logical data: objects
become `map[string]any`, arrays `[]any`, numbers `float64`. A program that
consumed JSON into `any` consumes CBOR the same way.

## Shipped scope (current)

The shipped API is four symbols, all in package `simdcbor`:

| symbol | behavior |
|---|---|
| `Unmarshal(data []byte) (any, int, error)` | decode the item at the front of `data`; return value, consumed bytes, error |
| `Marshal(v any) ([]byte, error)` | encode a Go value; direct append, no backpatching |
| `Skip(data []byte) (int, error)` | frame the item at the front; return its span, no allocation |
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
- canonical behavior exists only to the extent the code and tests prove
  it: sorted keys (bytewise), shortest head forms, `float32` when it
  round-trips — **no** `float16` emission, **no** RFC 8949 §4.2.1
  length-then-bytewise key order. There is no full-RFC claim anywhere.

See `docs/verification.md` for what the tests actually pin.

## Decode pipeline

`Unmarshal` calls `decode(data, 0, 64)`, a recursive descent:

1. **Head check.** `i >= len(b)` is `ErrTruncated`; `depth < 0` is
   `ErrMalformed`. The major type is `b[i] >> 5`, the additional
   information `b[i] & 0x1f`.
2. **Argument.** `readArg` consumes the argument: inline (`ai < 24`),
   `uint8` (`24`), `uint16` (`25`), `uint32` (`26`), `uint64` (`27`), all
   big-endian. `ai` 28–30 are reserved and `31` (indefinite) is
   `ErrMalformed` — this is also how a `break` byte (`0xff`) anywhere is
   rejected.
3. **Dispatch by major type:**
   - unsigned / negative: `float64(arg)` / `-1 - float64(arg)`;
   - bytes / text: length checked against the buffer (`ErrTruncated` if it
     overruns), text additionally `simd.ValidUTF8` (`ErrMalformed` on bad
     UTF-8), result `string(s)`; `ai == 31` is `ErrMalformed` before the
     length is read;
   - array / map: a **pre-flight bound** rejects an impossible count
     before any allocation (`arg > len(b)-i`, one byte per item, two per
     pair), then the capacity is presized to `min(arg, 1024)` — the
     allocation is bounded by the remaining input, which is exactly the
     fix a fuzzer forced on the original unguarded presize;
   - tag: transparent — decode the tagged item (depth decremented, tag
     number discarded);
   - simple: `20`/`21` → `false`/`true`, `22`/`23` → `nil`, `25`/`26`/`27`
     → `float32`/`float64` via `math.Float32frombits` /
     `math.Float64frombits` (NaN/Inf payloads survive), `24` and anything
     else → `ErrMalformed`.
4. **Consumed count.** `Unmarshal` returns the index past the item;
   trailing data is the caller's business (frame the next item with
   `Skip`).

The scan is two-stage in the sense the package comment describes — the
head pass finds items, the walk builds values — but the shipped decoder is
one recursive pass over `[]byte`, not the simulated index of
`simdjson`. SIMD enters where runs are homogeneous: `ValidUTF8` on text,
and (via the string copy) the byte-string memmove.

## Skip

`Skip` is the same recursion with the build step removed: frame the head,
walk lengths. It allocates nothing. Its contract is stronger than a
separate implementation would be: **the accept/reject boundary is
identical to `Unmarshal`'s** — a `Skip` that succeeds is an item
`Unmarshal` would decode, with the same span. `TestSkipMatchesUnmarshal`
enforces this over a generated corpus. Depth cap is the same 64.

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

`ErrMalformed` covers both "not CBOR" and "CBOR outside the subset" —
deliberate: the subset has one reject boundary, shared by `Unmarshal` and
`Skip`.

## Target architecture (designed, not built)

The approved full RFC 8949 codec is designed in
`docs/plans/2026-08-13-simdcbor-production-design.md` and planned in
`docs/plans/2026-08-13-simdcbor-production.md`; the LLDs pin the details:

- **`simdcbor` (root)** — the current JSON-shaped API, rebuilt as an
  explicit adapter over the full codec, byte-for-byte compatible with the
  shipped behavior;
- **`simdcbor/value`** — the exact value model: kinds for every CBOR major
  and simple type, integer/float bit fidelity, tags, arbitrary keys;
- **`simdcbor/diag`** — RFC 8949 §8 diagnostic notation;
- **`internal/codec`** — the streaming `Encoder`/`Decoder` core the other
  three packages share.

Ordering rationale (safety first, then data model, then streaming):
`docs/roadmap.md`.
