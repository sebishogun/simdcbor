# simdcbor Production Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build the full RFC 8949 codec — `simdcbor/value`, `internal/codec` (streaming decoder/encoder), `simdcbor/diag` — with the shipped JSON-shaped API preserved as an explicit, byte-compatible adapter.

**Architecture:** The design (read first, it is binding): `docs/plans/2026-08-13-simdcbor-production-design.md` and the LLDs it cites — `docs/lld/data-model.md`, `docs/lld/decoder.md`, `docs/lld/encoder.md`, `docs/lld/streaming-lazy-and-diagnostic.md`. Order: safety, value model (pure data, no wire), streaming decoder, streaming encoder, canonical/deterministic modes, tags, lazy values, diagnostic notation, then the adapter. Every phase keeps the existing test suite green; the adapter never changes shipped behavior.

**Tech Stack:** Go 1.26 (`go.mod` already says 1.26.2), fxamacker/cbor v2.9.2 (already a dependency, the interop oracle), simd v1.20.0 (already a dependency, for `ValidUTF8` and kernels), standard `testing`/`testing/fuzz`, `go test -race`, `go vet`, `go tool objdump`, `perf stat`.

**Self-containment:** The executor needs only this repository, this plan, and `AGENTS.md`/`CLAUDE.md` at the repository root — both already exist, are current, and are mandatory reading before Task 1. No other context. Commit after every task; never on `main` (work on a feature branch, e.g. `feat/full-codec`).

**Ground rules (from CLAUDE.md, binding):** disassemble before explaining slowness; the 8.3% layout noise floor; interleaved A/B on the minimum; `perf stat -e instructions:u,cycles:u` for sub-floor claims; never pipe a gate through `tail` without `pipefail`; every allocation bounded by remaining input before it happens; every measurement or deferral that argues against a change goes into `docs/wrong.md` with its source.

---

### Task 0: Preflight

**Files:**
- Verify: `AGENTS.md`, `CLAUDE.md`, `README.md`, `docs/architecture.md`, `docs/roadmap.md`, `docs/verification.md`, `docs/wrong.md`, all of `docs/lld/`, both `docs/plans/` files, `docs/bench.svg`

**Step 1: Confirm the shipped baseline is green**

Run: `go test ./... && go vet ./... && go test -race ./...`
Expected: PASS, PASS, PASS. If any red, stop — do not build on a red baseline.

**Step 2: Confirm the branch**

Run: `git branch --show-current`
Expected: a feature branch, not `main`. Create one if needed: `git checkout -b feat/full-codec`.

**Step 3: Confirm the agent files are self-contained**

Read `AGENTS.md` and `CLAUDE.md`. They must name the shipped API, the subset gaps, the commands, the bench rules, and `docs/wrong.md`'s sourcing rule without referencing anything outside this repository. Fix them (docs-only) if they drift. Commit: `git add AGENTS.md CLAUDE.md && git commit -m "docs: keep agent files current"` (only if you changed them).

---

### Task 1: Safety hardening

**Files:**
- Create: `fuzz_test.go` (package simdcbor)
- Create: `testdata/fuzz/corpus/` seed files (RFC 8949 appendix A items, truncated prefixes, structured garbage)
- Modify: `decode_test.go` (limit-pin tests)
- Create: `limits_test.go`

**Step 1: Write the failing no-panic property test**

```go
// fuzz_test.go
package simdcbor

import "testing"

// FuzzUnmarshalNeverPanics: any input, any prefix, must error or decode;
// must never panic, hang, or allocate beyond the input's size.
func FuzzUnmarshalNeverPanics(f *testing.F) {
	for _, seed := range corpusSeeds() {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		Unmarshal(data)           // no panic
		Skip(data)                // same reject boundary
		for i := range data {
			Unmarshal(data[:i])   // every truncation errors or decodes
			Skip(data[:i])
		}
	})
}
```

Note: fuzz tests need an explicit `corpusSeeds` helper returning `[][]byte` built from the RFC appendix A bytes of `testdata/` (definite and indefinite items, tags, floats, `break` bytes, reserved `ai` 28–30).

**Step 2: Run it to verify it fails (or is at least unsatisfied)**

Run: `go test -run FuzzUnmarshalNeverPanics -fuzz FuzzUnmarshalNeverPanics -fuzztime 30s .`
Expected: no panic (the shipped decoder already survives; the *test* is the deliverable here), and the fuzz corpus persists to `testdata/fuzz/`.

