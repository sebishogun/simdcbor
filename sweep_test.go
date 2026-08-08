package simdcbor

import (
	"math/rand"
	"testing"

	"github.com/fxamacker/cbor/v2"
)

func buildShape(kind string, rng *rand.Rand) any {
	switch kind {
	case "strings":
		m := map[string]any{}
		for i := 0; i < 20; i++ {
			m[randStr(rng)] = randStr(rng)
		}
		return m
	case "numbers":
		s := make([]any, 200)
		for i := range s {
			s[i] = float64(rng.Intn(1 << 20))
		}
		return s
	case "deep":
		var v any = "leaf"
		for i := 0; i < 40; i++ {
			v = []any{v}
		}
		return v
	case "hugearray":
		s := make([]any, 5000)
		for i := range s {
			s[i] = float64(i)
		}
		return s
	}
	return nil
}

func BenchmarkSweep(b *testing.B) {
	rng := rand.New(rand.NewSource(1))
	dm, _ := cbor.DecOptions{}.DecMode()
	for _, kind := range []string{"strings", "numbers", "deep", "hugearray"} {
		enc, _ := cbor.Marshal(buildShape(kind, rng))
		b.Run("simdcbor/"+kind, func(b *testing.B) {
			b.SetBytes(int64(len(enc)))
			for b.Loop() {
				if _, _, err := Unmarshal(enc); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run("fxamacker/"+kind, func(b *testing.B) {
			b.SetBytes(int64(len(enc)))
			for b.Loop() {
				var v any
				if err := dm.Unmarshal(enc, &v); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
