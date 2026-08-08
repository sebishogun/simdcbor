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