**Step 3: Pin the limits with regression tests**

Add to `limits_test.go`:

```go
func TestDepthCap(t *testing.T)      // 64 nested containers decode; 65 → ErrMalformed
func TestPresizeBounded(t *testing.T) // header claiming 2^32-1 items in a 10-byte input → ErrTruncated, no big alloc
func TestTruncationNeverErrorsAsClean(t *testing.T) // every prefix of a valid item errors
```

Run: `go test -run 'TestDepthCap|TestPresizeBounded|TestTruncationNeverErrorsAsClean' .`
Expected: PASS. The presize test is the regression for the fuzzer-caught overflow recorded in `docs/wrong.md`.

**Step 4: Full fuzz budget under race**

Run: `go test -race -run FuzzUnmarshalNeverPanics -fuzz FuzzUnmarshalNeverPanics -fuzztime 2m .` (with `set -o pipefail` if you pipe anything)
Expected: clean exit, no crash, corpus grew.

**Step 5: Commit**

```bash
git add fuzz_test.go limits_test.go testdata/fuzz/
git commit -m "test: pin decoder safety properties with fuzz and limits"
```

---

### Task 2: Value model — `simdcbor/value`

**Files:**
- Create: `value/value.go` (kinds, `Value`, `Simple`, `Tag`, `KeyValue`)
- Create: `value/value_test.go`
- Create: `value/keys.go` (canonical key encoding, comparators, structural-key hash)
- Create: `value/keys_test.go`
- Create: `value/order.go` (`Deterministic`, `Canonical` comparators)
- Create: `value/order_test.go`
- Create: `value/doc.go`

**Step 1: Write the failing fidelity tests**

`value/value_test.go` pins the data-model LLD: `Float16`/`Float32`/`Float64` keep wire bits (`NaN` payloads, `-0.0`, signaling bits round-trip through encode/decode helpers); `Uint` exact to `2^64-1`; `NegInt` exact to `-2^63`; conversion to `float64` is a separate explicit function (`AsFloat64`) and is lossy above `2^53` — test that `Uint(2^63+1).AsFloat64()` is documented-lossy, never silent-exact.

Run: `go test ./value/`
Expected: FAIL (types do not exist yet).

**Step 2: Implement the value model**

`value.go` per the LLD: kind-tagged `Value`; `Simple` with named `False`/`True`/`Null`/`Undefined`; `Tag{Number uint64, Value Value}`; ordered `Map []KeyValue`. Include `AsFloat64` and a `String()` only if the diag task needs it later (YAGNI: skip `String()` until Task 8).

**Step 3: Run the tests**

Run: `go test ./value/`
Expected: PASS.

**Step 4: Write the failing key/order tests**

`keys_test.go`: bytes key `h'00'` equals bytes key `h'00'` and differs from `h'0000'`; float key `0xf9 3c00` equals `0xfa 3f800000` (both `1.0`) under canonical-encoding equality, and `NaN` keys are stable (each NaN key equals itself under canonical encoding); structural keys rejected by default, enabled by a mode flag, and then `[]Value{1,2}` ≠ `[]Value{1,2,0}`.

`order_test.go`: the `Deterministic` comparator is length-first then bytewise; `Canonical` is bytewise only; a specific pair of keys (e.g. `h'ff'` vs `h'00'` — same length, bytewise decides; `h'ff'` vs `h'0000'` — length decides in deterministic, bytewise decides in canonical) pins both.

Run: `go test ./value/`
Expected: FAIL.

**Step 5: Implement keys and ordering**

Per the data-model LLD. The canonical key encoding is the shortest wire form of the key; floats canonicalize to their shortest round-tripping width so equal values are equal keys.

**Step 6: Run the tests**

Run: `go test ./value/ && go vet ./value/`
Expected: PASS, PASS.

**Step 7: Commit**

```bash
git add value/
git commit -m "feat(value): exact value model with fidelity, keys, ordering"
```

---

### Task 3: Streaming decoder — `internal/codec`

**Files:**
- Create: `internal/codec/decoder.go` (cursor, head→argument→body machine, limits)
- Create: `internal/codec/decoder_test.go`
- Create: `internal/codec/limits.go` (`Limits`, error taxonomy)
- Create: `internal/codec/skip.go` (same-machine `Skip`)
- Create: `internal/codec/skip_test.go`
- Create: `internal/codec/stream.go` (reader-backed refill, `Next`/`More`)
- Create: `internal/codec/stream_test.go`
- Create: `internal/codec/doc.go`

