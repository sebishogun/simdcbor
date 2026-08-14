package codec

import "github.com/sebishogun/simd"

// Skip: the decoder's walk with the build step removed.
//
// It is in the same package and calls the same head reader on purpose. The
// shipped Skip and Unmarshal were written separately and disagreed in four
// places -- unsupported simple values, integer map keys, tagged string keys,
// invalid UTF-8 -- none of which any test found until one enumerated the head
// space. Two walks over one grammar will always drift; this is one walk.
//
// There are two arms, and the split is a measurement. Framing alone is what a
// filter needs, and validating the contents of strings it steps past costs
// +75% instructions (docs/wrong.md). So skip takes a strict flag: false judges
// framing, true judges what Decode would accept.
func (d *Decoder) skip(depth int, strict bool) error {
	if depth < 0 {
		return ErrDepth
	}
	if err := d.count(); err != nil {
		return err
	}
	mt, ai, arg, indef, err := d.head()
	if err != nil {
		return err
	}
	switch mt {
	case mtUint, mtNegInt:
		if indef {
			return ErrMalformed
		}
		return nil
	case mtBytes, mtText:
		return d.skipStringBody(mt, arg, indef, depth, strict)
	case mtArray:
		return d.skipContainer(arg, indef, depth, 1, strict)
	case mtMap:
		return d.skipContainer(arg, indef, depth, 2, strict)
	case mtTag:
		if indef {
			return ErrMalformed
		}
		return d.skip(depth-1, strict)
	default:
		// Major 7. The head reader has already refused the reserved ai values;
		// what remains to reject is the break stop-code standing alone.
		if ai == 31 {
			return ErrMalformed
		}
		if ai == 24 && arg < 32 {
			return ErrMalformed // duplicate encoding of a one-byte simple
		}
		return nil
	}
}

// skipStringBody walks a definite or chunked string. It validates UTF-8 for
// text, which is the one piece of content it looks at -- and it looks because
// the decoder rejects invalid text, so a skip that accepted it would hand the
// caller an offset into a document the decoder cannot read.
func (d *Decoder) skipStringBody(mt byte, arg uint64, indef bool, depth int, strict bool) error {
	if !indef {
		s, err := d.chunk(arg)
		if err != nil {
			return err
		}
		if strict && mt == mtText && !simd.ValidUTF8(s) {
			return ErrMalformed
		}
		return nil
	}
	if depth < 0 {
		return ErrDepth
	}
	// The concatenation is what gets validated: a chunk boundary may fall
	// inside a rune, so per-chunk validation would reject well-formed input.
	var joined []byte
	total := 0
	for {
		if d.i >= len(d.b) {
			return ErrTruncated
		}
		if d.b[d.i] == 0xff {
			d.i++
			break
		}
		cmt, _, carg, cindef, err := d.head()
		if err != nil {
			return err
		}
		if cmt != mt || cindef {
			return ErrMalformed
		}
		c, err := d.chunk(carg)
		if err != nil {
			return err
		}
		total += len(c)
		if total > d.lim.MaxStringBytes {
			return ErrTooLarge
		}
		if strict && mt == mtText {
			joined = append(joined, c...)
		}
	}
	if strict && mt == mtText && !simd.ValidUTF8(joined) {
		return ErrMalformed
	}
	return nil
}

// skipContainer walks arg items (or arg pairs, when each is two items) or runs
// to the break stop-code.
func (d *Decoder) skipContainer(arg uint64, indef bool, depth, per int, strict bool) error {
	if depth < 0 {
		return ErrDepth
	}
	if indef {
		for {
			done, err := d.atBreak()
			if err != nil {
				return err
			}
			if done {
				return nil
			}
			for k := 0; k < per; k++ {
				if err := d.skip(depth-1, strict); err != nil {
					return err
				}
			}
		}
	}
	limit := uint64(d.lim.MaxArrayElements)
	if per == 2 {
		limit = uint64(d.lim.MaxMapPairs)
	}
	if arg > limit {
		return ErrTooLarge
	}
	if arg > uint64(len(d.b)-d.i)/uint64(per) {
		return ErrTruncated
	}
	for k := uint64(0); k < arg; k++ {
		for p := 0; p < per; p++ {
			if err := d.skip(depth-1, strict); err != nil {
				return err
			}
		}
	}
	return nil
}
