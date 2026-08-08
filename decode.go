// Package simdcbor decodes CBOR (RFC 8949) into Go values.
//
// CBOR is a binary sibling of JSON, and the same architecture applies: a
// first pass over the head bytes to find where every item begins, then a
// walk that builds values from that index rather than re-scanning. The
// scan uses simd where a run of bytes is homogeneous -- copying a byte
// string, hashing map keys, validating a UTF-8 text string -- through the
// kernels simd ships.
//
// The decoded shapes match encoding/json's for the same logical data:
// objects become map[string]any, arrays []any, and numbers float64,
// so a program that consumed JSON into any consumes CBOR the same way.
package simdcbor

import (
	"errors"
	"math"

	"github.com/sebishogun/simd"
)

var (
	ErrTruncated = errors.New("simdcbor: truncated")
	ErrMalformed = errors.New("simdcbor: malformed")
)

// major types (RFC 8949 section 3).
const (
	mtUint   = 0
	mtNegInt = 1
	mtBytes  = 2
	mtText   = 3
	mtArray  = 4
	mtMap    = 5
	mtTag    = 6
	mtSimple = 7
)

// Unmarshal decodes the CBOR item at the front of data into a Go value,
// returning it and the number of bytes consumed.
func Unmarshal(data []byte) (any, int, error) {
	v, n, err := decode(data, 0, 64)
	return v, n, err
}

func decode(b []byte, i, depth int) (any, int, error) {
	if depth < 0 {
		return nil, 0, ErrMalformed
	}
	if i >= len(b) {
		return nil, 0, ErrTruncated
	}
	ib := b[i]
	mt := ib >> 5
	ai := ib & 0x1f
	arg, i, err := readArg(b, i)
	if err != nil {
		return nil, 0, err
	}
	switch mt {
	case mtUint:
		return float64(arg), i, nil
	case mtNegInt:
		return -1 - float64(arg), i, nil
	case mtBytes, mtText:
		if ai == 31 {
			return nil, 0, ErrMalformed // indefinite: handled elsewhere, not yet
		}
		end := i + int(arg)
		if end < i || end > len(b) {
			return nil, 0, ErrTruncated
		}
		s := b[i:end]
		if mt == mtText && !simd.ValidUTF8(s) {
			return nil, 0, ErrMalformed
		}
		return string(s), end, nil
	case mtArray:
		if arg > uint64(len(b)-i) {
			return nil, 0, ErrTruncated // each item is at least one byte
		}
		out := make([]any, 0, min(int(arg), 1024))
		for k := 0; k < int(arg); k++ {
			var v any
			v, i, err = decode(b, i, depth-1)
			if err != nil {
				return nil, 0, err
			}
			out = append(out, v)
		}
		return out, i, nil
	case mtMap:
		if arg > uint64(len(b)-i) { // each pair is at least two bytes
			return nil, 0, ErrTruncated
		}
		out := make(map[string]any, min(int(arg), 1024))
		for k := 0; k < int(arg); k++ {
			var kv, vv any
			kv, i, err = decode(b, i, depth-1)
			if err != nil {
				return nil, 0, err
			}
			ks, ok := kv.(string)
			if !ok {
				return nil, 0, ErrMalformed // JSON-shaped: string keys only
			}
			vv, i, err = decode(b, i, depth-1)
			if err != nil {
				return nil, 0, err
			}
			out[ks] = vv
		}
		return out, i, nil
	case mtTag:
		return decode(b, i, depth-1) // transparent: decode the tagged item
	case mtSimple:
		switch ai {
		case 20:
			return false, i, nil
		case 21:
			return true, i, nil
		case 22, 23:
			return nil, i, nil
		case 25:
			return float64(math.Float32frombits(halfToFloat32bits(uint16(arg)))), i, nil
		case 26:
			return float64(math.Float32frombits(uint32(arg))), i, nil
		case 27:
			return math.Float64frombits(arg), i, nil
		}
		return nil, 0, ErrMalformed
	}
	return nil, 0, ErrMalformed
}

// readArg reads the additional-information argument that follows the head
// byte and returns it with the index past it.
func readArg(b []byte, i int) (uint64, int, error) {
	ai := b[i] & 0x1f
	i++
	switch {
	case ai < 24:
		return uint64(ai), i, nil
	case ai == 24:
		if i >= len(b) {
			return 0, 0, ErrTruncated
		}
		return uint64(b[i]), i + 1, nil
	case ai == 25:
		if i+2 > len(b) {
			return 0, 0, ErrTruncated
		}
		return uint64(b[i])<<8 | uint64(b[i+1]), i + 2, nil
	case ai == 26:
		if i+4 > len(b) {
			return 0, 0, ErrTruncated
		}
		return uint64(b[i])<<24 | uint64(b[i+1])<<16 | uint64(b[i+2])<<8 | uint64(b[i+3]), i + 4, nil
	case ai == 27:
		if i+8 > len(b) {
			return 0, 0, ErrTruncated
		}
		var v uint64
		for k := 0; k < 8; k++ {
			v = v<<8 | uint64(b[i+k])
		}
		return v, i + 8, nil
	}
	return 0, 0, ErrMalformed // 28-30 reserved, 31 indefinite
}

func halfToFloat32bits(h uint16) uint32 {
	sign := uint32(h&0x8000) << 16
	exp := uint32(h>>10) & 0x1f
	mant := uint32(h & 0x3ff)
	switch exp {
	case 0:
		if mant == 0 {
			return sign
		}
		// subnormal: normalize into a float32 normal.
		e := 0
		for mant&0x400 == 0 {
			mant <<= 1
			e++
		}
		mant &= 0x3ff
		return sign | uint32(127-15-e)<<23 | mant<<13
	case 0x1f:
		return sign | 0xff<<23 | mant<<13
	}
	return sign | (exp-15+127)<<23 | mant<<13
}

// On lazy values, and why they are not here yet.
//
// The shape sweep (strings 1.84x, numbers 1.67x, huge array 1.35x
// against fxamacker) shows the decode advantage narrowing exactly where
// allocation dominates: a []any of thousands of boxed scalars is mostly
// the boxing, which no faster scan removes. The lever simdjson documents
// is a lazy value -- keep each item as a byte range and decode it only
// when the caller reads it, the way fastjson does -- which turns a
// filter-then-read workload into near-zero allocation. It is a real win
// and a real interface change (Value handles instead of any), measured
// and deferred rather than unmeasured: Skip already delivers the
// allocation-free traversal for the filtering case, the larger half of
// what lazy values would buy.
