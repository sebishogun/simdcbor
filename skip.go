package simdcbor

import "github.com/sebishogun/simd"

// Skip advances past the CBOR item at the front of data without building
// any Go value, returning the number of bytes it spans. It is the hot
// path for filtering a stream of records -- frame each item, decode only
// the ones that match -- and CBOR's explicit lengths make it pure
// arithmetic: a string skips its byte count, a container skips its
// declared number of items, no allocation and no per-value interface.
//
// Errors are ErrTruncated for a short buffer and ErrMalformed for a head
// that cannot begin an item; the accept/reject boundary is identical to
// Unmarshal's, so a Skip that succeeds is an item Unmarshal would decode.
//
// That last sentence used to be false in two places, which is why the
// boundary is now enforced here rather than assumed: simple values 0-19 and
// the two-byte 0xf8 form were skipped but not decodable, and a map with a
// non-text key was skipped while Unmarshal rejects it (the shipped value
// model is map[string]any). Both are the shipped subset's limits, not CBOR's;
// when the value model grows to the full space, both paths widen together.
func Skip(data []byte) (int, error) {
	return skip(data, 0, 64)
}

// keyDecodesToString peeks at the item at b[j] and reports whether Unmarshal
// would produce a Go string from it -- a text or byte string, under any number
// of tags, since decode returns the tagged value itself.
//
// It peeks rather than decodes: the caller still skips the whole item, tags
// included, so the span is unchanged.
func keyDecodesToString(b []byte, j int) bool {
	for depth := 0; depth < 64; depth++ {
		if j >= len(b) {
			return false
		}
		switch b[j] >> 5 {
		case mtText, mtBytes:
			return true
		case mtTag:
			_, next, err := readArg(b, j)
			if err != nil {
				return false
			}
			j = next
		default:
			return false
		}
	}
	return false
}

func skip(b []byte, i, depth int) (int, error) {
	if depth < 0 {
		return 0, ErrMalformed
	}
	if i >= len(b) {
		return 0, ErrTruncated
	}
	mt := b[i] >> 5
	ai := b[i] & 0x1f
	arg, j, err := readArg(b, i)
	if err != nil {
		return 0, err
	}
	switch mt {
	case mtUint, mtNegInt:
		return j, nil
	case mtSimple:
		// Only what decode accepts: false, true, null, undefined and the three
		// float widths. Simple values 0-19 and the two-byte form (ai 24) are
		// well-formed CBOR that this value model cannot represent.
		switch ai {
		case 20, 21, 22, 23, 25, 26, 27:
			return j, nil
		}
		return 0, ErrMalformed
	case mtBytes, mtText:
		if ai == 31 {
			return 0, ErrMalformed
		}
		end := j + int(arg)
		if end < j || end > len(b) {
			return 0, ErrTruncated
		}
		if mt == mtText && !simd.ValidUTF8(b[j:end]) {
			// Content, not framing -- and the only reason Skip looks at it is
			// the contract that a Skip which succeeds is an item Unmarshal
			// would decode (architecture.md). Unmarshal rejects a text string
			// that is not valid UTF-8, so Skip has to as well or the boundary
			// is not identical. Found by the fuzz on 61 cd.
			return 0, ErrMalformed
		}
		return end, nil
	case mtArray:
		if arg > uint64(len(b)-j) {
			return 0, ErrTruncated
		}
		for k := 0; k < int(arg); k++ {
			n, err := skip(b[j:], 0, depth-1)
			if err != nil {
				return 0, err
			}
			j += n
		}
		return j, nil
	case mtMap:
		if arg > uint64(len(b)-j) {
			return 0, ErrTruncated
		}
		for k := 0; k < int(arg); k++ {
			// The key has to be something Unmarshal can put in a
			// map[string]any. decode returns a Go string for a text string and
			// for a byte string, and it sees through tags, so a tagged string
			// is a key too. Each of those three facts cost a fuzz finding.
			if !keyDecodesToString(b, j) {
				if j >= len(b) {
					return 0, ErrTruncated
				}
				return 0, ErrMalformed
			}
			n, err := skip(b[j:], 0, depth-1)
			if err != nil {
				return 0, err
			}
			j += n
			n, err = skip(b[j:], 0, depth-1)
			if err != nil {
				return 0, err
			}
			j += n
		}
		return j, nil
	case mtTag:
		n, err := skip(b[j:], 0, depth-1)
		if err != nil {
			return 0, err
		}
		return j + n, nil
	}
	return 0, ErrMalformed
}
