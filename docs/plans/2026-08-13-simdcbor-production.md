# simdcbor Production Implementation Plan

> **Execution status:** Tasks 0-11 below executed as R1 and remain the
> historical work record. Do not re-execute them. Current work starts in the
> R2 production-readiness ledger at the bottom of this file.

**Goal:** Build the full RFC 8949 codec — `simdcbor/value`, `internal/codec` (streaming decoder/encoder), `simdcbor/diag` — with the shipped JSON-shaped API preserved as an explicit, byte-compatible adapter.

**Architecture:** The design (read first, it is binding): `docs/plans/2026-08-13-simdcbor-production-design.md` and the LLDs it cites — `docs/lld/data-model.md`, `docs/lld/decoder.md`, `docs/lld/encoder.md`, `docs/lld/streaming-lazy-and-diagnostic.md`. Order: Stage 0 (Skip/Unmarshal simple-value consistency), safety, value model, streaming decoder, streaming encoder, deterministic modes, tags, lazy values, diagnostic notation, then the adapter. Every phase keeps the existing test suite green; the adapter never changes shipped behavior.

**Tech Stack:** Go 1.26 (`go.mod` already says 1.26.2), fxamacker/cbor v2.9.2 (already a dependency, the interop oracle), simd v1.20.0 (already a dependency, for `ValidUTF8` and kernels), standard `testing`/`testing/fuzz`, `go test -race`, `go vet`, `go tool objdump`, `perf stat`.

**Self-containment:** The executor needs only this repository, this plan, and `AGENTS.md`/`CLAUDE.md` at the repository root — both already exist, are current, and are mandatory reading before Task 1. No other context. Commit after every task; never on `main` (work on a feature branch, e.g. `feat/full-codec`).

**Ground rules (from CLAUDE.md, binding):** disassemble before explaining slowness; the 8.3% layout noise floor; interleaved A/B on the minimum; `perf stat -e instructions:u,cycles:u` for sub-floor claims; never pipe a gate through `tail` (or anything) without `pipefail`; every allocation bounded by remaining input before it happens; every measurement or deferral that argues against a change goes into `docs/wrong.md` with its source.

**Known shipped bug this plan fixes first:** `Skip` accepts simple values (`ai` 0–19, `0xf8` form) that `Unmarshal` rejects — the shipped parity test cannot see it (corpus never generates those values; the random-bytes loop discards both errors). Recorded in `docs/wrong.md`.

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

Read `AGENTS.md` and `CLAUDE.md`. They must name the shipped API, the subset gaps, the Skip/Unmarshal simple-value bug, the commands, the bench rules, and `docs/wrong.md`'s sourcing rule without referencing anything outside this repository. Fix them (docs-only) if they drift. Commit: `git add AGENTS.md CLAUDE.md && git commit -m "docs: keep agent files current"` (only if you changed them).

---

### Task 1: Skip/Unmarshal simple-value consistency (Stage 0)

**Files:**
- Modify: `skip.go` (simple-value accept set)
- Modify: `skip_test.go` (head-byte enumeration test)

**Context:** `skip.go`'s `case mtUint, mtNegInt, mtSimple: return j, nil` accepts every simple `ai` the argument reader allows — including `ai` 0–19 (`0xe0`–`0xf3`) and the two-byte `0xf8` form — while `decode.go` handles only `ai` 20–23 and 25–27 and rejects the rest with `ErrMalformed`. The doc comment on `skip.go` claims the accept/reject boundary is identical to `Unmarshal`'s; it is not. `TestSkipMatchesUnmarshal` cannot see it: the generator never produces those simple values and the random-bytes loop discards both errors (`_ = ue; _ = se`).

The fix direction is policy-driven, not "add the same rejection to Skip and stop": the approved target supports the full simple-value model (`docs/lld/data-model.md`), so in the end both paths accept the full space. Stage 0 is the interim consistency on the shipped subset — both paths reject the values outside it — so no shipped `Unmarshal` behavior changes, and the divergence cannot survive into the adapter.

**Step 1: Write the failing enumeration test**

In `skip_test.go`:

