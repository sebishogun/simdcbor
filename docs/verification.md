# Verification

How this repository proves what it claims, now and at full-codec
completion. Rule of thumb throughout: **a claim is a test or a measured
record, or it is not made.**

## Shipped gates

```
make test        # go test ./...
make vet         # gofmt -l . ; go vet ./...
make bench       # one process, shuffled, count=6, minimum of the count
make bench-check # same, tee'd to /tmp/simdcbor-bench.txt, 8% floor
go test -race ./...
```

The bench numbers the README quotes are **minima** of repeated runs on
one quiet machine (amd64/avx512), the floor being 8.3% (code-layout
noise; see CLAUDE.md). `bench-check` currently tees to
`/tmp/simdcbor-bench.txt` for comparison against the quoted record; the
reference file `testdata/bench.txt` is not yet committed — the 8% floor
is a gate discipline, not a committed artifact.

## What the tests pin today

- **Oracle decode**: values built by a seeded generator are encoded by
  fxamacker/cbor, decoded by `Unmarshal`, and compared against the same
  value round-tripped through `encoding/json` — shape parity with JSON on
  the bytes fxamacker produces (3000 cases).
- **Round trip**: `Marshal` → `Unmarshal` is identity on the shaped set,
  and fxamacker decodes our bytes (3000 cases); the same map encodes to
  the same bytes (canonical-within-the-subset claim).
- **Skip parity**: `Skip` and `Unmarshal` agree on accept/reject and on
  span, over a generated corpus (5000 cases).
- **Truncation**: every prefix of a real item errors; **random bytes**
  never panic (5000 inputs); the fuzzer-caught presize overflow is
  regression-covered by the pre-flight bound.
- **Floats**: half/single/double decode to the same `float64`.

These tests bound every claim the README makes about the **subset**.
Claims the tests do not cover — full RFC conformance, arbitrary keys,
tags, indefinite forms, canonical modes — are explicitly not made.

## Full-codec gates (target)

When the production plan lands, verification grows to:

1. **RFC 8949 vectors.** Every item from RFC 8949 appendix A (and the
   §3.4/§4 example items) decodes to the documented value and encodes
   back to the documented bytes. Vectors are committed testdata, not
   generated.
2. **fxamacker interop.** Both directions, across modes: our bytes decode
   in fxamacker (default and canonical modes); fxamacker's bytes decode
   here; arbitrary-key and tag forms included. Where the two libraries
   legitimately differ (duplicate policy, tag interpretation), the
   difference is pinned by a test with the reason in a comment — the
   oracle is the RFC, not fxamacker.
3. **Duplicate/canonical profiles.** Each `DuplicatePolicy` × each
   ordering mode has a profile test: specific inputs, specific bytes,
   specific errors.
4. **Fuzz.** `go test -fuzz` with a seeded corpus (RFC vectors +
   truncated corpus items + structured garbage) under `-race`, asserting
   no panic, no hang, no allocation unbounded by input size. Fuzz runs
   before and after every decoder change; the presize bug is the standing
   warning.
5. **Race and cross-arch.** `go test -race ./...` on every wire-touching
   phase; `GOARCH=arm64` and `GOARCH=386` build + test (the package is
   pure Go plus simd's portable kernels; no assembly of its own), plus a
   big-endian build (`GOARCH=s390x` or `ppc64le`) for the argument
   assembly.
6. **Benchmarks.** The sweep (nested record, strings, numbers, huge
   array) and the filter-stream benchmark, run under the rules below;
   every quoted number is the minimum of ≥3 runs, one process, shuffled,
   machine quiet.
7. **Disassembly.** Any performance claim or regression explanation cites
   `go tool objdump` output, per CLAUDE.md — register pressure, bounds
   checks, inlining, branch layout are the things wall-clock cannot
   separate.

### Benchmark rules (binding)

- noise floor **8.3%**; sub-floor claims compare
  `perf stat -e instructions:u,cycles:u` (layout-independent) and cite
  disassembly;
- A/B builds **interleaved in one session**, compared on the minimum,
  never across sessions;
- **never pipe a gate through `tail`** without `pipefail` — a red run
  must stay red.

## Documentation checks

- every internal link in the README and `docs/` resolves to a committed
  file (`docs/bench.svg` is a committed asset);
- every claim about the shipped API matches `decode.go`/`encode.go`/
  `skip.go` and the tests — the docs-only branch rule exists to keep the
  two from drifting;
- a measurement or a commit is required before any entry enters
  `docs/wrong.md`.
