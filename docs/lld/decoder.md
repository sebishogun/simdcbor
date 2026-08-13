# LLD: decoder

Low-level design for the streaming decoder in `internal/codec`, the core
of the approved full RFC 8949 codec. The shipped `Unmarshal`/`Skip`
behavior is the compatibility floor: the new decoder must accept and
reject exactly what the shipped subset accepts and rejects when running
in adapter (JSON-shaped) mode, and span items identically.

## Position

The decoder is a **cursor over a buffer** plus an optional reader-backed
refill for streaming. One `Decoder` owns one input position; repeated
`Next`/`Unmarshal` calls advance it. There is no random access and no
separate index pass — framing is computed on the fly from head bytes, and
the two-stage scan of the package comment survives as "frame the head,
then consume the body" within each item.

## The state machine

Each item is read as: **head byte → argument → body**.

1. **Head.** At the cursor, no byte available is `ErrTruncated` (or
   `io.EOF`-driven refill in streaming mode). Major type = `b[i] >> 5`,
   `ai = b[i] & 0x1f`.
2. **Argument.** `ai < 24` inline; `24`/`25`/`26`/`27` read 1/2/4/8 bytes
   big-endian, truncated mid-argument is `ErrTruncated`. `ai` 28–30
   reserved, `ai 31` is the **break marker** — legal only as a terminator
   inside an indefinite container; anywhere else `ErrMalformed` (this is
   the shipped rule, preserved).
3. **Body by major type:**
   - `0`/`1` — value is the argument; exact (`uint64`, and `NegInt` as its
     `uint64` magnitude `n` with mathematical value `-1-n`, over the full
     `-1`..`-2^64` range), never `float64` at this layer;
   - `2`/`3` — definite: length `L`, bounds-checked against the remaining
     input (`ErrTruncated` if it overruns), body copied, text validated
     `simd.ValidUTF8` (`ErrMalformed`). Indefinite (`ai 31`): a sequence
     of zero or more **definite** chunks of the same major type,
     concatenated in order, terminated by `break`; bytes chunks
     concatenate raw, text chunks concatenate and the **result** is
     validated as UTF-8 (each chunk is itself a text string, so the
     concatenation is what the wire means). The shipped subset rejects
     indefinite byte/text (`ai 31` → `ErrMalformed`); the full decoder
     accepts them;
   - `4`/`5` — definite: `n` children (maps: `2n`); indefinite: children
     until `break`. The indefinite child loop is the one real state
     machine: read child heads, on `break` (major 7, `ai 31`) terminate
     the container, on anything else recurse into that item's
     head→argument→body. A `break` outside an indefinite container, or an
     indefinite container that hits input end, is `ErrMalformed` /
     `ErrTruncated` respectively;
   - `6` — tag number; the tagged item is the next item at the new depth;
     tags may nest. Mode decides `Discard` (adapter) / `Keep` (`Tag`);
   - `7` — simple values and floats: `ai` 0–19 → numeric `Simple`
     values `0`–`19`; `20`/`21` `false`/`true`, `22` `null`, `23`
     `undefined` (the shipped decoder maps both 22 and 23 to `nil`; the
     value model separates them); `24` + byte → `Simple` for values
     `32`–`255` (values below 32 in this form are malformed); `25`/`26`/
     `27` half/single/double; `31` break (terminator only — never a
     standalone item).

## Depth and limits

- **Depth**: default 64 (shipped), configurable via `Limits`. Decrement
  per container and per tag; `depth < 0` is `ErrDepth` (the shipped
  decoder reports `ErrMalformed` for depth — the adapter maps `ErrDepth`
  to `ErrMalformed` to preserve the reject boundary).
- **Pre-flight bounds**: every container count and string length is
  checked against the remaining input **before** any allocation or
  recursion (`n > remaining` → `ErrTruncated`), and presizing is capped
  (`min(n, cap)`) — the exact shape of the shipped fix that fuzzing
  forced, generalized to all limits.
- **Limits** (per-decoder struct, defaults chosen so no shipped behavior
  changes): max depth, max item count per container, max string/bytes
  length, max total decoded bytes, max tag nesting. Exceeding a limit is
  `ErrLimit` (or `ErrDepth` for depth); the adapter maps these to
  `ErrMalformed` so the shipped reject boundary holds.

## Errors

| error | shipped name | when |
|---|---|---|
| `ErrTruncated` | `ErrTruncated` | input ends inside a head, argument, string, or declared container items |
| `ErrMalformed` | `ErrMalformed` | reserved `ai`, illegal `break`, indefinite in definite-only mode, invalid UTF-8, unknown simple, non-string key in adapter mode |
| `ErrDepth` | (mapped to `ErrMalformed`) | nesting cap exceeded |
| `ErrLimit` | (mapped to `ErrMalformed`) | a configured size/count cap exceeded |
| `ErrDuplicateKey` | n/a (adapter: last-wins) | duplicate key under `Error` policy |
| `ErrUnsupportedKey` | (mapped to `ErrMalformed`) | non-string key in adapter mode; structural key when disabled |

The invariant that matters: **in adapter mode, the error set and the
accept/reject boundary are identical to the shipped `Unmarshal`**, and
`Skip` must span exactly what `Unmarshal` consumes. The shipped
`TestSkipMatchesUnmarshal` cannot enforce this on its own (see Skip
parity below); the new decoder's suite enforces it by construction — the
head-byte enumeration test, the extended corpus, and both errors
asserted in the random-input loop (plan Tasks 1 and 4).

## Skip parity

`Skip` is the decoder with the build step removed: frame heads, walk
lengths, allocate nothing. It is not a separate grammar — the new decoder
implements `skip` over the same state machine, so the two **cannot
drift**: accept sets and spans come from one code path.

The shipped package is the standing counterexample. `skip.go` accepts
every simple value the head allows (major 7, `ai` 0–19 and the `0xf8`
form) while `decode.go` rejects them with `ErrMalformed` — a `Skip` that
succeeds on bytes `Unmarshal` refuses. The shipped test cannot see it:
the generated corpus never produces those simple values, and the
random-bytes loop discards both errors (`_ = ue; _ = se`), so it asserts
nothing about agreement. The divergence is recorded in `docs/wrong.md`;
the plan's Stage 0 aligns the accept sets (policy-driven, so the full
simple-value model is the end state rather than a silent divergence), and
the decoder task adds the corpus and assertion work: head-byte
enumeration, simple values in the generator, both errors asserted.

## Ownership and scratch

- **Borrowed by default**: `RawMessage` returns a subslice of the input;
  the caller must not mutate the input while the raw message is live.
- **Copied on demand**: string and bytes values copy (text must be
  validated first; `[]byte` in the value model is the decoder's copy when
  `CopyBytes` is set, else borrowed — default copied, matching shipped
  `string` semantics).
- **Scratch**: one reusable buffer per decoder for head/argument assembly
  and streaming refill; no per-item allocation in framing paths; value
  building allocates only what the value itself needs.

## Streaming

On a reader-backed decoder: refill the buffer when the cursor reaches its
end **mid-item** — that is a refill, not `ErrTruncated`; only a refill
that returns `io.EOF` at an item boundary is the end of the sequence, and
`io.EOF` in the middle of an item is `ErrTruncated` (the streaming
decoder never hides a truncated item as clean input end). Sequence
streaming (`Next` returns items one at a time, `More` reports another
item) is the documented way to consume concatenated CBOR; the shipped
`Unmarshal`-then-`Skip`-the-rest pattern is its adapter-mode equivalent.
See the streaming LLD for buffering details.
