# simdcbor

CBOR (RFC 8949) decoding built on [simd.go](https://github.com/sebishogun/simd),
the binary sibling of [simdjson](https://github.com/sebishogun/simdjson):
the same two-stage architecture — find where every item begins, then
build values from that — applied to a format whose framing is explicit
in the head bytes. Homogeneous runs (copying a byte string, validating a
text string's UTF-8) go through simd's kernels.

```go
v, n, err := simdcbor.Unmarshal(buf) // v is map[string]any / []any / ...
```

## Shape

Decoded values match `encoding/json`'s for the same logical data: maps
become `map[string]any`, arrays `[]any`, all numbers `float64`, so code
that consumed JSON into `any` consumes CBOR unchanged. Checked against
[fxamacker/cbor](https://github.com/fxamacker/cbor) for the bytes and
`encoding/json` for the shape, over a generated corpus, with truncation
and random-input fuzzing that must never panic.

## Beyond decode

- **`Skip(data)`** advances past an item without building it -- the
  filtering hot path. Decoding only 1 record in 100 of a stream and
  skipping the rest runs **8.4x** faster than decoding all of them
  (79 us vs 662 us), because Skip allocates nothing.
- **`Marshal(v)`** encodes the same shaped set, canonically (sorted map
  keys, shortest-form numbers), round-trip-checked against fxamacker.

## Speed

Decode into `any`, minimum of three on amd64/avx512, against
fxamacker/cbor:

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
until touched), measured and deferred -- see the note in decode.go.

Pure Go, no cgo.
