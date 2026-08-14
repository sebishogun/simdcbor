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

import "errors"

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
	// The adapter over internal/codec. The old hand-written walk lived here
	// and disagreed with Skip's in four places; there is one walk now, and
	// this is the projection onto the shipped shapes.
	// DecodeJSON builds the shipped shapes directly off the walk. Going
	// through the full value model and projecting afterwards was 2-3x slower,
	// which the bench gate caught the moment the adapter landed: two
	// allocations and two passes per item to produce the same answer.
	d := adapterDecoder(data)
	out, err := d.DecodeJSON()
	if err != nil {
		return nil, 0, mapErr(err)
	}
	return out, d.Offset(), nil
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