```go
// TestSkipAgreesWithUnmarshalOnEveryHead: for every head byte and the
// two-byte simple form, Skip and Unmarshal must agree on accept/reject
// and on span. This is the test the corpus-based test cannot be:
// it enumerates the whole head space, so the simple-value divergence
// (0xe0-0xf3, 0xf8 xx) cannot hide.
func TestSkipAgreesWithUnmarshalOnEveryHead(t *testing.T) {
	for h := 0; h < 256; h++ {
		b := []byte{byte(h), 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
		_, un, uerr := Unmarshal(b)
		sn, serr := Skip(b)
		if (uerr == nil) != (serr == nil) {
			t.Errorf("head %02x: unmarshal %v skip %v", h, uerr, serr)
		}
		if uerr == nil && sn != un {
			t.Errorf("head %02x: span %d != consumed %d", h, sn, un)
		}
		// The two-byte simple form, for every payload byte.
		for p := 0; p < 256; p++ {
			b := []byte{0xf8, byte(p), 0x00}
			_, un, uerr := Unmarshal(b)
			sn, serr := Skip(b)
			if (uerr == nil) != (serr == nil) {
				t.Errorf("f8 %02x: unmarshal %v skip %v", p, uerr, serr)
			}
			if uerr == nil && sn != un {
				t.Errorf("f8 %02x: span %d != consumed %d", p, sn, un)
			}
		}
	}
}
```

Run: `go test -run TestSkipAgreesWithUnmarshalOnEveryHead .`
Expected: FAIL — the heads `e0`–`f3` and every `f8 xx` disagree (Skip accepts, Unmarshal rejects). This is the bug, now pinned.

**Step 2: Implement the consistency fix**

In `skip.go`, give the simple case the same accept set as `decode.go` for the shipped subset — `ai` 20–23 and 25–27 accept, `ai` 0–19 and 24 reject with `ErrMalformed` (readArg already rejects 28–30 and 31). Structure it as a policy check shared by both paths, so the decoder task (Task 4) only widens the policy to the full simple-value model — the two paths cannot drift again. Correct the doc comment's identical-boundary claim (a known implementation-doc defect, `docs/wrong.md`) to state the policy-driven accept set, so comment and code agree.

**Step 3: Run the tests**

Run: `go test ./...`
Expected: PASS — the enumeration test is green and every shipped test still passes unchanged.

**Step 4: Commit**

```bash
git add skip.go skip_test.go
git commit -m "fix: align Skip's simple-value accept set with Unmarshal (Stage 0)"
```

Note what is deliberately **not** done here: the generative corpus does not yet emit simple values, and the random-bytes loop still discards errors. That assertion work is scheduled in the decoder task (Task 4), where the full simple-value model lands and both paths accept the full space.

---

### Task 2: Safety hardening

**Files:**
- Create: `fuzz_test.go` (package simdcbor)
- Create: `testdata/fuzz/corpus/` seed files (RFC 8949 appendix A items, truncated prefixes, structured garbage)
- Modify: `decode_test.go` (limit-pin tests)
- Create: `limits_test.go`
- Modify: `Makefile` (`bench-check` pipefail)

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

Note: fuzz tests need an explicit `corpusSeeds` helper returning `[][]byte` built from the RFC appendix A bytes of `testdata/` (definite and indefinite items, tags, floats, `break` bytes, reserved `ai` 28–30, and the `0xe0`–`0xf3`/`0xf8` simple forms).

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

**Step 4: Fix the bench-check gate**

`bench-check` pipes `go test` through `tee` without `pipefail`, so the pipe reports `tee`'s status and a red run launders green (documented in `docs/verification.md`). Fix the Makefile so the gate fails loudly, e.g. `SHELL := bash -o pipefail` at the top or an explicit `set -o pipefail` in the target body.

Run: `make bench-check && echo GATE-OK`
Expected: the target runs, tees, and returns `tee`'s-and-`go test`'s real status.

**Step 5: Full fuzz budget under race**

Run: `go test -race -run FuzzUnmarshalNeverPanics -fuzz FuzzUnmarshalNeverPanics -fuzztime 2m .` (with `set -o pipefail` if you pipe anything)
Expected: clean exit, no crash, corpus grew.

**Step 6: Commit**

```bash
git add fuzz_test.go limits_test.go testdata/fuzz/ Makefile
git commit -m "test: pin decoder safety properties with fuzz and limits; fix bench-check pipefail"
```

---

### Task 3: Value model — `simdcbor/value`

**Files:**
- Create: `value/value.go` (kinds, `Value`, `Simple`, `Tag`, `KeyValue`)
- Create: `value/value_test.go`
- Create: `value/keys.go` (canonical key encoding, comparators, structural-key hash)
- Create: `value/keys_test.go`
- Create: `value/order.go` (`CoreDeterministic`, `LengthFirst` comparators)
- Create: `value/order_test.go`
- Create: `value/doc.go`

**Step 1: Write the failing fidelity tests**

