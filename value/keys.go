package value

import (
	"encoding/binary"
	"errors"
	"math"
)

// Map keys, and what makes two of them the same key.
//
// CBOR keys are compared as encoded bytes, not as Go values, and that is the
// only definition that works: 1.0 arrives as a half, a single or a double, and
// all three are the same key. So a key's identity is its canonical encoding --
// the shortest wire form that still reproduces it exactly.
//
// "Exactly" is doing work in that sentence. A float narrows only when the
// narrower width reads back bit-identical, which keeps -0.0 apart from 0.0 and
// keeps two NaNs with different payloads apart, while still making the three
// spellings of 1.0 one key.

// ErrStructuralKey is returned for an array or map used as a key when
// structural keys are not enabled.
var ErrStructuralKey = errors.New("simdcbor/value: array or map used as a map key")

// KeyMode controls which values may be keys.
type KeyMode uint8

const (
	// DirectKeys accepts integers, strings, floats, simple values and tags of
	// those. An array or map key is refused.
	DirectKeys KeyMode = iota
	// StructuralKeys additionally accepts arrays and maps, compared by their
	// canonical encoding like any other key.
	StructuralKeys
)

// CanonicalKey returns the bytes that identify v as a map key. Two keys are
// the same key when these bytes are equal.
func CanonicalKey(v Value, mode KeyMode) ([]byte, error) {
	var dst []byte
	return appendCanonical(dst, v, mode, 0)
}

