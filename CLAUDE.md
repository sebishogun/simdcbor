# Working on simdcbor

## Core tenets: performance-aware programming

**These are the core tenets of this codebase. Read them before writing a line.**

The stance is Casey Muratori's: *performance-aware programming*. Not
"optimization" as a phase that happens after the code works — knowing roughly
what the machine will do with what you write, while you write it. The
alternative is not "clean code that gets optimized later"; it is code whose
shape forecloses the fast version, and the rewrite costs more than thinking for
five minutes did.

Two ideas underneath everything below:

- **Know the order of magnitude before you type.** How many times does this run
  — once, per request, per row, per element? What does one iteration touch?
  Nobody needs a cycle count; everybody needs to know whether they just wrote
  something that runs 200,000 times and allocates.
- **The machine is not an abstract machine.** It has caches, a prefetcher, wide
  registers, and many cores. Code that pretends otherwise leaves 10-100x on the
  floor, and no amount of later profiling recovers a layout decision.

**How the tenets relate.** They are not a list of independent good ideas. The
data-layout ones exist to make the bulk operation POSSIBLE:

    struct-of-arrays + grouped lifetimes + zero per-element allocation
        -> contiguous, uniformly-typed arrays
            -> the kernel can run at all
                -> SIMD, and the parallel shard boundaries come free

You cannot vectorize an array-of-structs: the lanes are not adjacent. You
cannot vectorize a slice that is really a graph of separately-allocated
objects. You cannot keep a kernel fed if every element costs an allocation.
So struct-of-arrays, arenas and lifetime grouping are not housekeeping to do
after the fast path works — they are the precondition for the fast path
existing, and the reason a layout decision made carelessly cannot be recovered
by profiling later.

Read the sections in that order, and design in that order.

### 1. Zero allocations wherever it is possible at all

Not "few" — zero, on any path that runs per element, per record, per row or per
request.

The checklist, in the order it usually pays:

- **Nothing per-element or per-record that can be per-batch.** A `map` built
  per record, a `fmt.Sprintf` per line, a `[]byte`->`string` per field: at 200k
  records those are 200k allocations and 200k pieces of GC work. Reach for a
  byte scan over the fixed shape instead of a reflective decode into a map.
- **Size every slice and map you can size.** `make([]T, 0, n)` when n is known
  or estimable. Growing from nil reallocates and copies the whole thing at
  every doubling.
- **Reuse the caller's buffer.** Append into a supplied `[]byte`, compact in
  place when the write cursor provably trails the read cursor, take a `dst`
  parameter rather than returning a fresh allocation.
- **Do not scan twice.** If a later stage already parses the data, do not
  validate it fully first — do the O(1) structural check and let the one place
  that parses report the rest.
- **Escape analysis is part of the design.** A pointer stored in an interface,
  a closure capturing a local, a returned slice of a local array: each forces a
  heap allocation. `go build -gcflags=-m` says which.
- **Prefer a wider type to a pointer chase.** An index into a slab beats a
  pointer when the slab is contiguous — it is smaller, it does not escape, and
  it keeps the array vectorizable.

Verify with `-benchmem`. `0 allocs/op` is a target you can actually hit on a
hot path, and worth stating in the doc comment when you hit it.

### 2. Think about the data, then the code

Muratori's central point, and the one most often skipped. The layout of the
data decides the speed; the instructions are usually a detail.

**Struct-of-arrays over array-of-structs** for anything scanned columnwise. A
filter that reads one field should stream that field's array, not stride
through whole records pulling in fifteen fields it does not want. This is the
single highest-leverage decision in a columnar store, and it is made when the
type is declared, not later.

**Group lifetimes; allocate them together.** Objects born together and dying
together belong in one allocation. A per-request arena — one buffer that
everything for that request is carved out of, released in one move when the
request ends — replaces thousands of individual allocations and frees with a
single pointer reset. It also gives locality for free: everything the request
touches is contiguous. Where the lifetime is per-batch, per-group or
per-connection instead, the same applies at that scope. The rule is that the
allocation boundary should match the lifetime boundary; when it does not, you
get either leaks or a per-object free.

**Use the whole cache line.** Touch it once and consume all of it. Block a pass
to fit L1/L2 rather than striding across a large array repeatedly. Keep hot
fields adjacent and cold fields elsewhere so a line carries only what the loop
reads. Watch for false sharing when threads write adjacent words.

Locality is a hypothesis to check with perf counters, not a rule to apply
blindly: windowing won in simdcsv and did nothing in simdjson.

### 3. Do the work in bulk — use SIMD

This family exists for it. Whole-slice work goes through the kernels, not a
hand-written scalar loop. Where no kernel exists for the shape, say so
explicitly rather than quietly writing the scalar loop and leaving it.