**Step 1: Write the failing decode tests against the RFC vectors**

Commit `testdata/rfc8949/` first (a `testdata` directory inside `internal/codec`): every appendix A item as `(hex bytes, kind, expected value)` triples — definite and indefinite arrays/maps, all integer widths, all float widths, tags, simple values, `break`, reserved `ai` 28–30.

`decoder_test.go`: for each vector, decode and compare against the value-model expectation; `break` outside an indefinite container → `ErrMalformed`; truncated vector → `ErrTruncated`; adapter-mode decode of a non-string-key map → the adapter's error (see Task 9), decode in `Keep`-key mode succeeds with a `Bytes` key.

Run: `go test ./internal/codec/`
Expected: FAIL (no decoder yet).

**Step 2: Implement the decoder**

Per `docs/lld/decoder.md`, in this order: head→argument (reserved `ai` and `ai 31` rejection), definite containers with pre-flight bounds and presize cap, indefinite containers with the break state machine, tags (`Keep` mode; `Discard` mode is Task 9's adapter), `Limits` enforcement at framing, ownership (borrowed `RawMessage`-shaped accessor, copied string/bytes). Depth default 64. No `float64` widening: values are exact per the value model.

**Step 3: Run the tests**

Run: `go test ./internal/codec/`
Expected: PASS.

**Step 4: Write the failing Skip-parity test**

`skip_test.go`: property test over generated corpora (reuse the generator pattern from the shipped `skip_test.go`): for every item, `Skip` and the decoder's span agree, and accept/reject agree — including indefinite containers, tags, and arbitrary keys.

Run: `go test ./internal/codec/ -run TestSkipParity`
Expected: FAIL.

**Step 5: Implement `Skip` on the same machine**

`internal/codec/skip.go` — the build step removed, the framing step shared. This is the shipped invariant (`skip.go`'s doc comment) carried forward: a skip that succeeds is an item the decoder consumes, same span.

**Step 6: Run the tests**

Run: `go test ./internal/codec/`
Expected: PASS.

**Step 7: Write the failing streaming tests**

`stream_test.go`: a reader that hands bytes in 3-byte chunks; a record split across every possible chunk boundary decodes identically to the whole-buffer decode; `io.EOF` mid-item → `ErrTruncated`; `io.EOF` at an item boundary → clean end; `Next` over a concatenated sequence returns items one at a time with the cursor at the next head.

Run: `go test ./internal/codec/ -run TestStreaming`
Expected: FAIL.

**Step 8: Implement reader-backed streaming**

Per `docs/lld/streaming-lazy-and-diagnostic.md`: refill distinguishes split-items from clean EOF; the state machine is the buffer cursor's, the refill only relocates it.

**Step 9: Run the tests**

Run: `go test ./internal/codec/ && go vet ./internal/codec/ && go test -race ./internal/codec/`
Expected: PASS, PASS, PASS.

**Step 10: Fuzz the new decoder**

Run: `go test -run '^$' -fuzz FuzzDecoder -fuzztime 1m ./internal/codec/` with a seed corpus of RFC vectors plus truncated prefixes. Add the fuzz function in `decoder_test.go`.
Expected: clean; any finding is a bug — fix it and add the input to the corpus (that is how the shipped presize bug was found; it is the standing warning).

**Step 11: Commit**

```bash
git add internal/codec/ internal/codec/testdata/
git commit -m "feat(codec): streaming decoder with indefinite forms, limits, skip parity"
```

---

### Task 4: Streaming encoder — `internal/codec`

**Files:**
- Create: `internal/codec/encoder.go`
- Create: `internal/codec/encoder_test.go`

**Step 1: Write the failing round-trip tests**

`encoder_test.go`:
- every RFC vector decodes with the Task 3 decoder, encodes, and re-decodes to an equal value;
- `StartArray(n)`/`EndArray` and indefinite forms emit exactly the documented bytes (`9f ... ff`);
- the container stack rejects: `End` on an empty stack, `EndArray` over a map, overrunning a definite count — before any bytes are written;
- `WriteTag(55799)` emits `d9 d9f7` then the value; no tag is emitted unless written.

Run: `go test ./internal/codec/ -run TestEncoder`
Expected: FAIL.

**Step 2: Implement the encoder**

Per `docs/lld/encoder.md`: forward append only, shortest heads, container stack, `WriteTag`, indefinite via explicit `StartIndefinite*`/`End*` (the only `0xff` writers). Float rule for now: adapter-shaped (`float32`-if-round-trips, else `float64`); the `float16` extension arrives with the modes in Task 5.

**Step 3: Run the tests**

Run: `go test ./internal/codec/ && go vet ./internal/codec/`
Expected: PASS, PASS.

**Step 4: Write the failing interop test**

`encoder_test.go`: encode values from the shipped generator corpus, decode with fxamacker (`cbor.Unmarshal` into `any` where the value model maps onto JSON shapes; into fxamacker's `cbor.Tag`/`RawTag` types otherwise), and require equality per the data model's canonical-encoding equality.

Run: `go test ./internal/codec/ -run TestInteropFxA`
Expected: FAIL.

**Step 5: Fix to green**

Expected: PASS. Where fxamacker and the design legitimately differ (duplicate policy defaults, tag interpretation), pin the difference with a comment-carrying test — the oracle is the RFC, not fxamacker.

**Step 6: Commit**

```bash
git add internal/codec/
git commit -m "feat(codec): streaming encoder, container stack, fxamacker interop"
```

---

### Task 5: Canonical and deterministic modes

**Files:**
- Modify: `internal/codec/encoder.go` (mode plumbing, float16 rule, key ordering)
- Create: `internal/codec/modes.go`
- Create: `internal/codec/modes_test.go`
- Modify: `internal/codec/decoder.go` (duplicate detection via canonical key equality)

**Step 1: Write the failing mode tests**

`modes_test.go`:
- `Deterministic` sorts length-first, bytewise (`h'ff'` before `h'0000'`); `Canonical` sorts bytewise (`h'0000'` before `h'ff'`);
- deterministic/canonical float rule: `1.0` encodes `f9 3c00`, `1.5` encodes `f9 3e00`, a value that does not round-trip through `float16` encodes `fa`/`fb`; the adapter mode (Task 9) never emits `f9`;
- duplicate policies: `Error` → `ErrDuplicateKey` on the second canonical-equal key; `FirstWins`/`LastWins` pin their map results; `0xf9 3c00` and `0xfa 3f800000` are the same key under every policy.

Run: `go test ./internal/codec/ -run 'TestModes|TestDuplicates'`
Expected: FAIL.

**Step 2: Implement modes**

Per the data-model and encoder LLDs: a `Mode` enum on the encoder (adapter/deterministic/canonical) and a `DuplicatePolicy` on the decoder; the float16 forward conversion (the inverse of `halfToFloat32bits` — the decode side already exists in `decode.go`); key ordering via `value/order.go` comparators.

**Step 3: Run the tests**

Run: `go test ./internal/codec/ && go test ./value/`
Expected: PASS, PASS.

**Step 4: Write the failing profile tests**

`modes_test.go`: fxamacker's canonical mode (`cbor.CanonicalEncOptions()`) decodes our `Canonical`-mode bytes, and vice versa; a duplicate-key input decodes under each policy to the documented result/error.

**Step 5: Run and fix to green**

Run: `go test ./internal/codec/ -run TestProfiles`
Expected: PASS.

**Step 6: Commit**

```bash
git add internal/codec/
git commit -m "feat(codec): canonical and deterministic modes, duplicate policies"
```

---

### Task 6: Tags

**Files:**
- Modify: `internal/codec/decoder.go`, `internal/codec/encoder.go` (interpret mode)
- Create: `internal/codec/tags.go`
- Create: `internal/codec/tags_test.go`

**Step 1: Write the failing tag tests**

`tags_test.go`:
- `Keep` mode: `c0 78 18 ...` decodes to `Tag{0, Text}` and re-encodes to the same bytes;
- `Interpret` mode (opt-in): tags 0/1/2/3/4/5/32/33/34/36/55799 map to the documented native forms (epoch float, bignum via `math/big`, etc.); unknown tags stay generic;
- adapter mode (`Discard`) is Task 9's behavior, pinned here as the mode's default off.

Run: `go test ./internal/codec/ -run TestTags`
Expected: FAIL.

**Step 2: Implement tag modes**

Generic `Tag` storage already exists from Task 2/3; this task adds the interpret table and the mode plumbing. YAGNI: only the tags listed in the data-model LLD get native forms.

**Step 3: Run the tests**

Run: `go test ./internal/codec/ && go vet ./internal/codec/`
Expected: PASS, PASS.

**Step 4: Commit**

```bash
git add internal/codec/
git commit -m "feat(codec): tag keep/interpret modes"
```

---

### Task 7: Lazy values

**Files:**
- Modify: `internal/codec/decoder.go` (range framing, pin-copy mode)
- Create: `internal/codec/raw.go`
- Create: `internal/codec/raw_test.go`

**Step 1: Write the failing lazy tests**

`raw_test.go`:
- a buffer-backed decode of a 5,000-item array frames each item as a range; materializing one item decodes exactly that item;
- `RawMessage.Bytes()` returns the borrowed subslice; mutating the input after framing is a documented caller bug (asserted only in a comment, not tested);
- reader-backed decoder: raw messages rejected by default (`ErrLazyUnavailable`-class), pin-copy mode makes them valid;
- filter-then-read: framing 10,000 records with lazy values allocates nothing per record (assert with `testing.AllocsPerRun`).

Run: `go test ./internal/codec/ -run TestLazy`
Expected: FAIL.

**Step 2: Implement lazy framing**

Per `docs/lld/streaming-lazy-and-diagnostic.md`. The frame pass is the Task 3 machine with the build step replaced by range capture.

**Step 3: Run the tests**

Run: `go test ./internal/codec/`
Expected: PASS.

**Step 4: Measure the filter-then-read win and record it**

Run: `go test -run '^$' -bench . -benchmem -count=6 -shuffle=on ./internal/codec/` (one process, machine quiet).
Compare the lazy filter against the shipped decode-all and skip-then-decode benchmarks (the shipped `bench_test.go` figures: decode-all 662 us vs skip-filter 79 us on the 2,000-record stream). If lazy values deliver the predicted allocation drop, record the delta in `docs/wrong.md`'s lazy-values entry as the superseding measurement; if they do not, record **that** finding — the entry exists precisely to hold whichever measurement arrives.

**Step 5: Commit**

```bash
git add internal/codec/
git commit -m "feat(codec): lazy values as framed byte ranges"
```

---

### Task 8: Diagnostic notation — `simdcbor/diag`

**Files:**
- Create: `diag/diag.go` (render)
- Create: `diag/parse.go`
- Create: `diag/diag_test.go`
- Create: `diag/doc.go`

**Step 1: Write the failing notation tests**

`diag_test.go`, per the LLD table: `00`→`0`, `20`→`-1`, `40 01 02`→`h'0102'`, `61 61`→`"a"`, `f4`/`f5`/`f6`/`f7`, `f9 3c00`→`1.0`, `e0`→`simple(0)`, `80`, `9f 00 ff`→`[_ 0]`, `bf ... ff`→`{_ ...}`, `c0 ...`→`0("...")`, floats as shortest round-tripping decimal, `nan`/`infinity`/`-infinity`. Parse side round-trips through the value model. Error-prefix rendering: a truncated item renders the well-formed prefix in notation.

Run: `go test ./diag/`
Expected: FAIL.

**Step 2: Implement render and parse**

`diag` depends only on `internal/codec` and `simdcbor/value` (never the root package). Rendering floats uses the shortest-decimal machinery simd ships for JSON (`parsefloat`/`dtoa` in the parent library; re-exported or vendored as needed).

**Step 3: Run the tests**

Run: `go test ./diag/ && go vet ./diag/ && go test -race ./diag/`
Expected: PASS, PASS, PASS.

**Step 4: Commit**

```bash
git add diag/
git commit -m "feat(diag): RFC 8949 diagnostic notation render and parse"
```

---

### Task 9: Adapter — the shipped API over the new core

**Files:**
- Modify: `decode.go`, `encode.go`, `skip.go` (reimplement as adapters over `internal/codec`)
- Modify: `decode_test.go`, `encode_test.go`, `skip_test.go` (unchanged assertions must pass untouched)
- Create: `adapter_test.go` (byte-identity and boundary parity)

**Step 1: Write the failing adapter tests**

`adapter_test.go`:
- **byte identity**: on the shipped corpus, `Marshal` produces the exact bytes the pre-adapter encoder produced (snapshot a corpus of encoded outputs into `testdata/adapter/` at this task's start, before reimplementing);
- **shape identity**: `Unmarshal` returns the JSON shapes (`map[string]any`/`[]any`/`float64`), tags discarded, duplicates last-wins, depth 64 → `ErrMalformed`, non-string key → `ErrMalformed`, `undefined` → `nil`;
- **boundary parity**: `Skip` and `Unmarshal` agree on accept/reject and span on the new core, exactly as the shipped `TestSkipMatchesUnmarshal` asserts;
- the shipped `decode_test.go`/`encode_test.go`/`skip_test.go` files pass **without modification**.

Run: `go test ./...`
Expected: FAIL where the adapter does not yet exist.

**Step 2: Reimplement the root package as the adapter**

`Unmarshal` → configure the new decoder with adapter mode (JSON shapes, `float64` conversion via the value model's `AsFloat64`, `Discard` tags, `LastWins`, depth 64 with `ErrDepth`→`ErrMalformed` mapping, `ErrLimit`/`ErrUnsupportedKey`→`ErrMalformed`), decode one item, return `(value, n, err)`. `Marshal` → adapter mode encoder, byte-identical output (including: no `float16`, `NaN` as `fb` double, `uint` still unsupported, `[]byte`→bytes, sorted bytewise). `Skip` → the new core's skip with adapter limits. The two error values stay the only exported errors from the root package.

**Step 3: Run the full suite**

Run: `go test ./... && go vet ./... && go test -race ./...`
Expected: PASS, PASS, PASS — including the unmodified shipped tests.

**Step 4: Fuzz the adapter**

Run: `go test -run FuzzUnmarshalNeverPanics -fuzz FuzzUnmarshalNeverPanics -fuzztime 2m .` plus the new decoder's fuzz target.
Expected: clean.

**Step 5: Commit**

```bash
git add decode.go encode.go skip.go adapter_test.go testdata/adapter/
git commit -m "refactor: shipped API as explicit adapter over the full codec"
```

---

### Task 10: Final gates and records

**Files:**
- Modify: `README.md` (shipped API + subset unchanged; add links to the new packages and docs)
- Modify: `docs/verification.md` (mark the full-codec gates as live)
- Modify: `docs/wrong.md` (add any measurements this work produced)

**Step 1: Full gate run**

Run each bare or with `set -o pipefail` first:

```
go test ./...
go vet ./...
go test -race ./...
go test -run '^$' -fuzz FuzzUnmarshalNeverPanics -fuzztime 2m .
go test -run '^$' -fuzz FuzzDecoder -fuzztime 2m ./internal/codec/
GOARCH=arm64 go build ./... && GOARCH=arm64 go test ./...
GOARCH=386 go build ./... && GOARCH=386 go test ./...
GOARCH=ppc64le go build ./... && GOARCH=ppc64le go test ./...
make bench
```

Expected: all green. Any failure stops the task.

**Step 2: Benchmark sweep with the rules**

Run the sweep (nested record, strings, numbers, huge array, filter stream) interleaved against fxamacker in one session, minimum of ≥3, machine quiet. Record the new numbers in `README.md` if they change materially; record **every** measurement that argues against a change or a deferral in `docs/wrong.md` with its source.

**Step 3: Disassembly pass on the hot paths**

Run: `go test -c -o /tmp/x.test ./internal/codec/ && go tool objdump -s 'codec\.' /tmp/x.test | less`
Review the decode hot loop: bounds checks eliminated where provable, no spilled loop counter, `append` inlined, the argument assembly is shifts not multiplies. Any explanation of a regression cites this output.

**Step 4: Cross-arch race on one big-endian build**

Run: `GOARCH=ppc64le go test -race ./...` (if the toolchain supports it; else document in `docs/verification.md` which arches got `-race`).

**Step 5: Documentation updates**

`README.md`: shipped API section unchanged in substance; add links to `simdcbor/value`, `simdcbor/diag`, `internal/codec` docs, and the design/plan. `docs/verification.md`: move the full-codec gates from target to live. `docs/wrong.md`: the lazy-value measurement from Task 7 and any other sourced findings. Check every internal link resolves.

**Step 6: Commit**

```bash
git add README.md docs/
git commit -m "docs: record full-codec gates, measurements, and links"
```

**Step 7: Report**

Report per the executing-plans protocol: each task's verification output, the SHA of the final commit, and the gate list with results. Do not push; do not tag; do not release.
