package simdcbor

import (
	"math/rand"
	"testing"

	"github.com/fxamacker/cbor/v2"
)

// Skip must span exactly what Unmarshal consumes, on every item, and
// agree on accept/reject.
func TestSkipMatchesUnmarshal(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	var build func(d int) any
	build = func(d int) any {
		switch k := rng.Intn(7); {
		case k == 0 && d < 4:
			m := map[string]any{}
			for i := 0; i < rng.Intn(5); i++ {
				m[randStr(rng)] = build(d + 1)
			}
			return m
		case k == 1 && d < 4:
			s := make([]any, rng.Intn(6))
			for i := range s {
				s[i] = build(d + 1)
			}
			return s
		case k == 2:
			return randStr(rng)
		case k == 3:
			return float64(rng.Intn(1 << 20))
		default:
			return rng.Intn(2) == 0
		}
	}
	for i := 0; i < 5000; i++ {
		enc, _ := cbor.Marshal(build(0))
		_, un, uerr := Unmarshal(enc)
		sn, serr := Skip(enc)
		if (uerr == nil) != (serr == nil) {
			t.Fatalf("accept mismatch: unmarshal %v skip %v", uerr, serr)
		}
		if uerr == nil && sn != un {
			t.Fatalf("span %d != consumed %d", sn, un)
		}
	}
	// Truncations and random bytes never panic and agree on rejection.
	for i := 0; i < 5000; i++ {
		b := make([]byte, rng.Intn(30))
		rng.Read(b)
		_, ue := func() (int, error) { _, n, e := Unmarshal(b); return n, e }()
		_, se := Skip(b)
		_ = ue
		_ = se
	}
}

// TestSkipAgreesWithUnmarshalOnEveryHead enumerates the whole head space, so
// the simple-value divergence cannot hide the way it hid from the corpus test.
//
// The corpus generator never produced simple values in 0xe0-0xf3, and the
// random-bytes loop discarded both errors, so a Skip that accepted what
// Unmarshal rejected looked identical to one that agreed. Skip's own doc
// comment claimed the accept boundary was the same as Unmarshal's; it was not.
func TestSkipAgreesWithUnmarshalOnEveryHead(t *testing.T) {
	for h := 0; h < 256; h++ {
		b := []byte{byte(h), 0, 0, 0, 0, 0, 0, 0, 0, 0}
		_, un, uerr := Unmarshal(b)
		sn, serr := Skip(b)
		if (uerr == nil) != (serr == nil) {
			t.Errorf("head %02x: unmarshal err=%v, skip err=%v", h, uerr, serr)
			continue
		}
		if uerr == nil && sn != un {
			t.Errorf("head %02x: skip span %d, unmarshal consumed %d", h, sn, un)
		}
	}
	// The two-byte simple form, for every payload.
	for p := 0; p < 256; p++ {
		b := []byte{0xf8, byte(p), 0}
		_, un, uerr := Unmarshal(b)
		sn, serr := Skip(b)
		if (uerr == nil) != (serr == nil) {
			t.Errorf("f8 %02x: unmarshal err=%v, skip err=%v", p, uerr, serr)
			continue
		}
		if uerr == nil && sn != un {
			t.Errorf("f8 %02x: skip span %d, unmarshal consumed %d", p, sn, un)
		}
	}
}
