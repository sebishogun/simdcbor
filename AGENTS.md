# simdcbor: working notes for agents

## What this repository is

`simdcbor` is a CBOR (RFC 8949) codec built on the two-stage architecture
of [simd](https://github.com/sebishogun/simd): a first pass over head bytes
finds where every item begins, then a walk builds Go values from that index.
The only simd kernel in use is `ValidUTF8` for text strings; byte-string
copies are plain memmoves.

**Shipped scope today is the JSON-shaped subset**, not the full RFC:

- `Unmarshal(data []byte) (any, int, error)` — decodes the item at the front
  of `data` into `map[string]any` / `[]any` / `float64` shapes, returning the
  consumed byte count.
- `Marshal(v any) ([]byte, error)` — encodes the inverse of that set.
- `Skip(data []byte) (int, error)` — advances past an item without building
  it; allocation-free filtering.
- `ErrTruncated`, `ErrMalformed` — the only two error values.

Gaps (all deliberate, all documented): string map keys only, numbers as
`float64`, depth cap 64, indefinite forms rejected, tags discarded, duplicate
keys last-wins, a restricted marshal type set. One gap is a known bug, not a
decision: `Skip` accepts simple values `0`–`19` and the `0xf8` form that
`Unmarshal` rejects with `ErrMalformed` — do not claim Skip/Unmarshal parity
(see `docs/wrong.md`; scheduled as Stage 0 of the production plan). There is
**no full RFC 8949 claim** anywhere in this repository.

The approved target — a full RFC 8949 codec with streaming, tags, arbitrary
keys, canonical/deterministic modes, lazy values, and diagnostic notation —
is designed in `docs/plans/2026-08-13-simdcbor-production-design.md` and
planned in `docs/plans/2026-08-13-simdcbor-production.md`. `docs/roadmap.md`
orders the work; the LLDs in `docs/lld/` pin the data model, decoder,
encoder, and streaming/lazy/diagnostic designs.

## Layout

| path | what it is |
|---|---|
| `decode.go` | recursive-descent decoder, `readArg`, half-to-float |
| `encode.go` | direct-append encoder, sorted keys, shortest heads |
| `skip.go` | framing-only Skip, no allocation |
| `*_test.go` | oracle tests against fxamacker/cbor and encoding/json, sweep benches |
| `docs/` | architecture, LLDs, roadmap, verification, wrong, plans |
| `AGENTS.md`, `CLAUDE.md` | these working notes |

## Commands

```
make test        # go test ./...
make vet         # gofmt -l . ; go vet ./...
make bench       # one process, shuffled, count=6, minimum — the numbers the README quotes
make bench-check # tee'd to /tmp/simdcbor-bench.txt; pipes without pipefail, so NOT a reliable gate alone
```

`bench-check`'s pipe launders a failing `go test` into a green exit unless
run under `set -o pipefail`; a later code task fixes the Makefile
(roadmap Phase 1). Treat it as informational until then.

## Rules that cost real time when skipped

**Disassemble first, always.** Before proposing a cause for anything slow,
before writing a variant, build it and read the instructions:

```
go test -c -o /tmp/x.test .
go tool objdump -s 'simdcbor\.' /tmp/x.test | less
```

Register pressure, bounds-check elimination, inlining, branch layout — no
performance counter reports these.

**Benchmark noise floor is 8.3%** (code layout, per-build). Anything smaller
cannot be told from nothing by wall-clock. For sub-floor claims compare
`perf stat -e instructions:u,cycles:u` and read the disassembly. A/B builds
must be interleaved in one session and compared on the minimum, never across
sessions. Run the machine quiet.

**Never pipe a gate through `tail`** without `set -o pipefail`: the pipe
reports the last command's status and a red run launders into a green exit.

**The record.** `docs/wrong.md` holds measurements that argued against a
change, and deferred findings, *sourced* — an entry requires the measurement
or the commit that produced it. Nothing inferred from implementation shape.

**Security posture.** No panic, hang, overflow, or resource exhaustion on any
input, fuzzed or not. Every allocation must be bounded by the remaining input
before it happens. Fuzz before and after every decoder change.

## Scope discipline

This branch is documentation-only (`docs/v120-documentation`). Only `.md`
files may be modified here; no Go, no tests, no `go.mod`/`go.sum`, no
`Makefile`, no assets, no workflows, no releases, no push. Code tasks live in
`docs/plans/2026-08-13-simdcbor-production.md` and execute on a code branch.
