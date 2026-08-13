package codec

import (
	"bytes"
	"encoding/hex"
	"math"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/sebishogun/simdcbor/value"
)

// Interop against fxamacker/cbor. The oracle is the RFC, not the other
// library: where the two legitimately differ, the difference is pinned with
// the reason rather than conceded.

// What this encoder writes, fxamacker must read back to the same thing.
func TestInteropFxAReadsWhatWeWrite(t *testing.T) {
	for _, c := range []struct {
		name string
		v    value.Value
		want any
	}{
		{"uint", value.FromUint(1000000), uint64(1000000)},
		{"uint max", value.FromUint(math.MaxUint64), uint64(math.MaxUint64)},
		{"negint", value.FromInt(-100), int64(-100)},
		{"bytes", value.FromBytes([]byte{1, 2, 3, 4}), []byte{1, 2, 3, 4}},
		{"text", value.FromText("IETF"), "IETF"},
		{"text with a 4-byte rune", value.FromText("𐅑"), "𐅑"},
		{"true", value.True, true},
		{"false", value.False, false},
		{"null", value.Null, nil},
		{"array", value.FromArray(value.FromUint(1), value.FromUint(2)), []any{uint64(1), uint64(2)}},
		{"map", value.FromMap(value.KeyValue{Key: value.FromText("a"), Value: value.FromUint(1)}),
			map[any]any{"a": uint64(1)}},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := encodeValue(t, c.v)
			var got any
			if err := cbor.Unmarshal(b, &got); err != nil {
				t.Fatalf("fxamacker could not read %x: %v", b, err)
			}
			if !equalAny(got, c.want) {
				t.Fatalf("fxamacker read %#v, want %#v (bytes %x)", got, c.want, b)
			}
		})
	}
}

// And the reverse: what fxamacker writes, this decoder must read.
func TestInteropWeReadWhatFxAWrites(t *testing.T) {
	for _, c := range []struct {
		name string
		v    any
		kind value.Kind
	}{
		{"uint", uint64(1000000), value.Uint},
		{"negint", int64(-100), value.NegInt},
		{"bytes", []byte{1, 2, 3}, value.Bytes},
		{"text", "IETF", value.Text},
		{"bool", true, value.SimpleKind},
		{"nil", nil, value.SimpleKind},
		{"float", 3.14, value.Float64},
		{"array", []any{1, 2, 3}, value.Array},
		{"map", map[string]int{"a": 1}, value.Map},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, err := cbor.Marshal(c.v)
			if err != nil {
				t.Fatal(err)
			}
			v, err := New(b, Limits{}).Decode()
			if err != nil {
				t.Fatalf("we could not read fxamacker's %x: %v", b, err)
			}
			if v.Kind() != c.kind {
				t.Fatalf("kind %v, want %v (bytes %x)", v.Kind(), c.kind, b)
			}
		})
	}
}

// The RFC vectors, encoded by both, must agree byte for byte where both claim
// preferred serialization.
func TestInteropAgreesOnCanonicalIntegers(t *testing.T) {
	em, err := cbor.CanonicalEncOptions().EncMode()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []uint64{0, 1, 23, 24, 255, 256, 65535, 65536, 1 << 32, math.MaxUint64} {
		theirs, err := em.Marshal(n)
		if err != nil {
			t.Fatal(err)
		}
		e := NewEncoder(nil)
		must(t, e.WriteUint(n))
		ours, _ := e.Bytes()
		if !bytes.Equal(ours, theirs) {
			t.Errorf("uint %d: ours %x, fxamacker %x", n, ours, theirs)
		}
	}
}

// Where the two differ, and why. These are pinned so a change to either side
// is noticed rather than absorbed.
func TestInteropDocumentedDifferences(t *testing.T) {
	t.Run("simple values outside the named four", func(t *testing.T) {
		// RFC 8949 section 3.3: simple values 0-19 and 32-255 are well-formed.
		// This package represents them; fxamacker decodes them into its own
		// SimpleValue type rather than any of the Go primitives, so "equal" is
		// only meaningful through that type.
		v, _ := value.FromSimple(32)
		b := encodeValue(t, v)
		if hex.EncodeToString(b) != "f820" {
			t.Fatalf("simple(32) encoded as %x", b)
		}
		var sv cbor.SimpleValue
		if err := cbor.Unmarshal(b, &sv); err != nil {
			t.Fatalf("fxamacker could not read simple(32): %v", err)
		}
		if sv != cbor.SimpleValue(32) {
			t.Fatalf("fxamacker read simple(%d)", sv)
		}
	})
	t.Run("the -2^64 endpoint", func(t *testing.T) {
		// No int64 holds it, so fxamacker surfaces it as big.Int rather than
		// an integer. Both are reading the same bytes correctly; only the Go
		// type differs, which is why this package keeps the magnitude.
		v := value.FromNegMagnitude(math.MaxUint64)
		b := encodeValue(t, v)
		if hex.EncodeToString(b) != "3bffffffffffffffff" {
			t.Fatalf("encoded as %x", b)
		}
		var got any
		if err := cbor.Unmarshal(b, &got); err != nil {
			t.Fatalf("fxamacker could not read -2^64: %v", err)
		}
		if _, isInt := got.(int64); isInt {
			t.Fatal("fxamacker returned an int64 for -2^64, which cannot hold it")
		}
	})
	t.Run("float width is preserved rather than normalized", func(t *testing.T) {
		// A half-precision 1.0 stays a half here. fxamacker's default decode
		// widens to float64, which is a value-model choice, not a disagreement
		// about the bytes.
		v := value.FromFloat16Bits(0x3c00)
		b := encodeValue(t, v)
		if hex.EncodeToString(b) != "f93c00" {
			t.Fatalf("half 1.0 encoded as %x", b)
		}
		var f float64
		if err := cbor.Unmarshal(b, &f); err != nil {
			t.Fatalf("fxamacker could not read a half: %v", err)
		}
		if f != 1 {
			t.Fatalf("fxamacker read %v", f)
		}
	})
}

func equalAny(a, b any) bool {
	switch av := a.(type) {
	case []byte:
		bv, ok := b.([]byte)
		return ok && bytes.Equal(av, bv)
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !equalAny(av[i], bv[i]) {
				return false
			}
		}
		return true
	case map[any]any:
		bv, ok := b.(map[any]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, v := range av {
			if !equalAny(v, bv[k]) {
				return false
			}
		}
		return true
	}
	return a == b
}