`value/value_test.go` pins the data-model LLD: `Float16`/`Float32`/`Float64` keep wire bits (`NaN` payloads, `-0.0`, signaling bits round-trip through encode/decode helpers); `Uint` exact to `2^64-1`; `NegInt` exact over the full CBOR negative range `-1`..`-2^64` — stored as its `uint64` magnitude `n` with mathematical value `-1-n`, so the `-2^64` endpoint (`n = 2^64-1`, wire `3b ffffffffffffffff`) is representable even though no `int64` holds it; conversion to `float64` is a separate explicit function (`AsFloat64`) and is lossy above `2^53` — test that `Uint(2^63+1).AsFloat64()` is documented-lossy, never silent-exact, and that `NegInt` with magnitude `2^64-1` converts via `-1 - float64(n)`, matching the shipped decoder.

Run: `go test ./value/`
Expected: FAIL (types do not exist yet).

**Step 2: Implement the value model**

`value.go` per the LLD: kind-tagged `Value`; the full simple-value space — numeric `Simple` for values `0`–`19` (short form) and `32`–`255` (`0xf8` form), named `False`/`True`/`Null`/`Undefined` constants for 20–23, `24`–`31` reserved, `break` (`ai 31`) never a simple value; `Tag{Number uint64, Value Value}`; ordered `Map []KeyValue`. Include `AsFloat64` and a `String()` only if the diag task needs it later (YAGNI: skip `String()` until Task 9).

**Step 3: Run the tests**

Run: `go test ./value/`
Expected: PASS.

**Step 4: Write the failing key/order tests**

`keys_test.go`: bytes key `h'00'` equals bytes key `h'00'` and differs from `h'0000'`; float key `0xf9 3c00` equals `0xfa 3f800000` (both `1.0`) under canonical-encoding equality, and `NaN` keys are stable (each NaN key equals itself under canonical encoding); `Undefined` is a direct comparable key like the other named constants; tag keys classify by their tagged value: `Tag{1, "a"}` equals `Tag{1, "a"}` and differs from `Tag{2, "a"}` and `Tag{1, "b"}`, a tag of bytes is a valid key, and a tag of an array is rejected by default and accepted under `StructuralKeys` (canonical-encoding equality, consistent with duplicate detection); structural keys rejected by default, enabled by a mode flag, and then `[]Value{1,2}` ≠ `[]Value{1,2,0}`.

`order_test.go`: the `CoreDeterministic` comparator is bytewise lexicographic over the **full encoded keys — head and body** (RFC 8949 §4.2.1); the `LengthFirst` comparator is length-first then bytewise (RFC 8949 §4.2.3 legacy, the RFC 7049 §3.9 "Canonical CBOR" rule). Text keys of differing encoded lengths prove the head participates: `"z"` (`61 7a`) vs `"aa"` (`62 61 61`) — both RFC modes put `"z"` first (head 0x61 < 0x62 / shorter), while `sort.Strings` puts `"aa"` first; that contrast is the adapter-mode boundary, pinned again in Task 10. A pair that separates the two modes: `h'ff'` vs `h'0000'` — bytewise sorts `h'0000'` first (0x00 < 0xff); length-first sorts `h'ff'` first (shorter). Same-length `h'ff'` vs `h'00'` is bytewise in both.

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

### Task 4: Streaming decoder — `internal/codec`

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

Commit `testdata/rfc8949/` first (a `testdata` directory inside `internal/codec`): every appendix A item as `(hex bytes, kind, expected value)` triples — definite and indefinite arrays/maps, **indefinite byte/text strings** (`5f … ff`, `7f … ff`), all integer widths **including `3b ffffffffffffffff` → `-18446744073709551616` (`-2^64`)**, all float widths, tags, the **full simple-value space** (`0xe0`–`0xf3` → values 0–19, `0xf8 20` → `simple(32)`, named 20–23, `0xf8` with a payload below 32 → malformed), `break`, reserved `ai` 28–30.

`decoder_test.go`: for each vector, decode and compare against the value-model expectation; `break` outside an indefinite container → `ErrMalformed`; truncated vector → `ErrTruncated`; indefinite byte/text decodes to the concatenated bytes/text — bytes raw, text with the **concatenation** validated as UTF-8 (`simd.ValidUTF8` on the result); adapter-mode decode of a non-string-key map → the adapter's error (see Task 10), decode in `Keep`-key mode succeeds with a `Bytes` key.

Run: `go test ./internal/codec/`
Expected: FAIL (no decoder yet).

**Step 2: Implement the decoder**

