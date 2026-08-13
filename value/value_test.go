package value

import (
	"math"
	"testing"
)

// The exactness properties, one test per thing a decoder loses when it maps
// CBOR onto Go's built-in types.

func TestNegIntCoversTheFullRange(t *testing.T) {
	for _, c := range []struct {
		mag  uint64
		math string
	}{
		{0, "-1"},
		{99, "-100"},
		{math.MaxInt64, "-2^63"},
		{math.MaxUint64, "-2^64"}, // the endpoint no int64 holds
	} {
		v := FromNegMagnitude(c.mag)
		got, ok := v.AsNegMagnitude()
		if !ok || got != c.mag {
			t.Fatalf("%s: magnitude %d, want %d", c.math, got, c.mag)
		}
		// AsInt refuses rather than wrapping.
		i, ok := v.AsInt()
		wantFits := c.mag <= math.MaxInt64
		if ok != wantFits {
			t.Errorf("%s: AsInt ok=%v, want %v", c.math, ok, wantFits)
		}
		if ok && i != -1-int64(c.mag) {
			t.Errorf("%s: AsInt %d", c.math, i)
		}
	}
	// The endpoint converts to a float by the same law the decoder uses.
	f, _, ok := FromNegMagnitude(math.MaxUint64).AsFloat64()
	if !ok || f != -1-float64(uint64(math.MaxUint64)) {
		t.Fatalf("-2^64 as float64: %v", f)
	}
}

func TestUintToFloatIsLossyAndSaysSo(t *testing.T) {
	for _, c := range []struct {
		n     uint64
		exact bool
	}{
		{0, true},
		{1 << 52, true},
		{1<<53 - 1, true},
		{1 << 53, true},    // representable
		{1<<53 + 1, false}, // the first integer a float64 cannot hold
		{1<<63 + 1, false},
		{math.MaxUint64, false},
	} {
		_, exact, ok := FromUint(c.n).AsFloat64()
		if !ok {
			t.Fatalf("%d: not a number", c.n)
		}
		if exact != c.exact {
			t.Errorf("%d: exact=%v, want %v", c.n, exact, c.exact)
		}
	}
}

// A float keeps its width and its bits. Narrowing on the way in would make
// -0.0 into 0.0 and flatten NaN payloads, and neither can be recovered.
func TestFloatsKeepTheirBits(t *testing.T) {
	for _, c := range []struct {
		name string
		v    Value
		bits uint64
		kind Kind
	}{
		{"half 1.0", FromFloat16Bits(0x3c00), 0x3c00, Float16},
		{"half -0.0", FromFloat16Bits(0x8000), 0x8000, Float16},
		{"half NaN payload", FromFloat16Bits(0x7e01), 0x7e01, Float16},
		{"single 1.0", FromFloat32Bits(0x3f800000), 0x3f800000, Float32},
		{"single signalling NaN", FromFloat32Bits(0x7f800001), 0x7f800001, Float32},
		{"double -0.0", FromFloat64Bits(0x8000000000000000), 0x8000000000000000, Float64},
		{"double NaN payload", FromFloat64Bits(0x7ff8000000000001), 0x7ff8000000000001, Float64},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.v.Kind() != c.kind {
				t.Fatalf("kind %v, want %v", c.v.Kind(), c.kind)
			}
			got, ok := c.v.FloatBits()
			if !ok || got != c.bits {
				t.Fatalf("bits %#x, want %#x", got, c.bits)
			}
		})
	}
	// -0.0 is not 0.0, which is the one an == comparison gets wrong.
	negZero, _, _ := FromFloat64Bits(0x8000000000000000).AsFloat64()
	if !math.Signbit(negZero) {
		t.Fatal("-0.0 lost its sign")
	}
}

func TestHalfWideningKeepsPayloads(t *testing.T) {
	for _, c := range []struct {
		half uint16
		want float64
	}{
		{0x0000, 0},
		{0x3c00, 1},
		{0x3e00, 1.5},
		{0xc000, -2},
		{0x7c00, math.Inf(1)},
		{0xfc00, math.Inf(-1)},
		{0x0001, 5.960464477539063e-8}, // smallest subnormal
	} {
		f, _, ok := FromFloat16Bits(c.half).AsFloat64()
		if !ok || f != c.want {
			t.Errorf("half %#04x -> %v, want %v", c.half, f, c.want)
		}
	}
	// A half NaN keeps its payload through the widening.
	f, _, _ := FromFloat16Bits(0x7e01).AsFloat64()
	if !math.IsNaN(f) {
		t.Fatal("half NaN did not widen to NaN")
	}
	if bits := math.Float64bits(f); bits&0x000fffffffffffff == 0 {
		t.Fatal("the NaN payload was flattened by the widening")
	}
}

// The simple space is wider than the four named values, and 24-31 have no
// well-formed encoding at all.
func TestSimpleValueSpace(t *testing.T) {
	for n := 0; n < 256; n++ {
		v, ok := FromSimple(uint8(n))
		wantOK := n < 24 || n > 31
		if ok != wantOK {
			t.Errorf("simple %d: ok=%v, want %v", n, ok, wantOK)
			continue
		}
		if !ok {
			continue
		}
		got, _ := v.AsSimple()
		if int(got) != n {
			t.Errorf("simple %d round-tripped as %d", n, got)
		}
	}
	if !False.IsBool() || !True.IsBool() {
		t.Error("False and True are not bools")
	}
	if !Null.IsNull() || !Undefined.IsUndefined() {
		t.Error("Null and Undefined misreport themselves")
	}
	if Null.IsUndefined() || Undefined.IsNull() {
		t.Error("Null and Undefined are the same value")
	}
}

func TestZeroValueIsInvalid(t *testing.T) {
	var v Value
	if v.Kind() != Invalid {
		t.Fatalf("the zero Value is %v, so an unset value passes for a set one", v.Kind())
	}
	if _, ok := v.AsUint(); ok {
		t.Fatal("the zero Value reads as a uint")
	}
}

func TestContainersAndTags(t *testing.T) {
	a := FromArray(FromUint(1), FromText("x"))
	if got, ok := a.AsArray(); !ok || len(got) != 2 {
		t.Fatalf("array %v", got)
	}
	m := FromMap(KeyValue{FromText("k"), FromUint(1)})
	if got, ok := m.AsMap(); !ok || len(got) != 1 || got[0].Key.Kind() != Text {
		t.Fatalf("map %v", got)
	}
	tg := FromTag(1363896240, FromUint(1700000000))
	n, inner, ok := tg.AsTag()
	if !ok || n != 1363896240 {
		t.Fatalf("tag %d", n)
	}
	if got, _ := inner.AsUint(); got != 1700000000 {
		t.Fatalf("tagged value %d", got)
	}
}
