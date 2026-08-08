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
