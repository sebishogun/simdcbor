# LLD: encoder

Low-level design for the streaming encoder in `internal/codec`. The
shipped `Marshal` is the compatibility floor and the seed: direct append,
lengths known before bytes, no backpatching. The new encoder keeps that
property and adds streaming, modes, tags, and arbitrary keys.

## Position

An `Encoder` writes into a caller-owned buffer or an `io.Writer`.
Everything is a forward append; nothing is backpatched or re-emitted,
which is what makes streaming (flush, bounded memory) possible. The head
is written before the body, and for definite forms the length is known
before the head — the property that makes backpatching unnecessary in
CBOR at all, already exercised by `Marshal`.

## API shape (target)

```go
type Encoder struct { ... }

func (e *Encoder) WriteValue(v value.Value) error
func (e *Encoder) StartArray(n int) error       // definite
func (e *Encoder) StartIndefiniteArray() error  // break-terminated
func (e *Encoder) EndArray() error
func (e *Encoder) StartMap(n int) error
func (e *Encoder) StartIndefiniteMap() error
func (e *Encoder) EndMap() error
func (e *Encoder) WriteTag(n uint64) error
func (e *Encoder) WriteHead(mt byte, arg uint64) error // low-level; exported for streaming containers
func (e *Encoder) Bytes() []byte
func (e *Encoder) Flush() error
```

The current `Marshal(v any) ([]byte, error)` becomes the adapter's
entry point: build a `value.Value` in JSON-shaped form, encode with the
adapter's fixed mode (sorted bytewise keys, `float32`-then-`float64`,
shortest heads, **no** `float16`), and return exactly the bytes the
shipped encoder would have produced.

## Heads and shortest forms

`appendHead` generalizes unchanged: `arg < 24` inline, then
`24`/`25`/`26`/`27` for 1/2/4/8 bytes. Under `Deterministic`/`Canonical`
modes the float rule extends:

- adapter mode (shipped): `float32` iff `float64(float32(f)) == f`, else
  `float64`; never `float16`; `NaN` always `float64` (it never round-trips
  through `float32`);
- deterministic/canonical modes: `float16` iff
  `float64(float16(f)) == f`, then `float32`, then `float64`. `float16`
  conversion is the RFC-prescribed shortest form (already implemented as
  `halfToFloat32bits`'s inverse in decode; the encoder needs the forward
  direction).

`NaN`/`Inf` round-trip bit-exactly in every mode (encode via
`math.Float*bits`), and no mode ever converts a NaN payload.

## Map key ordering

The shipped encoder sorts `map[string]any` keys with `sort.Strings`
(bytewise) — that is the `Canonical` ordering for the text-key subset.
The new encoder:

- `Canonical` mode: bytewise over the canonical wire encoding of each key
  (data-model LLD: shortest head, shortest float), so non-text keys order
  by their bytes, matching CTAP2;
- `Deterministic` mode: length-first, then bytewise (RFC 8949 §4.2.1);
- adapter mode: exactly `sort.Strings` on the text keys, whatever the
  underlying value layer does — the adapter sorts before encoding, as
  today.

Sorting is stable and happens before any head is written, so a map is
always emitted as one contiguous run with no reordering after the fact.

## Tags

`WriteTag(n)` writes the major-6 head, then the encoder writes the tagged
value. Tags nest naturally (tag of a tag). The self-describe tag `55799`
is written only when explicitly requested (`WriteTag(55799)` or an
encoder option); the adapter never emits it, matching shipped bytes.

## Indefinite forms

`StartIndefiniteArray`/`StartIndefiniteMap` write the `ai 31` head;
`EndArray`/`EndMap` write `0xff`. A `break` is written only by `End*`, so
an indefinite container left unterminated is a caller bug caught by the
encoder's container stack (see below) — the encoder never emits a
stray `0xff`.

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