Check the dispatch actually reaches the kernel at runtime: every complex kernel
in `simd` was dead code from v1.14.0 to v1.20.0 because nothing walked the
tables the runtime indexes.

A per-element function call defeats vectorization outright — measured at 11
extra instructions per element, a 2.56x ratio. If the API shape forces one, the
API shape is the bug.

### 4. Don't do the work at all

The fastest code is the code that does not run. Prune before you decode: a
bloom filter that rejects a group, a time window that skips a block, a column
never materialized because nothing asked for it. simdlogs' rare-needle path
beats a full scan by rejecting groups without decoding them, not by decoding
faster.

Hoist invariants out of loops. Compute once what does not change. Do not scan
twice — if a later stage already parses the data, do the O(1) structural check
and let the one place that parses report the rest.

### 5. Multi-threaded where it is beneficial

And only there. Parallelism pays when the work per shard clears the
coordination cost; below that it is slower and less predictable.

Shard on a boundary the data already has (groups, blocks, row ranges), give
each worker its own output buffer, merge once. Never share a mutable buffer
between goroutines without saying so in the doc comment. `-race` is a gate.

### 6. `sync.Pool` is the last resort — and it has to be correct

Reach for it last. Most allocation wins are a size hint, an arena, or a
caller-supplied buffer: free at runtime, no correctness hazard. A pool costs
Get/Put, a miss allocates anyway, and it introduces a class of bug the others
cannot have.

When a pool IS the right answer, these are not optional:

- **The buffer must be fully overwritten before anything reads it.** A pooled
  buffer arrives holding a PREVIOUS request's data. If any path reads an
  element it did not write, that request's data is silently served to this one
  — a correctness bug, cross-request data leakage, not a performance one. Know
  the property holds and say WHY in the doc comment; do not assume it.
- **Prove it with a poisoning test.** Fill pooled buffers with a value that
  cannot occur, then assert the pooled result equals the unpooled result
  exactly. Write that test FIRST. This is the only thing that catches the bug,
  because the unpooled path zeroes and therefore hides it.
- **Ownership must be unambiguous.** A pooled buffer must not escape into a
  returned value, be captured by a goroutine that outlives the Put, or be
  aliased by a slice the caller keeps. Returning a slice of a pooled array is a
  use-after-free in all but name.
- **Put back exactly what you took**, reset to a known state, once. A double
  Put hands the same array to two callers at the same time.
- **Pool a pointer, not a slice.** A `[]T` placed in an `any` allocates on
  every Put, which is the cost the pool exists to remove.
- **Sizing is part of the contract.** A pool of mixed sizes either wastes the
  large buffers or reallocates on the small ones; decide which and say so.
- **Testing note:** `sync.Pool.Put` drops the value at random one time in four
  under `-race`, so any test asserting reuse across a single round trip is red
  a quarter of the time. Assert reuse within a few attempts, not on a
  particular one.

### Then measure

These tenets are where to start, not a substitute for the benchmark.
Fast-looking code that was never measured is a guess. The noise floor, the
interleaved A/B discipline, and "disassemble before you theorise" apply to code
written this way exactly as they apply to a tuning change — and a claim with no
number behind it does not go in a doc.

## Know the shipped scope before touching anything

The root package ships the **JSON-shaped adapter**: `Unmarshal` projects to
`map[string]any` / `[]any` / `float64`, with string keys, discarded tags,
rejected indefinite forms, depth cap 64, and last-wins duplicates. `Skip`
checks framing and deliberately accepts a superset; `SkipStrict` has
`Unmarshal`'s boundary and span. The full codec packages (`value`,
`internal/codec`, `diag`) are implemented, but no tagged release or
root-package full-RFC claim exists. Do not let a code change silently widen
or narrow the adapter subset without updating README, the tests, and
`docs/wrong.md` where measurement decides the boundary. The measured
`Skip`/`SkipStrict` split is closed in `docs/wrong.md`.

## Required reading order

Before any task, read **the core tenets above** first — performance-aware programming is the standard every change in this repository is held to.

Before any task: `README.md` → `docs/architecture.md` → the four LLDs
(`docs/lld/data-model.md`, `docs/lld/decoder.md`, `docs/lld/encoder.md`,
`docs/lld/streaming-lazy-and-diagnostic.md`) → `docs/roadmap.md` →
`docs/verification.md` → `docs/wrong.md` → the design
(`docs/plans/2026-08-13-simdcbor-production-design.md`) → the plan
(`docs/plans/2026-08-13-simdcbor-production.md`). Each file assumes the
ones before it.

## Disassemble first, always