// KeyString is CanonicalKey as a string, for use as a Go map key when
// detecting duplicates.
func KeyString(v Value, mode KeyMode) (string, error) {
	b, err := CanonicalKey(v, mode)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// KeysEqual reports whether two values are the same map key.
func KeysEqual(a, b Value, mode KeyMode) (bool, error) {
	ka, err := CanonicalKey(a, mode)
	if err != nil {
		return false, err
	}
	kb, err := CanonicalKey(b, mode)
	if err != nil {
		return false, err
	}
	if len(ka) != len(kb) {
		return false, nil
	}
	for i := range ka {
		if ka[i] != kb[i] {
			return false, nil
		}
	}
	return true, nil
}

const maxKeyDepth = 64

func appendCanonical(dst []byte, v Value, mode KeyMode, depth int) ([]byte, error) {
	if depth > maxKeyDepth {
		return nil, errors.New("simdcbor/value: key nested too deeply")
	}
	switch v.kind {
	case Uint:
		return appendHead(dst, 0, v.num), nil
	case NegInt:
		return appendHead(dst, 1, v.num), nil
	case Bytes:
		return append(appendHead(dst, 2, uint64(len(v.b))), v.b...), nil
	case Text:
		return append(appendHead(dst, 3, uint64(len(v.b))), v.b...), nil
	case TagKind:
		// A tag is part of the key: Tag{1,"a"} and Tag{2,"a"} are different
		// keys, and so are Tag{1,"a"} and a bare "a".
		if len(v.a) != 1 {
			return nil, errors.New("simdcbor/value: malformed tag")
		}
		dst = appendHead(dst, 6, v.num)
		return appendCanonical(dst, v.a[0], mode, depth+1)
	case SimpleKind:
		if v.num < 24 {
			return append(dst, 0xe0|byte(v.num)), nil
		}
		return append(dst, 0xf8, byte(v.num)), nil
	case Float16, Float32, Float64:
		return appendCanonicalFloat(dst, v), nil
	case Array:
		if mode != StructuralKeys {
			return nil, ErrStructuralKey
		}
		dst = appendHead(dst, 4, uint64(len(v.a)))
		var err error
		for _, e := range v.a {
			if dst, err = appendCanonical(dst, e, mode, depth+1); err != nil {
				return nil, err
			}
		}
		return dst, nil
	case Map:
		if mode != StructuralKeys {
			return nil, ErrStructuralKey
		}
		dst = appendHead(dst, 5, uint64(len(v.m)))
		var err error
		for _, kv := range v.m {
			if dst, err = appendCanonical(dst, kv.Key, mode, depth+1); err != nil {
				return nil, err
			}
			if dst, err = appendCanonical(dst, kv.Value, mode, depth+1); err != nil {
				return nil, err
			}
		}
		return dst, nil
	}
	return nil, errors.New("simdcbor/value: invalid value used as a key")
}

// appendCanonicalFloat writes the float in the narrowest width that reads back
// bit-identical, so the three spellings of 1.0 are one key while -0.0, 0.0 and
// two NaN payloads stay three different keys.
func appendCanonicalFloat(dst []byte, v Value) []byte {
	var bits64 uint64
	switch v.kind {
	case Float16:
		return append(dst, 0xf9, byte(v.num>>8), byte(v.num))
	case Float32:
		b32 := uint32(v.num)
		if h, ok := float32ToHalfExact(b32); ok {
			return append(dst, 0xf9, byte(h>>8), byte(h))
		}
		dst = append(dst, 0xfa)
		return binary.BigEndian.AppendUint32(dst, b32)
	default:
		bits64 = v.num
	}
	if b32, ok := float64ToFloat32Exact(bits64); ok {
		if h, ok := float32ToHalfExact(b32); ok {
			return append(dst, 0xf9, byte(h>>8), byte(h))
		}
		dst = append(dst, 0xfa)
		return binary.BigEndian.AppendUint32(dst, b32)
	}
	dst = append(dst, 0xfb)
	return binary.BigEndian.AppendUint64(dst, bits64)
}

// ShortestFloat returns v in the narrowest float width that reads back
// bit-identical, or v unchanged when it cannot narrow.
//
// Exported because the encoder's preferred-serialization rule and the map-key
// identity rule are the same rule: two floats are the same value exactly when
// they have the same shortest exact form. Two implementations of that would be
// two answers to "is this the same key".
func ShortestFloat(v Value) Value {
	switch v.kind {
	case Float16:
		return v
	case Float32:
		if h, ok := float32ToHalfExact(uint32(v.num)); ok {
			return FromFloat16Bits(h)
		}
		return v
	case Float64:
		if b32, ok := float64ToFloat32Exact(v.num); ok {
			if h, ok := float32ToHalfExact(b32); ok {
				return FromFloat16Bits(h)
			}
			return FromFloat32Bits(b32)
		}
		return v
	}
	return v
}

// float64ToFloat32Exact narrows only when widening the result restores the
// original bits, which is what keeps a NaN payload from being invented or lost.
func float64ToFloat32Exact(bits uint64) (uint32, bool) {
	f := math.Float64frombits(bits)
	b32 := math.Float32bits(float32(f))
	if math.Float64bits(float64(math.Float32frombits(b32))) == bits {
		return b32, true
	}
	return 0, false
}

func float32ToHalfExact(b32 uint32) (uint16, bool) {
	h, ok := float32bitsToHalf(b32)
	if !ok {
		return 0, false
	}
	if halfToFloat32bits(h) == b32 {
		return h, true
	}
	return 0, false
}

// float32bitsToHalf narrows a binary32 to a binary16 when it fits without
// rounding. It reports false rather than rounding, because a key that rounded
// would collide with a different value.
func float32bitsToHalf(b uint32) (uint16, bool) {
	sign := uint16((b >> 16) & 0x8000)
	exp := int32((b>>23)&0xff) - 127
	mant := b & 0x7fffff
	switch {
	case (b>>23)&0xff == 0xff:
		// Infinity or NaN: the payload has to survive the narrowing.
		if mant&0x1fff != 0 {
			return 0, false
		}
		return sign | 0x7c00 | uint16(mant>>13), true
	case (b>>23)&0xff == 0 && mant == 0:
		return sign, true // zero, keeping the sign
	case exp < -24 || exp > 15:
		return 0, false
	case exp < -14:
		// Subnormal in binary16.
		shift := uint32(-14 - exp)
		full := mant | 0x800000
		if full&((1<<(13+shift))-1) != 0 {
			return 0, false // would round
		}
		return sign | uint16(full>>(13+shift)), true
	default:
		if mant&0x1fff != 0 {
			return 0, false // would round
		}
		return sign | uint16((exp+15)<<10) | uint16(mant>>13), true
	}
}

// appendHead writes a CBOR head in its shortest form.
func appendHead(dst []byte, major byte, arg uint64) []byte {
	m := major << 5
	switch {
	case arg < 24:
		return append(dst, m|byte(arg))
	case arg <= 0xff:
		return append(dst, m|24, byte(arg))
	case arg <= 0xffff:
		return append(dst, m|25, byte(arg>>8), byte(arg))
	case arg <= 0xffffffff:
		dst = append(dst, m|26)
		return binary.BigEndian.AppendUint32(dst, uint32(arg))
	default:
		dst = append(dst, m|27)
		return binary.BigEndian.AppendUint64(dst, arg)
	}
}
