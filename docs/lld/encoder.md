# LLD: encoder

Low-level design for the implemented encoder in `internal/codec`. Root
`Marshal` remains the compatibility floor and direct-append path until
CBOR-V1-03 migrates it under a byte-identity snapshot. The codec encoder keeps
forward append and adds incremental containers, modes, tags, and arbitrary
keys.

## Position

An `Encoder` appends to a caller-supplied buffer; `Bytes` returns the completed
buffer. Everything is a forward append; nothing is backpatched or re-emitted.
The head
is written before the body, and for definite forms the length is known
before the head — the property that makes backpatching unnecessary in
CBOR at all, already exercised by `Marshal`.

## API shape (current)

```go
type Encoder struct { ... }

func NewEncoder(buf []byte) *Encoder
func (e *Encoder) SetMode(m Mode)
func (e *Encoder) Reset()
func (e *Encoder) WriteValue(v value.Value) error
func (e *Encoder) WriteUint(n uint64) error
func (e *Encoder) WriteInt(i int64) error
func (e *Encoder) WriteBytes(b []byte) error
func (e *Encoder) WriteText(s string) error
func (e *Encoder) WriteTag(number uint64) error
func (e *Encoder) WriteSimple(n uint8) error
func (e *Encoder) WriteFloat64(f float64) error
func (e *Encoder) StartArray(n uint64) error       // definite
func (e *Encoder) StartIndefiniteArray() error  // break-terminated
func (e *Encoder) EndArray() error
func (e *Encoder) StartMap(n uint64) error
func (e *Encoder) StartIndefiniteMap() error
func (e *Encoder) EndMap() error
func (e *Encoder) StartIndefiniteBytes() error
func (e *Encoder) StartIndefiniteText() error
func (e *Encoder) WriteChunk(b []byte) error
func (e *Encoder) EndBytes() error
func (e *Encoder) EndText() error
func (e *Encoder) Bytes() ([]byte, error)
```

CBOR-V1-03 moves `Marshal(v any) ([]byte, error)` onto this encoder with the
adapter's fixed mode (sorted bytewise keys, `float32`-then-`float64`, shortest
heads, **no** `float16`) and requires exact byte identity with the current
direct-append encoder.

## Heads and shortest forms

`appendHead` generalizes unchanged: `arg < 24` inline, then
`24`/`25`/`26`/`27` for 1/2/4/8 bytes. Under `CoreDeterministic`/
`LengthFirst` modes the float rule extends:

- adapter mode (shipped): `float32` iff `float64(float32(f)) == f`, else
  `float64`; never `float16`; `NaN` always `float64` (it never round-trips
  through `float32`);
- `CoreDeterministic`/`LengthFirst` modes: `float16` iff
  `float64(float16(f)) == f`, then `float32`, then `float64`. `float16`
  conversion is the RFC-prescribed shortest form (already implemented as
  `halfToFloat32bits`'s inverse in decode; the encoder needs the forward
  direction).

`NaN`/`Inf` round-trip bit-exactly in every mode (encode via
`math.Float*bits`), and no mode ever converts a NaN payload.

## Map key ordering

Mode names are the data-model LLD's, unambiguous:

- `CoreDeterministic` (RFC 8949 §4.2.1): **bytewise** lexicographic order
  over the full canonical wire encoding of each key — head and body
  (data-model LLD: shortest head, shortest float), so non-text keys order
  by their bytes;
- `LengthFirst` (RFC 8949 §4.2.3, legacy): **length first, then
  bytewise** — the RFC 7049 §3.9 "Canonical CBOR" rule;
- adapter mode: exactly `sort.Strings` on the text keys — a
  content-bytewise order over Go string keys, which is **not**
  `CoreDeterministic`'s encoded-byte order (that compares the full
  encoded key, head included, so text keys of differing encoded lengths
  order by length through the head: `"z"` → `61 7a` before `"aa"` →
  `62 61 61`, where `sort.Strings` puts `"aa"` first). The two coincide
  where the encoded head cannot reverse the content comparison — e.g.
  equal-length text keys. The adapter sorts before encoding, as
  today — its fixed mode is a scoped Go-string-key claim, not RFC core
  canonical.

Interop pairing against fxamacker/cbor v2.9.2 (verified against its
source, `encode.go`): `CoreDetEncOptions()` (`SortCoreDeterministic` =
`SortBytewiseLexical`) pairs with `CoreDeterministic`; `CanonicalEncOptions()`
(`SortCanonical` = `SortLengthFirst`) pairs with `LengthFirst`. The
"canonical" name in fxamacker is the legacy length-first ordering, not
bytewise — pairing `CanonicalEncOptions` with a bytewise mode would be
wrong. Caveat: fxamacker's deterministic options also force
`ShortestFloat16` and normalize NaN to `0xf97e00` and Inf to float16;
profile tests pin the key-sorting rule specifically and pin the float/NaN
differences by test — the oracle is the RFC, not fxamacker.

Sorting is stable and happens before any head is written, so a map is
always emitted as one contiguous run with no reordering after the fact.

## Tags

`WriteTag(n)` writes the major-6 head, then the encoder writes the tagged
value. Tags nest naturally (tag of a tag). The self-describe tag `55799`
is written only when explicitly requested (`WriteTag(55799)` or an
encoder option); the adapter never emits it, matching shipped bytes.

## Indefinite forms

`StartIndefiniteArray`/`StartIndefiniteMap` write the `ai 31` head;
`EndArray`/`EndMap` write `0xff`. Indefinite byte/text strings (`5f`/`7f`)
can be emitted chunk-wise — the indefinite head, definite chunks, then a
`0xff` — for data whose length is unknown ahead of time; the value
model's `Bytes`/`Text` are definite, so chunk emission is a streaming
form, and the adapter never emits it. A `break` is written only by
`End*`, so an indefinite container left unterminated is a caller bug
caught by the encoder's container stack (see below) — the encoder never
emits a stray `0xff`.

## Container stack

The encoder keeps a small stack of open containers (kind, definite or
indefinite, remaining count for definite). It catches, before any bytes
are written:

- `End` on an empty stack (`ErrContainer`-class error);
- `EndArray` when the open container is a map;
- more items than the definite count declared — the encoder refuses to
  overrun a definite container, since the head already promised `n`.

This is a caller-interface guard, not wire validation; the bytes produced
for well-formed call sequences are identical to what a stateless append
would write.

## Errors

| error | when |
|---|---|
| `ErrUnsupported` | a value kind the mode cannot encode (e.g. structural map key with structural keys disabled; unknown simple) |
| `ErrContainer` | mismatched or unterminated container calls |
| `ErrTruncated`-class (writer errors) | the underlying `io.Writer` fails; `Flush` propagates it |

The adapter maps `ErrUnsupported` to `ErrMalformed` so `Marshal` keeps
its shipped error surface (`ErrMalformed` for unsupported types).

## Streaming and ownership

`Bytes()` returns the accumulated output (borrowed); after `Flush` to a
writer the encoder's buffer is reused, so callers must copy before the
next write if they retained the slice. Sequence encoding is
straightforward concatenation — `WriteValue` calls in sequence produce
concatenated items, which the streaming decoder reads back with `Next`.