Per `docs/lld/decoder.md`, in this order: head→argument (reserved `ai` and `ai 31` rejection), definite containers with pre-flight bounds and presize cap, indefinite containers with the break state machine, **indefinite byte/text chunked concatenation with UTF-8 validation of the result**, the full simple-value model (numeric `Simple` values 0–19 and 32–255, named 20–23, `0xf8` with payload < 32 malformed, `break` terminator-only), tags (`Keep` mode; `Discard` mode is Task 10's adapter), `Limits` enforcement at framing, ownership (borrowed `RawMessage`-shaped accessor, copied string/bytes). Depth default 64. No `float64` widening: values are exact per the value model.

**Step 3: Run the tests**

Run: `go test ./internal/codec/`
Expected: PASS.

**Step 4: Write the failing Skip-parity tests — the Stage 0 assertion work**

This task lands the corpus and assertion work that Stage 0 (Task 1) deliberately deferred:

- carry the head-byte enumeration test (from Task 1's `skip_test.go`, extended): for every head byte and the `0xf8`+byte form, decoder and `Skip` agree on accept/reject and span;
- extend the generative corpus (the pattern from the shipped `skip_test.go`) so the generator emits the full simple-value space — values 0–19, `0xf8` forms 32–255, named 20–23 — plus indefinite containers, indefinite byte/text strings, and tag keys (tag of a scalar, tag of bytes, tag of a structural value under `StructuralKeys`);
- repair the random-bytes loop: **assert** both errors agree (`if (uerr == nil) != (serr == nil) { fail }`) instead of discarding them to `_`.

Run: `go test ./internal/codec/ -run 'TestSkipParity|TestSkipAgreesWithUnmarshalOnEveryHead'`
Expected: FAIL.

**Step 5: Implement `Skip` on the same machine**

`internal/codec/skip.go` — the build step removed, the framing step shared, accept policy shared by construction with the decoder. This is the shipped invariant, now actually enforced: a skip that succeeds is an item the decoder consumes, same span.

**Step 6: Run the tests**

Run: `go test ./internal/codec/ && go test ./...`
Expected: PASS, PASS (the shipped suite, including the Task 1 fixes, stays green).

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
git commit -m "feat(codec): streaming decoder with indefinite forms, simple values, limits, skip parity"
```

---

### Task 5: Streaming encoder — `internal/codec`

**Files:**
- Create: `internal/codec/encoder.go`
- Create: `internal/codec/encoder_test.go`

**Step 1: Write the failing round-trip tests**

`encoder_test.go`:
- every RFC vector decodes with the Task 4 decoder, encodes, and re-decodes to an equal value;
- `StartArray(n)`/`EndArray` and indefinite forms emit exactly the
  documented bytes (`9f ... ff`);
- indefinite byte/text strings (`5f … ff`, `7f … ff`) emit chunk-wise and
  re-decode to the concatenated value (text: concatenation UTF-8-valid);
- the container stack rejects: `End` on an empty stack, `EndArray` over a map, overrunning a definite count — before any bytes are written;
- `WriteTag(55799)` emits `d9 d9f7` then the value; no tag is emitted unless written;
- the full simple-value space encodes: `Simple(0)` → `e0`, `Simple(32)` → `f8 20`, named constants → `f4`–`f7`, `break` never emitted by the encoder except via `End*`.

Run: `go test ./internal/codec/ -run TestEncoder`
Expected: FAIL.

**Step 2: Implement the encoder**

Per `docs/lld/encoder.md`: forward append only, shortest heads, container stack, `WriteTag`, indefinite via explicit `StartIndefinite*`/`End*` (the only `0xff` writers), chunk-wise indefinite byte/text emission (`5f`/`7f`) for streamed data, and the same simple-value policy as the decoder. Float rule for now: adapter-shaped (`float32`-if-round-trips, else `float64`); the `float16` extension arrives with the modes in Task 6.

**Step 3: Run the tests**

Run: `go test ./internal/codec/ && go vet ./internal/codec/`
Expected: PASS, PASS.

**Step 4: Write the failing interop test**

`encoder_test.go`: encode values from the shipped generator corpus, decode with fxamacker (`cbor.Unmarshal` into `any` where the value model maps onto JSON shapes; into fxamacker's `cbor.Tag`/`RawTag` types otherwise), and require equality per the data model's canonical-encoding equality.

Run: `go test ./internal/codec/ -run TestInteropFxA`
Expected: FAIL.

**Step 5: Fix to green**

Expected: PASS. Where fxamacker and the design legitimately differ (duplicate policy defaults, tag interpretation, NaN handling), pin the difference with a comment-carrying test — the oracle is the RFC, not fxamacker.

**Step 6: Commit**

```bash
git add internal/codec/
git commit -m "feat(codec): streaming encoder, container stack, fxamacker interop"
```

---

### Task 6: Deterministic modes

**Files:**
- Modify: `internal/codec/encoder.go` (mode plumbing, float16 rule, key ordering)
- Create: `internal/codec/modes.go`
- Create: `internal/codec/modes_test.go`
- Modify: `internal/codec/decoder.go` (duplicate detection via canonical key equality)

**Step 1: Write the failing mode tests**

`modes_test.go`:
- mode names are the data-model LLD's, unambiguous: `CoreDeterministic` (RFC 8949 §4.2.1, bytewise over the full encoded keys — head and body) and `LengthFirst` (RFC 8949 §4.2.3 legacy, length-first then bytewise);
- `CoreDeterministic` sorts encoded-bytewise (`h'0000'` before `h'ff'`; text `"z"` → `61 7a` before `"aa"` → `62 61 61`); `LengthFirst` sorts `h'ff'` before `h'0000'` (shorter first);
- both modes' float rule: `1.0` encodes `f9 3c00`, `1.5` encodes `f9 3e00`, a value that does not round-trip through `float16` encodes `fa`/`fb`; the adapter mode (Task 10) never emits `f9`;
- duplicate policies: `Error` → `ErrDuplicateKey` on the second canonical-equal key; `FirstWins`/`LastWins` pin their map results; `0xf9 3c00` and `0xfa 3f800000` are the same key under every policy; tag keys are canonical-equal iff number and tagged value are (a duplicate `Tag{1, "a"}` key is caught under `Error`);

Run: `go test ./internal/codec/ -run 'TestModes|TestDuplicates'`
Expected: FAIL.

**Step 2: Implement modes**

Per the data-model and encoder LLDs: a `Mode` enum on the encoder (adapter/core-deterministic/length-first) and a `DuplicatePolicy` on the decoder; the float16 forward conversion (the inverse of `halfToFloat32bits` — the decode side already exists in `decode.go`); key ordering via `value/order.go` comparators.

**Step 3: Run the tests**

Run: `go test ./internal/codec/ && go test ./value/`
Expected: PASS, PASS.

**Step 4: Write the failing profile tests — interop paired correctly**

`modes_test.go`, against the installed fxamacker v2.9.2 (verified against its `encode.go`: `SortCoreDeterministic` = `SortBytewiseLexical`; `SortCanonical` = `SortLengthFirst`):

- `CoreDetEncOptions()` (bytewise) decodes our `CoreDeterministic`-mode bytes, and vice versa;
- `CanonicalEncOptions()` (length-first — fxamacker's "canonical" is the legacy RFC 7049 §3.9 ordering, not bytewise) decodes our `LengthFirst`-mode bytes, and vice versa;
- caveat pinned by test: fxamacker's deterministic options also force `ShortestFloat16` and normalize NaN to `0xf97e00` and Inf to float16; our modes preserve NaN payloads per the data-model LLD — the difference is a pinned profile test with the reason in a comment;
- a duplicate-key input decodes under each policy to the documented result/error.

Run: `go test ./internal/codec/ -run TestProfiles`
Expected: PASS.

**Step 5: Run and fix to green**

Run: `go test ./internal/codec/ -run TestProfiles`
Expected: PASS.

**Step 6: Commit**

```bash
git add internal/codec/
git commit -m "feat(codec): core-deterministic and length-first modes, duplicate policies"
```

---

### Task 7: Tags

**Files:**
- Modify: `internal/codec/decoder.go`, `internal/codec/encoder.go` (interpret mode)
- Create: `internal/codec/tags.go`
- Create: `internal/codec/tags_test.go`

**Step 1: Write the failing tag tests**

`tags_test.go`:
- `Keep` mode: `c0 74` + the 20-byte date-time string decodes to `Tag{0, Text}` and re-encodes to the same bytes;
- `Interpret` mode (opt-in): tags 0/1/2/3/4/5/32/33/34/36/55799 map to the documented native forms (epoch float, bignum via `math/big`, etc.); unknown tags stay generic;
- adapter mode (`Discard`) is Task 10's behavior, pinned here as the mode's default off.

Run: `go test ./internal/codec/ -run TestTags`
Expected: FAIL.

**Step 2: Implement tag modes**

Generic `Tag` storage already exists from Task 3/4; this task adds the interpret table and the mode plumbing. YAGNI: only the tags listed in the data-model LLD get native forms.

**Step 3: Run the tests**

Run: `go test ./internal/codec/ && go vet ./internal/codec/`
Expected: PASS, PASS.

**Step 4: Commit**

```bash
git add internal/codec/
git commit -m "feat(codec): tag keep/interpret modes"
```

---

### Task 8: Lazy values

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

Per `docs/lld/streaming-lazy-and-diagnostic.md`. The frame pass is the Task 4 machine with the build step replaced by range capture.

**Step 3: Run the tests**

Run: `go test ./internal/codec/`
Expected: PASS.

**Step 4: Measure the filter-then-read win and record it**

Run: `go test -run '^$' -bench . -benchmem -count=6 -shuffle=on ./internal/codec/` (one process, machine quiet).
Compare the lazy filter against the shipped decode-all and skip-then-decode benchmarks (the shipped `bench_test.go` figures: decode-all 662 us vs skip-filter 79 us on the 2,000-record stream). If lazy values deliver the predicted allocation drop, record the delta in `docs/wrong.md`'s lazy-values entry as the superseding measurement; if they do not, record **that** finding — the entry exists precisely to hold whichever measurement arrives.

**Current note (2026-08-26):** the figures above are the historical measurement
the task was written against. The later committed `testdata/bench.txt` baseline
recorded different minima under load 4.82, so neither record substitutes for a
fresh quiet-host run.

**Step 5: Commit**

```bash
git add internal/codec/
git commit -m "feat(codec): lazy values as framed byte ranges"
```

---

### Task 9: Diagnostic notation — `simdcbor/diag`

**Files:**
- Create: `diag/diag.go` (render)
- Create: `diag/parse.go`
- Create: `diag/diag_test.go`
- Create: `diag/doc.go`

**Step 1: Write the failing notation tests**

`diag_test.go`, per the LLD table (exact wire examples): `00`→`0`, `20`→`-1`, `3b ffffffffffffffff`→`-18446744073709551616`, `42 01 02`→`h'0102'` (the head is `0x42`, length 2 — not `0x40`), `61 61`→`"a"`, `f4`/`f5`/`f6`/`f7`, `f9 3c00`→`1.0`, `e0`→`simple(0)`, `f8 20`→`simple(32)`, `80`, `9f 00 ff`→`[_ 0]`, `5f 42 01 02 ff`→`(_ h'0102')`, `7f 61 61 61 62 ff`→`(_ "ab")`, `bf ... ff`→`{_ ...}`, and `c0 74 32 30 31 33 2d 30 33 2d 32 31 54 32 30 3a 30 34 3a 30 30 5a`→`0("2013-03-21T20:04:00Z")` (tag 0 of the 20-byte date-time string — `0x74`, not `0x78 0x18`). Floats render as the shortest round-tripping decimal, with integral float values keeping a trailing `.0` (`1.0`, not `1`) to preserve the float/int distinction; nonfinite values spell exactly `NaN`, `Infinity`, `-Infinity` — capitals per RFC 8949 §8, and the tests pin the exact spelling. Parse side round-trips through the value model. Error-prefix rendering: a truncated item renders the well-formed prefix in notation.

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

### Task 10: Adapter — the shipped API over the new core

**Files:**
- Modify: `decode.go`, `encode.go`, `skip.go` (reimplement as adapters over `internal/codec`)
- Modify: `decode_test.go`, `encode_test.go`, `skip_test.go` (unchanged assertions must pass untouched)
- Create: `adapter_test.go` (byte-identity and boundary parity)

**Step 1: Write the failing adapter tests**

`adapter_test.go`:
- **byte identity**: on the shipped corpus, `Marshal` produces the exact bytes the pre-adapter encoder produced (snapshot a corpus of encoded outputs into `testdata/adapter/` at this task's start, before reimplementing);
- **shape identity**: `Unmarshal` returns the JSON shapes (`map[string]any`/`[]any`/`float64`), tags discarded, duplicates last-wins, depth 64 → `ErrMalformed`, non-string key → `ErrMalformed`, `undefined` → `nil`;
- **boundary parity**: `Skip` and `Unmarshal` agree on accept/reject and span on the new core — the head-byte enumeration test and the asserted random-input loop (Tasks 1 and 4) keep this true by construction; the Stage 0 divergence must not reappear;
- the shipped `decode_test.go`/`encode_test.go`/`skip_test.go` files pass **without modification**.

Run: `go test ./...`
Expected: FAIL where the adapter does not yet exist.

**Step 2: Reimplement the root package as the adapter**

`Unmarshal` → configure the new decoder with adapter mode (JSON shapes, `float64` conversion via the value model's `AsFloat64`, `Discard` tags, `LastWins`, depth 64 with `ErrDepth`→`ErrMalformed` mapping, `ErrLimit`/`ErrUnsupportedKey`→`ErrMalformed`, simple values outside the JSON shape rejected — consistent with `Skip`, per Stage 0). `Marshal` → adapter mode encoder, byte-identical output (including: no `float16`, `NaN` as `fb` double, `uint` still unsupported, `[]byte`→bytes, sorted bytewise). `Skip` → the new core's skip with adapter limits. The two error values stay the only exported errors from the root package.

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

### Task 11: Final gates and records

**Files:**
- Modify: `README.md` (shipped API + subset unchanged; add links to the new packages and docs; the Skip caveat becomes "resolved in Stage 0")
- Modify: `docs/verification.md` (mark the full-codec gates as live)
- Modify: `docs/wrong.md` (add any measurements this work produced; close the Skip-divergence entry)

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
make bench-check   # now pipefail-safe after Task 2
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

`README.md`: shipped API section unchanged in substance; add links to `simdcbor/value`, `simdcbor/diag`, `internal/codec` docs, and the design/plan; the Skip simple-value caveat sentence is replaced by the Stage 0 resolution. `docs/verification.md`: move the full-codec gates from target to live. `docs/wrong.md`: close the Skip-divergence entry with its resolution, add the lazy-value measurement from Task 8 and any other sourced findings. Check every internal link resolves.

**Step 6: Commit**

```bash
git add README.md docs/
git commit -m "docs: record full-codec gates, measurements, and links"
```

**Step 7: Report**

Report per the executing-plans protocol: each task's verification output, the SHA of the final commit, and the gate list with results. Do not push; do not tag; do not release.

---

## Production readiness ledger

R1 (Tasks 0-11) builds the codec; R2 is the round that makes it
shippable as v1. This ledger is the local authority for R2 work: this
repository's docs are the tracker, and nothing outside the repository
may close, reject, renumber, or reorder a row. R1 above is historical
and untouched by R2.

- **Stable IDs.** Each ID (`CBOR-V1-NN`) is a stable reference
  key, not an ordering, priority, or canonical sequence. Rows may be
  executed in any order that respects their dependencies; the ID never
  changes and never implies rank.
- **One ID per item.** An ID is issued once and refers to exactly one
  work item. A closed or rejected ID is never reused.
- **One task at a time.** A session touching implementation work names its
  ledger ID in its first message; without one it touches no implementation
  files.
- **Noncanonical family index.** The index at
  `GO_SIMD/docs/plans/2026-08-24-simd-family-production-readiness.md` is a link
  collection and never overrides this ledger or duplicates its task status.
- **States.** A row is in exactly one of `open`, `staged`,
  `in-progress`, `blocked`, `evidence-complete`, `shipped`, `rejected`.
  Every transition is an edit in the ledger backed by recorded
  evidence; `shipped` creates or updates CHANGELOG.md, and `rejected`
  records its measurement in `docs/wrong.md` with the reopen condition
  there. `rejected` is terminal without a documented reopen condition;
  rejection is recorded with its evidence, never a removal.
- **Timed bare gates.** Every gate a row runs is bare - never piped
  through `tail`/`tee` without `pipefail` - and carries an explicit
  timeout.

The oracle is RFC 8949: appendix A vectors are what behavior is checked
against. fxamacker/cbor, QCBOR, TinyCBOR, serde_cbor, and ciborium are
peers, not oracles: a row compares against a library only where the row
says so explicitly, and a difference from a library is resolved by the
RFC's text, not by matching the library. Evidence is concrete: a matrix
of vectors, indefinite forms, and round-trip shapes against the RFC, or
a sourced measurement. No invented figures.

| ID | state | work | evidence | exit |
|---|---|---|---|---|
| CBOR-V1-01 | open | Byte-string representation: pin how byte strings appear in the value model and the adapter - the value kind, the adapter's decode shape, and the marshal type set - and fix the contract in the docs that pin the subset. | Matrix of RFC appendix A byte-string vectors through the value model and the adapter, including indefinite `5f` forms; round-trip shapes for every adapter type. | Representation contract fixed in the code and in the docs that pin it; adapter and value-model paths agree on the matrix. |
| CBOR-V1-02 | open | Public limits and typed errors: export the codec's limit configuration and error taxonomy from the root package instead of the two blanket values, with the adapter's mapping pinned. | Vector matrix of limit violations (depth, presize, truncation) asserting the exact exported error each produces; sourced docs of the mapping. | Exported limits and typed errors usable without loss of adapter compatibility; every vector's error asserted by test. |
| CBOR-V1-03 | open | Marshal adapter and the dead path: reimplement `Marshal` over the streaming encoder, pin byte identity against the shipped encoder's output, then delete the superseded encoder path. | Byte-identity snapshot corpus of shipped `Marshal` outputs taken before the swap; the snapshot re-checks after the swap; round-trip matrix through the new encoder. | Adapter `Marshal` byte-identical to the snapshot; dead path deleted; full suite green. |
| CBOR-V1-04 | open | RawNext UTF-8: raw text items surfaced without materialization must still be validated as UTF-8 at framing, so a raw `Next` cannot hand out text the decoder would reject. | Matrix of UTF-8-valid and invalid text items (definite and indefinite) asserted at framing, not at materialization. | Every raw text item is UTF-8-validated at framing; no raw path accepts invalid UTF-8. |
| CBOR-V1-05 | open | Linear indefinite text: indefinite text concatenation must validate in one linear pass over the chunks, not per-chunk revalidation or repeated copying. | Round-trip matrix of multi-chunk indefinite text; a sourced benchmark of a many-chunk item asserting linear time per the verification rules. | Many-chunk indefinite text decodes in linear time with one validation pass; the benchmark is recorded, or the finding goes to `docs/wrong.md`. |
| CBOR-V1-06 | open | Repeated `0, nil` progress: the streaming `Next` must make progress on every call - an empty item or a zero-length record may not be reported as `0, nil` twice, which would hang a sequence loop. | Sequence matrix of empty items, zero-length items, and concatenated records asserting every `Next` advances or errors. | `Next` never returns `0, nil` twice; a progress invariant test is green under `-race`. |
| CBOR-V1-07 | open | Diagnostics and documentation: finish `simdcbor/diag` error prefixes and write the release documentation the v1 surface needs (limits, errors, modes, adapter contract). | Notation round-trip matrix through the value model; the Task 9 diag vectors stay green; every doc claim checked against code. | Diagnostics shipped and documented; no doc claim contradicts the code. |
| CBOR-V1-08 | open | Gates and first release: run the full matrix - RFC vectors, interop, `-race`, fuzz, cross-arch builds, benchmarks - and prepare the first v1 release; tag/publish operations stop at the evidence checkpoint until separately authorized. | The full gate list run bare and timed per `docs/verification.md`, plus the vector and interop matrices the rows above produced. | All gates green and release identity prepared; v1 tag/publish only when separately authorized. |
| CBOR-V1-09 | open | Workload decisions: measure the codec against fxamacker/cbor, QCBOR, TinyCBOR, serde_cbor, and ciborium per workload, and record where it is competitive, where it is not, and what is a non-goal. | Interleaved A/B sweep per the benchmark rules, minimum of three, machine quiet, sourced in `docs/wrong.md` or a workload matrix doc. | A sourced workload matrix exists; each row's decision (optimize, document, non-goal) is recorded; no claim without its measurement. |

CBOR-V1-07 explicitly includes the stale package comment and lazy-value note
in `decode.go`: replace the two-stage/copy/hash description with the one-walk
architecture and point the lazy-value rationale at the completed measurement
in `docs/wrong.md`. Wave 0 is documentation-only and does not modify Go source.

### Workload matrix (no numbers)

RFC 8949 is the oracle for wire behavior. The libraries are peers except in a
separately declared interop promise. Every row is measured on identical bytes
and records peer versions; no feature count or borrowed result settles it.

| workload | this repo | peers | oracle-or-basis | gate |
|---|---|---|---|---|
| RFC appendix A scalar, string and container vectors | root adapter and value model | fxamacker/cbor, QCBOR, TinyCBOR, serde_cbor, ciborium | RFC 8949 | vector and round-trip matrix |
| definite and indefinite byte/text strings, including many-chunk text | streaming decoder, `RawNext`, adapter | same peers where the shape is exposed | RFC 8949 framing and UTF-8 rules | UTF-8, linear-time and allocation evidence |
| concatenated sequence decode with empty and zero-length items | streaming `Next` path | same peers where sequence APIs exist | RFC 8949 sequence framing; local progress contract | progress matrix and `-race` |
| encode and marshal over every supported adapter type | streaming encoder and `Marshal` | same peers | RFC-valid bytes; pre-swap snapshot only for this repo's byte-identity promise | round-trip and byte-identity corpus |
| deep, large and truncated inputs at configured limits | decoder, raw path and adapter | same peers on equivalent configured limits | this repo's documented resource contract | limit/error matrix, fuzz and `GOMEMLIMIT` |
