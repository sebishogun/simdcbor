package simdcbor

import "github.com/sebishogun/simd"

// Skip advances past the CBOR item at the front of data without building
// any Go value, returning the number of bytes it spans. It is the hot
// path for filtering a stream of records -- frame each item, decode only
// the ones that match -- and CBOR's explicit lengths make it pure
// arithmetic: a string skips its byte count, a container skips its
// declared number of items, no allocation and no per-value interface.
//
// Errors are ErrTruncated for a short buffer and ErrMalformed for a head that
// cannot begin an item. Skip judges framing: the head is a head, the lengths
// fit, the nesting closes. It does not judge whether the item is one this
// package's value model can represent, so it accepts a superset of what
// Unmarshal does -- an integer map key, a simple value outside the model, a
// text string that is not valid UTF-8.
//
// That split is measured, not assumed. Skip once claimed the identical
// boundary, and making the claim true cost +92.5% instructions on the
// filter-stream benchmark: +10% for the value-model checks and +75% more for
// validating UTF-8 on strings it is skipping past. Skip exists to be the cheap
// arm of a filter, and paying to reject content the caller is discarding is
// backwards. Use SkipStrict where the identical boundary is what matters --
// docs/wrong.md carries the numbers.
func Skip(data []byte) (int, error) {
	return skip(data, 0, 64, false)
}

// SkipStrict is Skip with Unmarshal's boundary rather than framing's: a
// SkipStrict that succeeds is an item Unmarshal would decode, with the same
// span. It additionally rejects simple values outside the value model, map
// keys that would not decode to a Go string, and text strings that are not
// valid UTF-8.
//
// It is the arm to use when a skipped item still has to be known-decodable --
// the adapter's case. It costs about 1.9x what Skip does on a filtering
// workload, which is why the two are separate rather than one strict default.
func SkipStrict(data []byte) (int, error) {
	return skip(data, 0, 64, true)
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

func skip(b []byte, i, depth int, strict bool) (int, error) {
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
		if strict {
			// What decode accepts: false, true, null, undefined and the three
			// float widths. Simple values 0-19 and the two-byte form are
			// well-formed CBOR this value model cannot represent.
			switch ai {
			case 20, 21, 22, 23, 25, 26, 27:
				return j, nil
			}
			return 0, ErrMalformed
		}
		return j, nil
	case mtBytes, mtText:
		if ai == 31 {
			return 0, ErrMalformed
		}
		end := j + int(arg)
		if end < j || end > len(b) {
			return 0, ErrTruncated
		}
		if strict && mt == mtText && !simd.ValidUTF8(b[j:end]) {
			// Content, not framing. Unmarshal rejects a text string that is
			// not valid UTF-8 (found by the fuzz on 61 cd), so the strict arm
			// has to as well -- and it is the reason the strict arm is
			// separate: this scan is +75% instructions on the filter path.
			return 0, ErrMalformed
		}
		return end, nil
	case mtArray:
		if arg > uint64(len(b)-j) {
			return 0, ErrTruncated
		}
		for k := 0; k < int(arg); k++ {
			n, err := skip(b[j:], 0, depth-1, strict)
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
			if strict && !keyDecodesToString(b, j) {
				if j >= len(b) {
					return 0, ErrTruncated
				}
				return 0, ErrMalformed
			}
			n, err := skip(b[j:], 0, depth-1, strict)
			if err != nil {
				return 0, err
			}
			j += n
			n, err = skip(b[j:], 0, depth-1, strict)
			if err != nil {
				return 0, err
			}
			j += n
		}
		return j, nil
	case mtTag:
		n, err := skip(b[j:], 0, depth-1, strict)
		if err != nil {
			return 0, err
		}
		return j + n, nil
	}
	return 0, ErrMalformed
}
