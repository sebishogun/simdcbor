# Working on simdcbor

## Know the shipped scope before touching anything

This package ships the **JSON-shaped subset** of CBOR, not the full RFC:
`Unmarshal` -> `map[string]any` / `[]any` / `float64`, string keys only,
tags discarded, indefinite rejected, depth cap 64, duplicate keys
last-wins. No full RFC 8949 claim is made anywhere. The full codec is a
designed, planned, not-yet-built target (`docs/plans/`, `docs/roadmap.md`,
`docs/lld/`). Do not describe the package as RFC-complete; do not let a
code change silently widen or narrow the subset without updating the
README, `docs/wrong.md` where it argues against a change, and the tests
that pin the subset.

One shipped inconsistency is a known bug, not a decision: `Skip` accepts
simple values `0`–`19` and the `0xf8` form that `Unmarshal` rejects with
`ErrMalformed`. Never claim Skip/Unmarshal parity — the shipped
`TestSkipMatchesUnmarshal` cannot see it (the corpus never generates
those simple values; the random-bytes loop discards both errors). The
divergence is recorded in `docs/wrong.md` and scheduled as Stage 0 of the
production plan.

## Required reading order

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
`make bench-check` is itself such a pipe (through `tee`, no `pipefail`) —
not a reliable gate on its own; a later code task fixes the Makefile.

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

Scope is **per task**, not a standing branch rule. This branch
(`docs/v120-documentation`) is documentation-only for the current task —
only `.md` files change here. A code task runs on its own branch (per the
plan) and changes exactly what that task lists; no task implies
permission to push, tag, or release unless it says so.

Current status, factual: **no tagged or published release** (no git
tags, local or remote; pre-v1); the shipped API is the JSON-shaped
subset. Release gates are the plan's Task 11 gate list and
`docs/verification.md`'s rules — full suite, race, vet, fuzz, cross-arch,
bench records — plus the owner's decision; docs never declare a release.
The roadmap, design, and plan describe the approved target, **not
shipped behavior**; only the README's shipped sections,
`docs/architecture.md`'s shipped scope, `docs/verification.md`'s pinned
list, and the code and tests state what ships.

Before committing: `make test`, `make vet`, `go test -race ./...`, and
check that every internal link in changed docs resolves.

## Concurrency posture

`Unmarshal`, `Marshal`, and `Skip` are stateless package functions: no
package-level mutable state, no retained input — safe for concurrent
calls from any number of goroutines. The caller owns the input slice
(never retained, safe to reuse after the call) and the results (freshly
allocated, safe to mutate).
