# Verification

How this repository proves what it claims. Rule of thumb throughout: **a
claim is a test or a measured record, or it is not made.**

## Current state, 2026-08-14

The full codec is live and the shipped API is an adapter over it. Every
gate below is green on amd64, toolchain `go1.26.2`:

```
go test ./...                      # root, value, internal/codec, diag
go vet ./...
go test -race ./...
go test -run '^$' -fuzz FuzzUnmarshalNeverPanics -fuzztime 2m .
make bench-check                   # against testdata/bench.txt, no pipe carries the verdict
GOARCH={arm64,386,ppc64le,s390x} go build ./... && go vet ./...
```

Cross-architecture is build-and-vet only; `-race` runs on amd64, which is
the architecture this machine has. The 386 lane earned its place
immediately: it caught an untyped `1 << 32` in a test corpus that
overflows `int` on a 32-bit build.

Boundary parity between `Unmarshal` and `SkipStrict` is asserted over the
whole head space (all 256 heads, all 256 two-byte simple payloads), over
the RFC vectors, over 200k random inputs and 50k generated documents. It
is also structural: both are one walk with different build steps, so the
four divergences that lived here before have nowhere left to live.

The hot path was reviewed in disassembly, not assumed: `decodeJSON` is 328
instructions with no bounds check, no indirect call and no multiply; the
head reader is 182 with eight bounds checks, which are the length guards
themselves.

## Shipped gates

```
make test        # go test ./...
make vet         # gofmt -l . ; go vet ./...
make bench       # one process, shuffled, count=6, minimum of the count
make bench-check # pipe-safe comparison against committed baseline, 8% guard
go test -race ./...
```

The README numbers are historical minima. `bench-check` now runs
`scripts/bench-check.sh` with no verdict-carrying pipe and compares against the
committed `testdata/bench.txt`. That baseline was captured at load 4.82, so it
is an operational regression guard, not publishable quiet-host evidence;
regenerate it with `make bench-baseline` below load 1 before citing a
wall-clock comparison. The code-layout floor remains 8.3% (CLAUDE.md).

## What the tests pin today

- **Oracle decode**: values built by a seeded generator are encoded by
  fxamacker/cbor, decoded by `Unmarshal`, and compared against the same
  value round-tripped through `encoding/json` — shape parity with JSON on
  the bytes fxamacker produces (3000 cases).
- **Round trip**: `Marshal` → `Unmarshal` is identity on the shaped set,
  and fxamacker decodes our bytes (3000 cases); the same map encodes to
  the same bytes (canonical-within-the-subset claim).
- **Skip contracts**: `SkipStrict` and `Unmarshal` agree on accept/reject and
  span over all 256 heads, all 256 two-byte simple payloads, RFC vectors,
  200k random inputs, and 50k generated documents. Plain `Skip` is asserted
  one-directionally as the framing superset. The measured split and the four
  historical divergences are closed in `docs/wrong.md`.
- **Truncation**: every prefix of a real item errors; **random bytes**
  never panic. `TestPresizeBounded`, `TestDepthCap`, and
  `TestTruncationNeverDecodesClean` pin the fuzzer-found allocation and limit
  boundaries.
- **Floats**: half/single/double decode to the same `float64`.

These tests bound every root-adapter claim. The full-codec gates below cover
RFC vectors, arbitrary keys, tags, indefinite forms, and deterministic modes;
a tagged full-RFC release claim remains gated by the R2 ledger.

## Full-codec gates (live)

The executed production plan added:

1. **RFC 8949 vectors.** Every item from RFC 8949 appendix A (and the
   §3.4/§4 example items) decodes to the documented value and encodes
   back to the documented bytes. Vectors are committed testdata, not
   generated.
2. **fxamacker interop.** Both directions, across modes, paired
   correctly against the installed v2.9.2: fxamacker's
   `CoreDetEncOptions()` (bytewise `SortCoreDeterministic`) ↔ our
   `CoreDeterministic`; `CanonicalEncOptions()` (length-first
   `SortCanonical` — fxamacker's "canonical" is the RFC 7049 §3.9 legacy
   ordering, not bytewise) ↔ our `LengthFirst`. Where the two libraries
   legitimately differ (duplicate policy, tag interpretation, fxamacker's
   NaN→`0xf97e00`/Inf→float16 normalization under its deterministic
   options), the difference is pinned by a test with the reason in a
   comment — the oracle is the RFC, not fxamacker.
3. **Duplicate-policy and deterministic-mode profiles.** Each
   `DuplicatePolicy` × each ordering mode (`CoreDeterministic`,
   `LengthFirst`) has a profile test: specific inputs, specific bytes,
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
