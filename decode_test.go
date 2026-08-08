package simdcbor

import (
	"encoding/json"
	"math"
	"math/rand"
	"reflect"
	"testing"

	"github.com/fxamacker/cbor/v2"
)

// The oracle is fxamacker/cbor for the bytes and encoding/json for the
// shape: encode a value to CBOR with the reference encoder, decode it
// here, and require the JSON-shaped result to match what the same value
// decodes to through encoding/json.
func TestDecodeAgainstReference(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	var build func(d int) any
	build = func(d int) any {
		switch k := rng.Intn(8); {
		case k == 0 && d < 4:
			n := rng.Intn(5)
			m := map[string]any{}
			for i := 0; i < n; i++ {
				m[randStr(rng)] = build(d + 1)
			}
			return m
		case k == 1 && d < 4:
			n := rng.Intn(6)
			s := make([]any, n)
			for i := range s {
				s[i] = build(d + 1)
			}
			return s
		case k == 2:
			return randStr(rng)
		case k == 3:
			return float64(rng.Intn(1 << 20))
		case k == 4:
			return rng.Intn(2) == 0
		default:
			return nil
		}
	}
	for i := 0; i < 3000; i++ {
		v := build(0)
		enc, err := cbor.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		got, n, err := Unmarshal(enc)
		if err != nil {
			t.Fatalf("value %#v: %v", v, err)
		}
		if n != len(enc) {
			t.Fatalf("consumed %d of %d", n, len(enc))
		}
		// Normalize v through encoding/json to get the same shape ours
		// produces (numbers as float64, etc).
		jb, _ := json.Marshal(v)
		var want any
		json.Unmarshal(jb, &want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("value %#v:\n got %#v\nwant %#v", v, got, want)
		}
	}
	// Truncations never panic and always error.
	full, _ := cbor.Marshal(map[string]any{"a": []any{1.0, 2.0, "three"}})
	for i := 0; i < len(full); i++ {
		if _, _, err := Unmarshal(full[:i]); err == nil {
			t.Fatalf("prefix %d accepted", i)
		}
	}
	// Random bytes never panic.
	for i := 0; i < 5000; i++ {
		b := make([]byte, rng.Intn(40))
		rng.Read(b)
		_, _, _ = Unmarshal(b)
	}
	// Floats: half, single, double round-trip to float64.
	for _, f := range []float64{0, 1, -1, 0.5, math.Pi, 1e10, -2.5} {
		enc, _ := cbor.Marshal(f)
		got, _, err := Unmarshal(enc)
		if err != nil {
			t.Fatal(err)
		}
		if got.(float64) != f {
			t.Fatalf("float %v decoded %v", f, got)
		}
	}
}

func randStr(rng *rand.Rand) string {
	n := rng.Intn(12)
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + rng.Intn(26))
	}
	return string(b)
}
