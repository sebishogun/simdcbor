package codec

import (
	"bytes"
	"encoding/hex"
	"math"
	"math/rand"
	"testing"

	"github.com/sebishogun/simdcbor/value"
)

func encodeValue(t *testing.T, v value.Value) []byte {
	t.Helper()
	e := NewEncoder(nil)
	if err := e.WriteValue(v); err != nil {
		t.Fatalf("encode: %v", err)
	}
	b, err := e.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	return b
}

// Every vector decodes, encodes and decodes again to the same value. Widths
// and bits are part of "the same": a re-encode that turned a half into a
// double would round-trip by value and change the document.
func TestEncoderRoundTripsVectors(t *testing.T) {
	for _, h := range []string{
		"00", "17", "1818", "1903e8", "1a000f4240", "1b000000e8d4a51000",
		"1bffffffffffffffff", "20", "3863", "3bffffffffffffffff",
		"40", "4401020304", "60", "6161", "6449455446", "64f0908591",
		"80", "83010203", "8301820203820405", "a0", "a201020304",
		"a26161016162820203", "826161a161626163",
		"c074323031332d30332d32315432303a30343a30305a", "c11a514b67b0",
		"f4", "f5", "f6", "f7", "e0", "f0", "f3", "f820", "f8ff",
		"f90000", "f93c00", "f97e00", "fa47c35000", "fb400921fb54442d18",
		"f9c400", "fbc010666666666666",
	} {
		t.Run(h, func(t *testing.T) {
			in, _ := hex.DecodeString(h)
			v, err := New(in, Limits{}).Decode()
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			out := encodeValue(t, v)
			if !bytes.Equal(out, in) {
				t.Fatalf("re-encoded %x, want %x", out, in)
			}
			// And it decodes back to an equal value, by canonical key.
			v2, err := New(out, Limits{}).Decode()
			if err != nil {
				t.Fatalf("re-decode: %v", err)
			}
			k1, e1 := value.CanonicalKey(v, value.StructuralKeys)
			k2, e2 := value.CanonicalKey(v2, value.StructuralKeys)
			if e1 != nil || e2 != nil {
				t.Fatalf("canonical key: %v %v", e1, e2)
			}
			if !bytes.Equal(k1, k2) {
				t.Fatalf("round trip changed the value")
			}
		})
	}
}

// The indefinite forms emit exactly the documented bytes, and 0xff appears
// only where an End puts it.
func TestEncoderIndefiniteForms(t *testing.T) {
	t.Run("array", func(t *testing.T) {
		e := NewEncoder(nil)
		must(t, e.StartIndefiniteArray())
		must(t, e.WriteUint(1))
		must(t, e.WriteUint(2))
		must(t, e.EndArray())
		got, err := e.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		if want := []byte{0x9f, 0x01, 0x02, 0xff}; !bytes.Equal(got, want) {
			t.Fatalf("%x, want %x", got, want)
		}
	})
	t.Run("map", func(t *testing.T) {
		e := NewEncoder(nil)
		must(t, e.StartIndefiniteMap())
		must(t, e.WriteText("a"))
		must(t, e.WriteUint(1))
		must(t, e.EndMap())
		got, _ := e.Bytes()
		if want := []byte{0xbf, 0x61, 'a', 0x01, 0xff}; !bytes.Equal(got, want) {
			t.Fatalf("%x, want %x", got, want)
		}
	})
	t.Run("bytes chunked", func(t *testing.T) {
		e := NewEncoder(nil)
		must(t, e.StartIndefiniteBytes())
		must(t, e.WriteChunk([]byte{1, 2}))
		must(t, e.WriteChunk([]byte{3, 4, 5}))
		must(t, e.EndBytes())
		got, _ := e.Bytes()
		want, _ := hex.DecodeString("5f42010243030405ff")
		if !bytes.Equal(got, want) {
			t.Fatalf("%x, want %x", got, want)
		}
		v, err := New(got, Limits{}).Decode()
		if err != nil {
			t.Fatal(err)
		}
		if b, _ := v.AsBytes(); !bytes.Equal(b, []byte{1, 2, 3, 4, 5}) {
			t.Fatalf("decoded %x", b)
		}
	})
	t.Run("text chunked across a rune", func(t *testing.T) {
		// The two bytes of "ü" split between chunks: legal, because the
		// concatenation is what has to be valid.
		e := NewEncoder(nil)
		must(t, e.StartIndefiniteText())
		must(t, e.WriteChunk([]byte{0xc3}))
		must(t, e.WriteChunk([]byte{0xbc}))
		must(t, e.EndText())
		got, err := e.Bytes()
		if err != nil {
			t.Fatalf("a rune split across chunks was refused: %v", err)
		}
		v, err := New(got, Limits{}).Decode()
		if err != nil {
			t.Fatal(err)
		}
		if s, _ := v.AsText(); s != "ü" {
			t.Fatalf("decoded %q", s)
		}
	})
	t.Run("text chunks that never form valid UTF-8", func(t *testing.T) {
		e := NewEncoder(nil)
		must(t, e.StartIndefiniteText())
		must(t, e.WriteChunk([]byte{0xcd}))
		if err := e.EndText(); err == nil {
			t.Fatal("an indefinite text string of invalid UTF-8 was accepted")
		}
	})
}

