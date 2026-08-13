# LLD: streaming, lazy values, and diagnostic notation

Three designs that extend the core decoder/encoder LLDs, one document
because they share the ownership story (who holds the bytes).

## Streaming

### Reader-backed decoding

A reader-backed `Decoder` owns an internal buffer and a refill path:

- the cursor reads items from the buffer;
- when the cursor reaches the buffer end **inside an item** (mid-head,
  mid-argument, mid-string, mid-container), the decoder refills and
  continues — the item was merely split across buffer boundaries;
- the refill returns `io.EOF` only when no bytes were read at all. If that
  happens at an item boundary, the sequence is cleanly exhausted; if it
  happens mid-item, it is `ErrTruncated`. **The streaming decoder never
  converts a truncated item into clean input end**, and never reports
  `ErrTruncated` for a split that a refill resolves;
- buffer size is configurable; the default is large enough that the common
  record-per-buffer pattern (one item per refill) needs at most one copy.

The framing logic is the decoder LLD's state machine; streaming adds only
the refill boundary around it. `Skip` on a reader-backed decoder behaves
identically (frame, walk, refill as needed).

### Sequence streaming

Concatenated CBOR items (a stream of records, the shipped
`Skip`-filtering pattern) are read with:

```go
for d.More() {
    v, err := d.Next()   // one item
}
```

`Next` never over-consumes: after returning item *k*, the cursor sits at
item *k+1*'s head. This is the same framing contract `Unmarshal`'s
consumed-count return gives the shipped API, made stateful. The adapter
keeps the stateless `Unmarshal(data) (any, n, error)` shape for callers
that hold a full buffer.

### Writer-backed encoding

`Encoder` with an `io.Writer`: every write appends to the internal buffer;
`Flush` ships it and reuses the buffer (callers who retained `Bytes()`
must copy — the encoder's slice is borrowed). Indefinite containers make
bounded-memory streaming possible for data whose length is unknown ahead
of time (`StartIndefiniteArray` … `EndArray`), at the cost of the
terminator byte.

## Lazy values

### The problem they solve (measured)

The shipped sweep numbers — strings 1.84x, numbers 1.67x, nested record
1.55x, 5,000-element array 1.35x against fxamacker — narrow exactly where
allocation dominates: a `[]any` of thousands of boxed scalars is mostly
the boxing, which no faster scan removes. That finding is recorded in
`docs/wrong.md` with the deferral: `Skip` already delivers the
allocation-free traversal for filtering (the filter benchmark runs 8.4x
faster than decode-all), which is the larger half of what lazy values
would buy.

### Design

A lazy value is a **byte range**: `RawMessage { start, end int }` over the
decoder's input (or a `Value` in a `Lazy` kind holding the range). Framing
finds the range; nothing is built until the caller materializes:

- `RawMessage.Bytes() []byte` — the borrowed subslice;
- `RawMessage.Unmarshal() (value.Value, error)` — decode on demand (this
  is where `fastjson`-style filter-then-read workloads spend nothing);
- `RawMessage.Skip() int` — span, for walking past unread items.

Ownership: the raw message is a **borrow** — the input (or the decoder's
buffer) must outlive it, and must not be mutated while it is live. A
reader-backed decoder that refills invalidates raw messages unless it
pin-copies the range; the default is: **lazy values are only available
from buffer-backed decoders**, and a reader-backed decoder materializes
lazily only with an explicit pinning mode (copy the range at frame time —
allocation returns, but only for the items the caller actually touches).

Interface consequence (already noted in decode.go): lazy values are
`Value` handles, not `any` — a real interface change, which is why the
shipped subset ships `Skip` instead and defers the rest with the
measurement recorded.

## Diagnostic notation

`simdcbor/diag` renders and parses RFC 8949 §8 diagnostic notation, the
human-readable CBOR:

| wire | diagnostic |
|---|---|
| `00` | `0` |
| `20` | `-1` |
| `40 01 02` | `h'0102'` |
| `61 61` | `"a"` (escaped per §8.2.1) |
| `f4` `f5` `f6` `f7` | `false` `true` `null` `undefined` |
| `f9 3c00` | `1.0` (shortest decimal that round-trips) |
| `e0` | `simple(0)` |
| `80` `81 00` | `[]` `[0]` |
| `9f 00 ff` | `[_ 0]` |
| `a0` `a1 61 61 01` | `{}` `{"a": 1}` |
| `bf 61 61 01 ff` | `{_ "a": 1}` |
| `c0 78 18 ...` | `0("2013-03-21T20:04:00Z")` (any tag: `N(...)`) |

Rules pinned in the LLD sense:

- floats render as the shortest decimal that round-trips to the same
  bits (the dtoa machinery simd already ships for the JSON side);
- `NaN`, `Infinity`, `-Infinity` render as `nan`, `infinity`,
  `-infinity`;
- indefinite containers render with `[_` / `{_`; the parser accepts both
  definite and indefinite and distinguishes them in the value model via
  the container's wire form (a decode-time flag, not a value kind);
- tags render `N(value)`, nested tags nest;
- the parser accepts the full notation, including `simple(n)` and
  `h'...'`/`b64'...'` forms, and round-trips through the value model.

**Error use**: `diag` is also the format for error reporting — a decode
error should be able to render the offending item's prefix in diagnostic
notation (`truncated at `["a", 1, …`), which is why the notation parser
must accept (and render) well-formed *prefixes* of malformed input, not
only complete items.

`diag` is a leaf: it depends on `internal/codec` and `simdcbor/value`
only, never on the root package, so the diagnostic and adapter layers
cannot entangle.
