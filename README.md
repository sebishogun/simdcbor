# simdcbor

CBOR (RFC 8949) decoding built on [simd.go](https://github.com/sebishogun/simd),
the binary sibling of [simdjson](https://github.com/sebishogun/simdjson):
the same two-stage architecture — find where every item begins, then
build values from that — applied to a format whose framing is explicit
in the head bytes. SIMD enters in exactly one place: validating a text
string's UTF-8 through simd's `ValidUTF8` kernel. Byte-string copying is
a plain memmove.

## API

The shipped API is small and exact:

```go
v, n, err := simdcbor.Unmarshal(data) // v is map[string]any / []any / ...
b, err := simdcbor.Marshal(v)         // the inverse, over the shaped set
n, err := simdcbor.Skip(data)         // frame an item without building it
```

Errors are the two package-level values `ErrTruncated` and `ErrMalformed`.

## Shape — and where the subset stops

Decoded values match `encoding/json`'s for the same logical data: maps
become `map[string]any`, arrays `[]any`, all numbers `float64` (with the
usual `2^53` integer precision limit, as in `encoding/json`), so code
that consumed JSON into `any` consumes CBOR unchanged. Checked against
[fxamacker/cbor](https://github.com/fxamacker/cbor) for the bytes and
`encoding/json` for the shape, over a generated corpus, with truncation
and random-input fuzzing that must never panic.

This is the **JSON-shaped subset**, not a full RFC 8949 codec. Exactly
what is and is not supported, from the source:

| supported | not yet |
|---|---|
| definite arrays, maps, byte/text strings | indefinite-length forms (rejected, `ErrMalformed`) |
| unsigned, negative, half/single/double floats → `float64` | integer-exact decode (all numbers are `float64`) |
| tags (consumed and **discarded**; inner item decoded) | tag numbers, tag values |
| string map keys | arbitrary keys (non-string key → `ErrMalformed`) |
| simple values `false`/`true`/`null`, `undefined`→`nil`, floats 25–27 | simple values 0–19 (`0xe0`–`0xf3`) and the `0xf8` form: `Unmarshal` rejects, **`Skip` accepts** — a known inconsistency (see Skip below) |
| duplicate keys: last value wins | duplicate policies (error / first-wins) |
| depth up to 64 (exceeding → `ErrMalformed`) | configurable limits |
| `Marshal` types: `nil`, `bool`, `string`, `[]byte`, `float64`, `float32`, `int`, `int64`, `uint64`, `[]any`, `map[string]any` | `uint` and other fixed-width ints, arbitrary types |

Notes the source pins: `undefined` (`0xf7`) decodes to `nil` like `null`;
`[]byte` marshals as a byte string but unmarshals to `string`; text
strings are UTF-8-validated (`ErrMalformed` on invalid).

**Canonical, scoped.** `Marshal` sorts Go string keys with
`sort.Strings` and writes shortest-form heads and `float32` when it
round-trips — so the same map encodes to the same bytes, the property a
cache key needs. This is **not** RFC 8949 §4.2.1 core deterministic:
that order compares the encoded bytes of each key, head included
(`"z"` → `61 7a` sorts before `"aa"` → `62 61 61`), while
`sort.Strings` compares content bytes (`"aa"` first). The two coincide
where the encoded head cannot reverse the content comparison — e.g.
equal-length text keys, whose head bytes are identical. The §4.2.3
length-first legacy ordering is not implemented either, and the
encoder never emits `float16`. The "canonical" claim is exactly what
the code and tests prove, no more.

There is **no full RFC 8949 conformance claim** in this repository. The
full codec — exact value model, streaming decoder/encoder, tags,
arbitrary keys, canonical/deterministic modes, lazy values, diagnostic
notation — is designed and planned: [design](docs/plans/2026-08-13-simdcbor-production-design.md),
[plan](docs/plans/2026-08-13-simdcbor-production.md), [roadmap](docs/roadmap.md).

## Beyond decode

- **`Skip(data)`** advances past an item without building it — the
  filtering hot path. Decoding only 1 record in 100 of a stream and
  skipping the rest runs **8.4x** faster than decoding all of them
  (79 us vs 662 us), because Skip allocates nothing. One caveat: `Skip`
  today accepts simple values (`0xe0`–`0xf3`, `0xf8` + byte) that
  `Unmarshal` rejects — a known inconsistency, recorded in
  [docs/wrong.md](docs/wrong.md) and scheduled as Stage 0 of the
  [production plan](docs/plans/2026-08-13-simdcbor-production.md).
- **`Marshal(v)`** encodes the same shaped set (see the scoped canonical
  note above), round-trip-checked against fxamacker.

## Speed

![benchmarks](docs/bench.svg)


Decode into `any`, minimum of three on amd64/avx512, against
fxamacker/cbor (reproduce with `make bench`):

| shape | simdcbor | fxamacker | |
|---|---|---|---|
| nested record | 637 ns | 986 | 1.55x |
| strings | 1,175 ns | 2,162 | 1.84x |
| numbers | 2,570 ns | 4,288 | 1.67x |
| 5,000-element array | 74.9 us | 101 | 1.35x |

The huge-array row is the narrowest because it is allocation-bound: a
`[]any` of five thousand boxed floats is mostly the boxing, which the
two-stage scan cannot remove. That is the same any-decode residual
simdjson documents; the next lever is lazy values (items as byte ranges
until touched), measured and deferred — see the note in decode.go and
the record in [docs/wrong.md](docs/wrong.md).

Pure Go, no cgo.

## Documentation

- [docs/architecture.md](docs/architecture.md) — shipped pipeline and target layout
- [docs/roadmap.md](docs/roadmap.md) — the approved path to the full codec
- [docs/verification.md](docs/verification.md) — what the tests and benchmarks prove
- [docs/wrong.md](docs/wrong.md) — measurements that argued against a change
- LLDs: [data model](docs/lld/data-model.md), [decoder](docs/lld/decoder.md),
  [encoder](docs/lld/encoder.md), [streaming/lazy/diagnostic](docs/lld/streaming-lazy-and-diagnostic.md)
- Plans: [design](docs/plans/2026-08-13-simdcbor-production-design.md),
  [implementation](docs/plans/2026-08-13-simdcbor-production.md)
