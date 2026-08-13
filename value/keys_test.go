package value

import (
	"bytes"
	"testing"
)

func mustKey(t *testing.T, v Value, mode KeyMode) []byte {
	t.Helper()
	k, err := CanonicalKey(v, mode)
	if err != nil {
		t.Fatalf("CanonicalKey: %v", err)
	}
	return k
}

func equal(t *testing.T, a, b Value, mode KeyMode) bool {
	t.Helper()
	ok, err := KeysEqual(a, b, mode)
	if err != nil {
		t.Fatalf("KeysEqual: %v", err)
	}
	return ok
}

func TestKeyIdentity(t *testing.T) {
	for _, c := range []struct {
		name string
		a, b Value
		same bool
	}{
		{"same bytes", FromBytes([]byte{0}), FromBytes([]byte{0}), true},
		{"different length bytes", FromBytes([]byte{0}), FromBytes([]byte{0, 0}), false},
		{"bytes and text of the same payload", FromBytes([]byte("a")), FromText("a"), false},
		{"same uint", FromUint(1), FromUint(1), true},
		{"uint and negint", FromUint(1), FromNegMagnitude(1), false},
		{"same text", FromText("a"), FromText("a"), true},

		// The reason keys are compared as encodings: 1.0 arrives three ways.
		{"half and single 1.0", FromFloat16Bits(0x3c00), FromFloat32Bits(0x3f800000), true},
		{"half and double 1.0", FromFloat16Bits(0x3c00), FromFloat64(1), true},
		{"single and double 1.0", FromFloat32Bits(0x3f800000), FromFloat64(1), true},
		{"1.0 and 2.0", FromFloat64(1), FromFloat64(2), false},

		// And the reason narrowing has to be exact rather than approximate.
		{"0.0 and -0.0", FromFloat64(0), FromFloat64Bits(0x8000000000000000), false},
		{"two NaN payloads", FromFloat64Bits(0x7ff8000000000001), FromFloat64Bits(0x7ff8000000000002), false},
		{"a NaN equals itself", FromFloat64Bits(0x7ff8000000000001), FromFloat64Bits(0x7ff8000000000001), true},
		{"a value that cannot narrow", FromFloat64(0.1), FromFloat64(0.1), true},

		{"named simples differ", Null, Undefined, false},
		{"a named simple equals itself", True, True, true},

		// Tags classify by number and by what they wrap.
		{"same tag same value", FromTag(1, FromText("a")), FromTag(1, FromText("a")), true},
		{"different tag number", FromTag(1, FromText("a")), FromTag(2, FromText("a")), false},
		{"different tagged value", FromTag(1, FromText("a")), FromTag(1, FromText("b")), false},
		{"tag of bytes is a key", FromTag(1, FromBytes([]byte{1})), FromTag(1, FromBytes([]byte{1})), true},
		{"a tag is not its content", FromTag(1, FromText("a")), FromText("a"), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := equal(t, c.a, c.b, DirectKeys); got != c.same {
				t.Fatalf("equal=%v, want %v (keys %x and %x)", got, c.same,
					mustKey(t, c.a, DirectKeys), mustKey(t, c.b, DirectKeys))
			}
		})
	}
}

// A float key narrows only when the narrower width reads back bit-identical,
// so the shortest encoding is the identity and no two distinct values collide.
func TestFloatKeysUseTheShortestExactWidth(t *testing.T) {
	for _, c := range []struct {
		name string
		v    Value
		want []byte
	}{
		{"double 1.0 narrows to half", FromFloat64(1), []byte{0xf9, 0x3c, 0x00}},
		{"single 1.0 narrows to half", FromFloat32Bits(0x3f800000), []byte{0xf9, 0x3c, 0x00}},
		{"half stays half", FromFloat16Bits(0x3c00), []byte{0xf9, 0x3c, 0x00}},
		{"100000.0 needs a single", FromFloat64(100000), []byte{0xfa, 0x47, 0xc3, 0x50, 0x00}},
		{"0.1 needs a double", FromFloat64(0.1), []byte{0xfb, 0x3f, 0xb9, 0x99, 0x99, 0x99, 0x99, 0x99, 0x9a}},
		{"-0.0 narrows and keeps its sign", FromFloat64Bits(0x8000000000000000), []byte{0xf9, 0x80, 0x00}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := mustKey(t, c.v, DirectKeys); !bytes.Equal(got, c.want) {
				t.Fatalf("key %x, want %x", got, c.want)
			}
		})
	}
}

// Structural keys are refused by default and accepted under the mode, and once
// accepted they compare by their whole encoding.
func TestStructuralKeys(t *testing.T) {
	arr := FromArray(FromUint(1), FromUint(2))
	arr3 := FromArray(FromUint(1), FromUint(2), FromUint(0))
	if _, err := CanonicalKey(arr, DirectKeys); err != ErrStructuralKey {
		t.Fatalf("an array key was accepted by default: %v", err)
	}
	if _, err := CanonicalKey(FromMap(KeyValue{FromText("a"), FromUint(1)}), DirectKeys); err != ErrStructuralKey {
		t.Fatal("a map key was accepted by default")
	}
	// A tag of an array is refused by default for the same reason: the value
	// underneath is structural.
	if _, err := CanonicalKey(FromTag(1, arr), DirectKeys); err != ErrStructuralKey {
		t.Fatal("a tag of an array was accepted by default")
	}
	if !equal(t, arr, FromArray(FromUint(1), FromUint(2)), StructuralKeys) {
		t.Fatal("equal arrays are different keys")
	}
	if equal(t, arr, arr3, StructuralKeys) {
		t.Fatal("[1,2] and [1,2,0] are the same key")
	}
	if !equal(t, FromTag(1, arr), FromTag(1, arr), StructuralKeys) {
		t.Fatal("a tag of an array is not a key under StructuralKeys")
	}
}

func TestDuplicateKeyDetection(t *testing.T) {
	// The three spellings of 1.0 are one key, so this map has a duplicate even
	// though no two entries look alike.
	m := FromMap(
		KeyValue{FromFloat16Bits(0x3c00), FromUint(1)},
		KeyValue{FromText("a"), FromUint(2)},
		KeyValue{FromFloat64(1), FromUint(3)},
	)
	k, dup, err := DuplicateKey(m, DirectKeys)
	if err != nil {
		t.Fatal(err)
	}
	if !dup {
		t.Fatal("the two spellings of 1.0 were not seen as the same key")
	}
	if k.Kind() != Float64 {
		t.Fatalf("the duplicate reported is %v", k.Kind())
	}
	clean := FromMap(
		KeyValue{FromText("a"), FromUint(1)},
		KeyValue{FromText("b"), FromUint(2)},
	)
	if _, dup, _ := DuplicateKey(clean, DirectKeys); dup {
		t.Fatal("a map with distinct keys reported a duplicate")
	}
}
