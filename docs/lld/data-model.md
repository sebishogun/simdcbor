# LLD: value model

Low-level design for `simdcbor/value`, the exact value model of the
approved full RFC 8949 codec. This document describes the **target**
model; the shipped `simdcbor` package decodes into the JSON-shaped subset
(`map[string]any` / `[]any` / `float64`) and is unaffected by this design
except as the adapter that converts between the two.

## Kinds

Every CBOR item decodes to exactly one `Value` with a kind tag. The kind
set is the RFC 8949 type lattice, with wire fidelity preserved:

| kind | wire | Go representation |
|---|---|---|
| `Uint` | major 0 | `uint64` |
| `NegInt` | major 1 | `int64` (value `-1-n`) |
| `Bytes` | major 2 | `[]byte` |
| `Text` | major 3 | `string` |
| `Array` | major 4 | `[]Value` |
| `Map` | major 5 | `[]KeyValue` (ordered) |
| `Tag` | major 6 | `Tag{Number uint64, Value Value}` |
| `Simple` | major 7, `ai` < 24 | `Simple(uint8)` — `false`, `true`, `null`, `undefined` are the four named constants, all other `Simple` values carry their number |
| `Float16` | `0xf9` | `Float16` as raw `uint16` bits |
| `Float32` | `0xfa` | raw `uint32` bits |
| `Float64` | `0xfb` | `float64` (bits preserved through `math.Float64bits`) |

**Fidelity rules:**

- integers are **never** widened or narrowed by the value layer: `Uint`
  is exact to `2^64-1`, `NegInt` exact to `-2^63`. Conversion to `float64`
  is an explicit, lossy, documented operation (the adapter's job), exactly
  as `encoding/json` treats numbers;
- `Float16`/`Float32`/`Float64` are distinct kinds that preserve the wire
  bits: NaN payloads, signaling bits, and `-0.0` survive decode and
  round-trip. A `0xf9` item is a `Float16`, not "a float64 that happens to
  be small";
- `Bytes` is a byte string; the decoder keeps the bytes as read. Text is
  validated UTF-8 (existing `simd.ValidUTF8` path); invalid UTF-8 is a
  decode error, never a silent replacement.

## Tags

Tags are first-class: `Tag{Number, Value}` wraps the tagged item, nested
tags nest. The value layer stores what the wire said; it does not
interpret. A small set of well-known tags (`0` date-time text, `1` epoch
float, `2`/`3` bignum, `4`/`5` decimal fraction, `32`/`33`/`34` URI,
`36` MIME, `55799` self-describe) may gain native conversions in a later
phase, behind explicit opt-in; the generic `Tag` form is the default for
every tag number. Tag validation is a **mode**: `Discard` (adapter
behavior — current `Unmarshal` drops tag numbers entirely), `Keep`
(generic `Tag`), and later `Interpret` (native types for the known set).

## Map keys

CBOR allows any item as a map key. The value model must therefore hold
keys that Go cannot put in a `map`:

- **comparable in Go, direct:** `Uint`, `NegInt`, `Bool`, `Null`, `Text`,
  `Simple` — usable as Go map keys as-is. (Floats are comparable in Go
  but are excluded from the direct path: `NaN` is not equal to itself, so
  a `NaN` key could never be looked up; `Float16`/`Float32`/`Float64` map
  to a canonical key form, see below.)
- **comparable via a canonical encoding:** `Bytes` keys and float keys
  use the canonical wire encoding of the key as a `string`/`[32]byte`
  hash — byte-exact, unambiguous, and stable regardless of which float
  width the wire used. Two keys are equal iff their canonical encodings
  are equal.
- **non-comparable in Go:** `Array` and `Map` keys (CBOR permits them).
  Policy: **reject by default** with `ErrUnsupportedKey` — matching the
  shipped subset's string-keys-only boundary in spirit — with an opt-in
  `StructuralKeys` mode that hashes the key by its canonical encoding.
  Structural hashing is O(key size) per probe and documented as such;
  there is no attempt to make it cheap.

`Map` itself is stored as an **ordered `[]KeyValue`**, not a Go `map`,
because decode order, duplicate policy, and deterministic re-encoding all
need order. Lookup is a scan over the slice (or a hash index built at
decode time when the map is large and the mode demands it — a later
optimization, not in v1 of the value layer).

## Duplicate policies

A `DuplicatePolicy` applies at map build:

| policy | behavior |
|---|---|
| `LastWins` | later value replaces earlier — current adapter behavior |
| `FirstWins` | first value stands; later duplicates dropped |
| `Error` | `ErrDuplicateKey` on the second occurrence of a canonical-equal key |

Policy is a decode-time decision, per `Decoder` configuration, not a value
property. Canonical-equal means canonical-encoding-equal (see keys above):
`0xf9 3c00` and `0xfa 3f800000` are the same key `1.0` under every
policy.

## Deterministic and canonical ordering

Two orderings, one comparator, two modes. Keys are compared by their
**canonical wire encodings** (shortest head, shortest float — never
layout-dependent):

- `Deterministic` (RFC 8949 §4.2.1 core deterministic): sort by **encoded
  length first**, then bytewise. This is what the RFC calls
  deterministic; it is length-sensitive by construction.
- `Canonical` (CTAP2-style, the common "canonical CBOR"): sort **bytewise
  only**.

The current `Marshal` sorts Go string keys bytewise with `sort.Strings` —
that matches `Canonical` for the text-key subset and does **not** match
`Deterministic`. The shipped claim ("same map, same bytes") survives
either; the RFC's length-first ordering does not exist yet and will
arrive with the modes. No ordering applies inside `Array` (order is
data).

## Shortest forms

`appendHead` already writes the shortest head for an argument (`ai < 24`
inline, then `24`/`25`/`26`/`27`). The value layer's encoder extends
shortest-form to numbers under the deterministic/canonical modes:

- integers: shortest head for the magnitude (`0`–`23` inline, `uint8`,
  `uint16`, `uint32`, `uint64` / the negint mirror);
- floats: prefer `Float16` when `float64(float16(f)) == f`, then
  `Float32`, then `Float64` — the shipped encoder stops at `Float32` and
  never emits `0xf9`, so shortest-form floats change with the modes;
- bytes/text heads: shortest for the length, as today.

Shortest form is a wire concern; the value model never collapses `Float32`
into `Float16` (that would lose the `Float32` kind's fidelity).

## Errors in the value layer

The value layer itself validates structure (kinds, tags, nesting depth);
wire errors belong to the decoder LLD. Its error set is
`ErrUnsupportedKey`, `ErrDuplicateKey`, and `ErrDepth` — the first two new,
`ErrDepth` shared with the decoder's limits.
