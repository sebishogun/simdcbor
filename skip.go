package simdcbor

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
			// The key: text strings only, matching Unmarshal's map[string]any.
			if j >= len(b) {
				return 0, ErrTruncated
			}
			if b[j]>>5 != mtText {
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
