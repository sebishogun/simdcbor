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

## Required reading order

Before any task: `README.md` → `docs/architecture.md` → the four LLDs
(`docs/lld/data-model.md`, `docs/lld/decoder.md`, `docs/lld/encoder.md`,
`docs/lld/streaming-lazy-and-diagnostic.md`) → `docs/roadmap.md` →
`docs/verification.md` → `docs/wrong.md` → the design
(`docs/plans/2026-08-13-simdcbor-production-design.md`) → the plan
(`docs/plans/2026-08-13-simdcbor-production.md`). Each file assumes the
ones before it; skipping ahead is how edits drift from recorded
decisions.

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

## Task scope

Scope is **per task**, not a standing branch rule. The current branch
(`docs/v120-documentation`) is documentation-only for the current task —
only `.md` files change here; Go, tests, module files, the Makefile, and
assets are out of scope for *that* task. A code task executes on its own
branch (per the plan) and changes exactly what that task lists. No task
implies permission to push, tag, or release unless the task says so.

## Release and version status

Current status, factual: **no tagged or published release exists** (no
git tags, local or remote; pre-v1), and the shipped API is the
JSON-shaped subset described above. Release gates are the plan's Task 11
gate list and `docs/verification.md`'s rules — full suite, race, vet,
fuzz, cross-arch, benchmarks recorded — plus the owner's decision; docs
never declare a release.

**Roadmap-not-shipped:** `docs/roadmap.md`, the design, and the plan
describe the approved target, not shipped behavior. Nothing in them is a
claim about what the package does today; only the README's shipped
sections, `docs/architecture.md`'s shipped scope, `docs/verification.md`'s
pinned list, and the code and tests state what ships.

## Concurrency posture

The shipped API is three stateless package functions — `Unmarshal`,
`Marshal`, `Skip` — plus the two error values. No package-level mutable
state, no retained input: every function is safe for concurrent calls
from any number of goroutines. The caller owns the input slice (never
retained, safe to reuse after the call) and the results (freshly
allocated, safe to mutate).
