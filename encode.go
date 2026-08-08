package simdcbor

import (
	"math"
	"sort"
)

// Marshal encodes a Go value as CBOR, the inverse of Unmarshal over the
// same JSON-shaped set: nil, bool, float64 (and the Go integer types),
// string, []any and map[string]any. Encoding is the easy half of CBOR --
// every item's length is known before its bytes -- so this is a direct
// append with no backpatching.
//
// Map keys are sorted, so the same map encodes to the same bytes every
// time: canonical output, which is what a cache key or a signature needs.
func Marshal(v any) ([]byte, error) {
	return appendValue(nil, v)
}

func appendValue(b []byte, v any) ([]byte, error) {
	switch x := v.(type) {
	case nil:
		return append(b, 0xf6), nil // null
	case bool:
		if x {
			return append(b, 0xf5), nil
		}
		return append(b, 0xf4), nil
	case string:
		b = appendHead(b, mtText, uint64(len(x)))
		return append(b, x...), nil
	case []byte:
		b = appendHead(b, mtBytes, uint64(len(x)))
		return append(b, x...), nil
	case float64:
		return appendFloat(b, x), nil
	case float32:
		return appendFloat(b, float64(x)), nil
	case int:
		return appendInt(b, int64(x)), nil
	case int64:
		return appendInt(b, x), nil
	case uint64:
		return appendHead(b, mtUint, x), nil
	case []any:
		b = appendHead(b, mtArray, uint64(len(x)))
		var err error
		for _, e := range x {
			if b, err = appendValue(b, e); err != nil {
				return nil, err
			}
		}
		return b, nil
	case map[string]any:
		b = appendHead(b, mtMap, uint64(len(x)))
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var err error
		for _, k := range keys {
			b = appendHead(b, mtText, uint64(len(k)))
			b = append(b, k...)
			if b, err = appendValue(b, x[k]); err != nil {
				return nil, err
			}
		}
		return b, nil
	}
	return nil, ErrMalformed
}

func appendInt(b []byte, v int64) []byte {
	if v < 0 {
		return appendHead(b, mtNegInt, uint64(-1-v))
	}
	return appendHead(b, mtUint, uint64(v))
}

func appendFloat(b []byte, f float64) []byte {
	// A float that round-trips through float32 uses the shorter encoding,
	// exactly as canonical CBOR prescribes; otherwise the full double.
	if float64(float32(f)) == f {
		return append(b, 0xfa, byte(math.Float32bits(float32(f))>>24),
			byte(math.Float32bits(float32(f))>>16),
			byte(math.Float32bits(float32(f))>>8),
			byte(math.Float32bits(float32(f))))
	}
	u := math.Float64bits(f)
	return append(b, 0xfb, byte(u>>56), byte(u>>48), byte(u>>40), byte(u>>32),
		byte(u>>24), byte(u>>16), byte(u>>8), byte(u))
}

// appendHead writes a major type and its argument in the shortest form.
func appendHead(b []byte, mt byte, arg uint64) []byte {
	m := mt << 5
	switch {
	case arg < 24:
		return append(b, m|byte(arg))
	case arg < 1<<8:
		return append(b, m|24, byte(arg))
	case arg < 1<<16:
		return append(b, m|25, byte(arg>>8), byte(arg))
	case arg < 1<<32:
		return append(b, m|26, byte(arg>>24), byte(arg>>16), byte(arg>>8), byte(arg))
	default:
		return append(b, m|27, byte(arg>>56), byte(arg>>48), byte(arg>>40),
			byte(arg>>32), byte(arg>>24), byte(arg>>16), byte(arg>>8), byte(arg))
	}
}
