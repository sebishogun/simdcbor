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

## Documentation-only branch

`docs/v120-documentation` may change only `.md` files: no Go, no tests,
no module files, no Makefile, no assets, no workflows, no releases, no
push. Verify with `make test`, `make vet`, and `go test -race ./...`
before committing, and check that every internal link in changed docs
resolves.
