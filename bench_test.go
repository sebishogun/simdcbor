package simdcbor

import (
	"testing"

	"github.com/fxamacker/cbor/v2"
)

func BenchmarkUnmarshal(b *testing.B) {
	doc := map[string]any{
		"id": 12345.0, "name": "a realistic record", "active": true,
		"tags": []any{"x", "y", "z"}, "score": 98.6,
		"nested": map[string]any{"a": 1.0, "b": 2.0, "c": []any{3.0, 4.0, 5.0}},
	}
	enc, _ := cbor.Marshal(doc)
	dm, _ := cbor.DecOptions{}.DecMode()
	b.SetBytes(int64(len(enc)))
	b.Run("simdcbor", func(b *testing.B) {
		for b.Loop() {
			if _, _, err := Unmarshal(enc); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("fxamacker", func(b *testing.B) {
		for b.Loop() {
			var v any
			if err := dm.Unmarshal(enc, &v); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// The filtering shape: a stream of records, decode only the few that
// match a cheap predicate on the first field, Skip the rest. Skip is
// allocation-free, so the win grows as the match rate falls.
func BenchmarkFilterStream(b *testing.B) {
	var stream []byte
	for i := 0; i < 2000; i++ {
		rec, _ := marshalRecord(i)
		stream = append(stream, rec...)
	}
	b.Run("skip-then-decode-1pct", func(b *testing.B) {
		b.SetBytes(int64(len(stream)))
		for b.Loop() {
			i, rec, matched := 0, 0, 0
			for i < len(stream) {
				// One record in a hundred matches; the rest are skipped
				// without ever building a map. Real filters test a field,
				// but the cost that matters is decode-vs-skip, held here at
				// a 1% decode rate.
				if rec%100 == 0 {
					_, n, _ := Unmarshal(stream[i:])
					i += n
					matched++
				} else {
					n, _ := Skip(stream[i:])
					i += n
				}
				rec++
			}
			sinkI = matched
		}
	})
	b.Run("decode-all", func(b *testing.B) {
		b.SetBytes(int64(len(stream)))
		for b.Loop() {
			i := 0
			for i < len(stream) {
				_, n, _ := Unmarshal(stream[i:])
				i += n
			}
		}
	})
}

var sinkI int

func marshalRecord(i int) ([]byte, error) {
	return cbor.Marshal(map[string]any{
		"id": i, "name": "record", "score": float64(i) * 1.5,
		"tags": []any{"a", "b"},
	})
}