Before proposing a cause for anything slow, before writing a variant,
before reading a profile delta — **build it and read the instructions**.

```
go test -c -o /tmp/x.test .
go tool objdump -s 'simdcbor\.' /tmp/x.test | less
```

Go compiles in seconds; every guess that skips the disassembly costs a
build-measure-revert cycle. The disassembly shows register pressure (a
spilled loop counter cost 18% once in the parent library), bounds-check
elimination, whether a call inlined, and which branch is fallthrough.

## Benchmarks

The code-layout noise floor is **8.3%**; wall-clock cannot tell anything
smaller from nothing, and more samples do not help — layout noise is
per-build, not per-run. For changes expected to be worth less than that:

- compare **instructions retired** and **cycles** with
  `perf stat -e instructions:u,cycles:u`, which are layout-independent;
- read the disassembly, the only thing that explains *why*.

A/B builds must be **interleaved in one session**, compared on the
minimum, never across sessions. Machine quiet, load average under 1.

**Never pipe a gate through `tail`** (or anything else) without
`pipefail`: the pipe reports the last command's status and a red run
launders into a green exit. Run gates bare, or `set -o pipefail` first.
`make bench-check` now uses a pipe-safe script. Its committed baseline was
captured at load 4.82, so regenerate it below load 1 before using its
wall-clock comparison as publishable evidence.

## The record

`docs/wrong.md` holds measurements and findings that argued against a
change, or work deferred on measurement — **sourced**: an entry needs the
measurement or the commit that produced it. A finding that cost a
measurement belongs there whether or not any code changed; the entry is
the deliverable. Nothing inferred from implementation shape alone.

## Security

No panic, no hang, no overflow, no resource exhaustion on any input.
Every allocation is bounded by the remaining input before it happens —
that is the exact shape of the one bug this code has already had (an
unguarded presize on a malformed length header, caught by fuzzing).
Fuzz before and after any decoder change.

## Task scope and releases

Scope is **per task**, not a standing branch rule. A documentation-only task
changes only `.md` files. A code task changes exactly what its ledger row and
approved plan list; no task implies
permission to push, tag, or release unless it says so.

Current status, factual: **no tagged or published release** (no tags; pre-v1).
The root API is the JSON-shaped adapter, while the full codec packages are
implemented. Release gates are the R2 ledger and `docs/verification.md` — full
suite, race, vet, fuzz, cross-arch, bench records — plus the owner's decision;
docs never declare a release. R1 phases 0-8 and plan Tasks 0-11 are historical;
the roadmap's production-readiness section and R2 ledger own what remains.

Before committing: `make test`, `make vet`, `go test -race ./...`, and
check that every internal link in changed docs resolves.

## Production task management

Production work toward v1 lives in the Production readiness ledger at
the bottom of `docs/plans/2026-08-13-simdcbor-production.md`. Binding
rules:

- **Local authority.** The ledger is the tracker; nothing outside the
  repository closes, rejects, renumbers, or reorders a row.
- **Stable IDs.** `CBOR-V1-NN` is a stable reference key, not
  an ordering or priority; never renumber, never infer rank from an ID.
- **One ID per item.** Issued once, names one item; never reused.
- **One task at a time.** A session touching implementation work names its
  ledger ID first; without one it touches no implementation files.
- **Noncanonical family index.** The index at
  `GO_SIMD/docs/plans/2026-08-24-simd-family-production-readiness.md` is a link
  collection and never overrides local truth or duplicates task status.
- **States and rejection.** A row is in exactly one of `open`,
  `staged`, `in-progress`, `blocked`, `evidence-complete`, `shipped`,
  `rejected`. Every transition is an edit in the ledger with recorded
  evidence; `shipped` creates or updates CHANGELOG.md, and `rejected`
  records its measurement in `docs/wrong.md` with the reopen condition
  there. `rejected` is terminal without a documented reopen condition;
  rejection is never a silent removal.
- **Timed bare gates.** Explicit timeout on every gate; bare, never
  piped without `pipefail`.

RFC 8949 is the oracle; fxamacker/cbor, QCBOR, TinyCBOR, serde_cbor, and
ciborium are peers compared only where a row says so explicitly, and a
difference from a library is resolved by the RFC's text. Evidence is
concrete - vectors, indefinite forms, round-trip shapes, sourced
measurements; no invented figures.

## Concurrency posture

`Unmarshal`, `Marshal`, `Skip`, and `SkipStrict` are stateless package functions: no
package-level mutable state, no retained input — safe for concurrent
calls from any number of goroutines. The caller owns the input slice
(never retained, safe to reuse after the call) and the results (freshly
allocated, safe to mutate).