// The container stack refuses before writing, because a half-written container
// cannot be unwound.
func TestEncoderContainerStack(t *testing.T) {
	t.Run("end on an empty stack", func(t *testing.T) {
		e := NewEncoder(nil)
		if err := e.EndArray(); err != ErrContainerMismatch {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("closing the wrong type", func(t *testing.T) {
		e := NewEncoder(nil)
		must(t, e.StartMap(1))
		if err := e.EndArray(); err != ErrContainerMismatch {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("overrunning a definite count", func(t *testing.T) {
		e := NewEncoder(nil)
		must(t, e.StartArray(2))
		must(t, e.WriteUint(1))
		must(t, e.WriteUint(2))
		if err := e.WriteUint(3); err != ErrCountOverrun {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("ending short of a definite count", func(t *testing.T) {
		e := NewEncoder(nil)
		must(t, e.StartArray(3))
		must(t, e.WriteUint(1))
		if err := e.EndArray(); err != ErrCountOverrun {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("a map counts pairs, not items", func(t *testing.T) {
		e := NewEncoder(nil)
		must(t, e.StartMap(1))
		must(t, e.WriteText("a"))
		if err := e.EndMap(); err != ErrCountOverrun {
			t.Fatal("a map with a key and no value was accepted")
		}
	})
	t.Run("an open container is not a document", func(t *testing.T) {
		e := NewEncoder(nil)
		must(t, e.StartArray(1))
		must(t, e.WriteUint(1))
		if _, err := e.Bytes(); err != ErrContainerMismatch {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("a chunk outside an indefinite string", func(t *testing.T) {
		e := NewEncoder(nil)
		must(t, e.StartArray(1))
		if err := e.WriteChunk([]byte{1}); err != ErrContainerMismatch {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestEncoderTags(t *testing.T) {
	e := NewEncoder(nil)
	must(t, e.WriteTag(55799)) // the self-describe tag
	must(t, e.WriteUint(1))
	got, err := e.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := hex.DecodeString("d9d9f701"); !bytes.Equal(got, want) {
		t.Fatalf("%x, want %x", got, want)
	}
	// A tag and its content are one item to the container around them.
	e = NewEncoder(nil)
	must(t, e.StartArray(1))
	must(t, e.WriteTag(1))
	must(t, e.WriteUint(0))
	must(t, e.EndArray())
	if _, err := e.Bytes(); err != nil {
		t.Fatalf("a tagged item inside a one-element array: %v", err)
	}
}

// The whole simple space, and the reserved range that has no encoding.
func TestEncoderSimpleSpace(t *testing.T) {
	for n := 0; n < 256; n++ {
		e := NewEncoder(nil)
		err := e.WriteSimple(uint8(n))
		if n >= 24 && n <= 31 {
			if err != ErrNotWellFormed {
				t.Errorf("simple %d: err=%v, want ErrNotWellFormed", n, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("simple %d: %v", n, err)
			continue
		}
		got, _ := e.Bytes()
		var want []byte
		if n < 24 {
			want = []byte{0xe0 | byte(n)}
		} else {
			want = []byte{0xf8, byte(n)}
		}
		if !bytes.Equal(got, want) {
			t.Errorf("simple %d: %x, want %x", n, got, want)
		}
		// And it decodes back to the same simple value.
		v, err := New(got, Limits{}).Decode()
		if err != nil {
			t.Errorf("simple %d does not decode: %v", n, err)
			continue
		}
		if s, _ := v.AsSimple(); int(s) != n {
			t.Errorf("simple %d decoded as %d", n, s)
		}
	}
}

// The encoder refuses text it knows the decoder will reject, rather than
// producing a document this package cannot read back.
func TestEncoderRefusesInvalidUTF8(t *testing.T) {
	e := NewEncoder(nil)
	if err := e.WriteText(string([]byte{0xcd})); err != ErrNotWellFormed {
		t.Fatalf("err=%v", err)
	}
}

// Narrowing a float is exact or not at all, so -0.0 and NaN payloads survive.
func TestEncoderFloatNarrowing(t *testing.T) {
	for _, c := range []struct {
		f    float64
		want string
	}{
		{1, "fa3f800000"},
		{100000, "fa47c35000"},
		{0.1, "fb3fb999999999999a"},
		{math.Inf(1), "fa7f800000"},
	} {
		e := NewEncoder(nil)
		must(t, e.WriteFloat64(c.f))
		got, _ := e.Bytes()
		if hex.EncodeToString(got) != c.want {
			t.Errorf("%v encoded as %x, want %s", c.f, got, c.want)
		}
	}
	// -0.0 keeps its sign through the narrowing.
	e := NewEncoder(nil)
	must(t, e.WriteFloat64(math.Copysign(0, -1)))
	got, _ := e.Bytes()
	v, _ := New(got, Limits{}).Decode()
	f, _, _ := v.AsFloat64()
	if !math.Signbit(f) {
		t.Fatalf("-0.0 lost its sign: %x", got)
	}
}

// A generated corpus, encoded and decoded back: the property is that the
// canonical key of the value is unchanged, which is equality that accounts for
// widths and payloads.
func TestEncoderRoundTripsGeneratedValues(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	var gen func(depth int) value.Value
	gen = func(depth int) value.Value {
		switch rng.Intn(11) {
		case 0:
			return value.FromUint(rng.Uint64())
		case 1:
			return value.FromNegMagnitude(rng.Uint64())
		case 2:
			b := make([]byte, rng.Intn(6))
			rng.Read(b)
			return value.FromBytes(b)
		case 3:
			return value.FromText([]string{"", "a", "ab", "ü", "水", "𐅑"}[rng.Intn(6)])
		case 4:
			n, _ := value.FromSimple(uint8(rng.Intn(24)))
			return n
		case 5:
			n, _ := value.FromSimple(uint8(32 + rng.Intn(224)))
			return n
		case 6:
			return value.FromFloat16Bits(uint16(rng.Intn(1 << 16)))
		case 7:
			return value.FromFloat64Bits(rng.Uint64())
		case 8:
			if depth <= 0 {
				return value.FromUint(0)
			}
			n := rng.Intn(4)
			els := make([]value.Value, n)
			for i := range els {
				els[i] = gen(depth - 1)
			}
			return value.FromArray(els...)
		case 9:
			if depth <= 0 {
				return value.FromUint(0)
			}
			n := rng.Intn(3)
			kvs := make([]value.KeyValue, n)
			for i := range kvs {
				kvs[i] = value.KeyValue{Key: gen(depth - 1), Value: gen(depth - 1)}
			}
			return value.FromMap(kvs...)
		default:
			if depth <= 0 {
				return value.FromUint(0)
			}
			return value.FromTag(rng.Uint64()%70000, gen(depth-1))
		}
	}
	for i := 0; i < 20000; i++ {
		v := gen(3)
		e := NewEncoder(nil)
		if err := e.WriteValue(v); err != nil {
			t.Fatalf("encode: %v", err)
		}
		b, err := e.Bytes()
		if err != nil {
			t.Fatalf("Bytes: %v", err)
		}
		v2, err := New(b, Limits{}).Decode()
		if err != nil {
			t.Fatalf("%x does not decode: %v", b, err)
		}
		k1, err1 := value.CanonicalKey(v, value.StructuralKeys)
		k2, err2 := value.CanonicalKey(v2, value.StructuralKeys)
		if err1 != nil || err2 != nil {
			continue // a value too deep to key; the round trip above still held
		}
		if !bytes.Equal(k1, k2) {
			t.Fatalf("round trip changed the value: %x", b)
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
