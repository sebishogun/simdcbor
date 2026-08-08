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

## Speed

A realistic nested record decoded into `any`, minimum of three on
amd64/avx512:

| | ns/op | |
|---|---|---|
| **simdcbor** | **637** | |
| fxamacker/cbor | 986 | 1.55× |

Pure Go, no cgo.
