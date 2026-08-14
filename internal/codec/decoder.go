package codec

import (
	"encoding/binary"

	"github.com/sebishogun/simd"
	"github.com/sebishogun/simdcbor/value"
)

// The head-argument-body machine, shared by the decoder and by Skip.
//
// Sharing it is the point. The shipped Skip and Unmarshal each had their own
// idea of what a well-formed item is, and they disagreed in four places that
// nobody found for as long as the two were written separately. Here the
// framing step is one function, and Skip is the same walk with the build step
// removed, so the accept boundary is shared by construction rather than by
// two authors agreeing.

const (
	mtUint = iota
	mtNegInt
	mtBytes
	mtText
	mtArray
	mtMap
	mtTag
	mtSimple
)

// A head's length is either declared or not, and that is a separate fact from
// the argument's value. Using a sentinel argument for "indefinite" collides:
// ^uint64(0) is exactly what 1b ffffffffffffffff carries, so the largest
// unsigned integer CBOR can express decoded as malformed. The RFC vectors
// caught it on the first run.

// Decoder walks one buffer.
type Decoder struct {
	b     []byte
	i     int
	lim   Limits
	items int
	// keys selects which values may be map keys; the decoder does not enforce
	// it, but it carries it for the value layer that does.
	keys value.KeyMode
	dup  DuplicatePolicy
	tags TagMode

	// pin makes RawMessage copy rather than borrow; borrowUnsafe marks a
	// decoder whose buffer is reused, so borrowing from it is refused.
	pin          bool
	borrowUnsafe bool
	raws         []RawMessage
}

// markBorrowUnsafe records that this decoder's buffer is reused, so a
// RawMessage may not borrow from it. The stream sets it.
func (d *Decoder) markBorrowUnsafe() { d.borrowUnsafe = true }

// New returns a Decoder over b.
func New(b []byte, lim Limits) *Decoder {
	return &Decoder{b: b, lim: lim.withDefaults()}
}

// SetKeyMode selects the key policy the value layer will apply.
func (d *Decoder) SetKeyMode(m value.KeyMode) { d.keys = m }

// Offset returns how far the decoder has advanced.
func (d *Decoder) Offset() int { return d.i }

// More reports whether any bytes remain.
func (d *Decoder) More() bool { return d.i < len(d.b) }

// Decode reads the next item.
func (d *Decoder) Decode() (value.Value, error) {
	return d.decode(d.lim.MaxDepth)
}

// Skip advances past the next item without building it, returning its span.
//
// It judges framing, so it accepts a superset of what Decode does: it does not
// check whether text is valid UTF-8, which costs +75% instructions on a
// filtering workload for content the caller is discarding unread.
func (d *Decoder) Skip() (int, error) {
	start := d.i
	if err := d.skip(d.lim.MaxDepth, false); err != nil {
		return 0, err
	}
	return d.i - start, nil
}

// SkipStrict is Skip with Decode's boundary: an item it accepts is an item
// Decode would build, with the same span.
func (d *Decoder) SkipStrict() (int, error) {
	start := d.i
	if err := d.skip(d.lim.MaxDepth, true); err != nil {
		return 0, err
	}
	return d.i - start, nil
}

// head reads one head byte and its argument.
//
// It is the only place additional-information values are interpreted, which is
// why the reserved ones cannot leak: 28-30 have no meaning and are refused
// here rather than defaulting to something downstream.
func (d *Decoder) head() (mt byte, ai byte, arg uint64, indef bool, err error) {
	if d.i >= len(d.b) {
		return 0, 0, 0, false, ErrTruncated
	}
	c := d.b[d.i]
	mt, ai = c>>5, c&0x1f
	d.i++
	switch {
	case ai < 24:
		return mt, ai, uint64(ai), false, nil
	case ai == 24:
		if d.i >= len(d.b) {
			return 0, 0, 0, false, ErrTruncated
		}
		arg = uint64(d.b[d.i])
		d.i++
	case ai == 25:
		if d.i+2 > len(d.b) {
			return 0, 0, 0, false, ErrTruncated
		}
		arg = uint64(binary.BigEndian.Uint16(d.b[d.i:]))
		d.i += 2
	case ai == 26:
		if d.i+4 > len(d.b) {
			return 0, 0, 0, false, ErrTruncated
		}
		arg = uint64(binary.BigEndian.Uint32(d.b[d.i:]))
		d.i += 4
	case ai == 27:
		if d.i+8 > len(d.b) {
			return 0, 0, 0, false, ErrTruncated
		}
		arg = binary.BigEndian.Uint64(d.b[d.i:])
		d.i += 8
	case ai == 31:
		// Indefinite length. Legal only for the four container types and as
		// the break stop-code; the caller decides.
		return mt, ai, 0, true, nil
	default:
		// ai 28, 29, 30: reserved, and reserved means not well-formed.
		return 0, 0, 0, false, ErrMalformed
	}
	return mt, ai, arg, false, nil
}

func (d *Decoder) count() error {
	d.items++
	if d.items > d.lim.MaxTotalItems {
		return ErrTooLarge
	}
	return nil
}

func (d *Decoder) decode(depth int) (value.Value, error) {
	if depth < 0 {
		return value.Value{}, ErrDepth
	}
	if err := d.count(); err != nil {
		return value.Value{}, err
	}
	mt, ai, arg, indef, err := d.head()
	if err != nil {
		return value.Value{}, err
	}
	switch mt {
	case mtUint:
		if indef {
			return value.Value{}, ErrMalformed
		}
		return value.FromUint(arg), nil
	case mtNegInt:
		if indef {
			return value.Value{}, ErrMalformed
		}
		// The magnitude is what the wire carries; -1-arg is the value, and the
		// endpoint -2^64 stays representable.
		return value.FromNegMagnitude(arg), nil
	case mtBytes, mtText:
		b, err := d.stringBody(mt, arg, indef, depth)
		if err != nil {
			return value.Value{}, err
		}
		var sv value.Value
		if mt == mtText {
			sv = value.FromText(string(b))
		} else {
			sv = value.FromBytes(b)
		}
		if indef {
			sv = sv.AsIndefinite()
		}
		return sv, nil
	case mtArray:
		return d.array(arg, indef, depth)
	case mtMap:
		return d.mapValue(arg, indef, depth)
	case mtTag:
		if indef {
			return value.Value{}, ErrMalformed
		}
		inner, err := d.decode(depth - 1)
		if err != nil {
			return value.Value{}, err
		}
		if d.tags == TagDiscard {
			// Lossy on purpose, and only when asked for: tag 1 around an
			// integer becomes that integer, so a re-encode writes a number
			// where a timestamp was.
			return inner, nil
		}
		tagged := value.FromTag(arg, inner)
		if d.tags == TagInterpret {
			// Interpretation validates the content; it does not replace the
			// generic form, so the original bytes can still be reproduced.
			if _, _, err := Interpret(tagged); err != nil {
				return value.Value{}, err
			}
		}
		return tagged, nil
	default:
		return d.simple(ai, arg)
	}
}

// simple builds a major-7 item: the floats, the named values, and the numeric
// simple space that a decoder mapping everything to nil cannot re-encode.
func (d *Decoder) simple(ai byte, arg uint64) (value.Value, error) {
	switch ai {
	case 25:
		return value.FromFloat16Bits(uint16(arg)), nil
	case 26:
		return value.FromFloat32Bits(uint32(arg)), nil
	case 27:
		return value.FromFloat64Bits(arg), nil
	case 31:
		// break: a terminator, never a value on its own.
		return value.Value{}, ErrMalformed
	case 24:
		// The two-byte form must carry 32 or above; below that it duplicates
		// the one-byte form, and a duplicate encoding is not well-formed.
		if arg < 32 {
			return value.Value{}, ErrMalformed
		}
		v, ok := value.FromSimple(uint8(arg))
		if !ok {
			return value.Value{}, ErrMalformed
		}
		return v, nil
	}
	v, ok := value.FromSimple(uint8(ai))
	if !ok {
		return value.Value{}, ErrMalformed
	}
	return v, nil
}

// stringBody reads a definite or chunked string. An indefinite string is the
// concatenation of its chunks, and for text the concatenation is what gets
// validated -- a chunk boundary may fall inside a multi-byte rune, so
// validating each chunk alone would reject documents that are well-formed.
func (d *Decoder) stringBody(mt byte, arg uint64, indef bool, depth int) ([]byte, error) {
	if !indef {
		s, err := d.chunk(arg)
		if err != nil {
			return nil, err
		}
		// A definite text string needs the same UTF-8 check a chunked one
		// gets. It was missing, and the appendix vectors found it on 61 cd.
		if mt == mtText && !simd.ValidUTF8(s) {
			return nil, ErrMalformed
		}
		return s, nil
	}
	if depth < 0 {
		return nil, ErrDepth
	}
	var out []byte
	for {
		if d.i >= len(d.b) {
			return nil, ErrTruncated
		}
		if d.b[d.i] == 0xff {
			d.i++
			break
		}
		cmt, _, carg, cindef, err := d.head()
		if err != nil {
			return nil, err
		}
		// Chunks must be definite and of the same major type: an indefinite
		// string may not nest, and a byte chunk may not appear inside a text
		// string.
		if cmt != mt || cindef {
			return nil, ErrMalformed
		}
		c, err := d.chunk(carg)
		if err != nil {
			return nil, err
		}
		if len(out)+len(c) > d.lim.MaxStringBytes {
			return nil, ErrTooLarge
		}
		out = append(out, c...)
	}
	if mt == mtText && !simd.ValidUTF8(out) {
		return nil, ErrMalformed
	}
	return out, nil
}

// chunk reads arg bytes, checking the claim against what is actually there
// before it is believed.
func (d *Decoder) chunk(arg uint64) ([]byte, error) {
	if arg > uint64(d.lim.MaxStringBytes) {
		return nil, ErrTooLarge
	}
	n := int(arg)
	if n < 0 || d.i+n > len(d.b) {
		return nil, ErrTruncated
	}
	s := d.b[d.i : d.i+n]
	d.i += n
	return s, nil
}

func (d *Decoder) array(arg uint64, indef bool, depth int) (value.Value, error) {
	if depth < 0 {
		return value.Value{}, ErrDepth
	}
	if indef {
		var out []value.Value
		for {
			done, err := d.atBreak()
			if err != nil {
				return value.Value{}, err
			}
			if done {
				return value.FromArray(out...).AsIndefinite(), nil
			}
			v, err := d.decode(depth - 1)
			if err != nil {
				return value.Value{}, err
			}
			if len(out) >= d.lim.MaxArrayElements {
				return value.Value{}, ErrTooLarge
			}
			out = append(out, v)
		}
	}
	if arg > uint64(d.lim.MaxArrayElements) {
		return value.Value{}, ErrTooLarge
	}
	// Each element needs at least one byte, so a count larger than the
	// remaining input is a lie and is refused before anything is sized from it.
	if arg > uint64(len(d.b)-d.i) {
		return value.Value{}, ErrTruncated
	}
	out := make([]value.Value, 0, arg)
	for k := uint64(0); k < arg; k++ {
		v, err := d.decode(depth - 1)
		if err != nil {
			return value.Value{}, err
		}
		out = append(out, v)
	}
	return value.FromArray(out...), nil
}

func (d *Decoder) mapValue(arg uint64, indef bool, depth int) (value.Value, error) {
	if depth < 0 {
		return value.Value{}, ErrDepth
	}
	if indef {
		var out []value.KeyValue
		for {
			done, err := d.atBreak()
			if err != nil {
				return value.Value{}, err
			}
			if done {
				m, err := d.applyDuplicatePolicy(out)
				if err != nil {
					return value.Value{}, err
				}
				return m.AsIndefinite(), nil
			}
			kv, err := d.pair(depth)
			if err != nil {
				return value.Value{}, err
			}
			if len(out) >= d.lim.MaxMapPairs {
				return value.Value{}, ErrTooLarge
			}
			out = append(out, kv)
		}
	}
	if arg > uint64(d.lim.MaxMapPairs) {
		return value.Value{}, ErrTooLarge
	}
	if arg > uint64(len(d.b)-d.i)/2 { // a pair is at least two bytes
		return value.Value{}, ErrTruncated
	}
	out := make([]value.KeyValue, 0, arg)
	for k := uint64(0); k < arg; k++ {
		kv, err := d.pair(depth)
		if err != nil {
			return value.Value{}, err
		}
		out = append(out, kv)
	}
	return d.applyDuplicatePolicy(out)
}

// applyDuplicatePolicy resolves keys that are the same key.
//
// CBOR permits duplicates on the wire, so a decoder has to choose. Leaving the
// choice implicit is the one option that is not available: a Go map silently
// keeps whichever entry was written last, and a caller has no way to learn
// that a key was ever repeated.
//
// Sameness is canonical-encoding equality, so the two spellings of 1.0 are one
// key even though the bytes differ.
func (d *Decoder) applyDuplicatePolicy(kvs []value.KeyValue) (value.Value, error) {
	if d.dup == DuplicateKeep || len(kvs) < 2 {
		return value.FromMap(kvs...), nil
	}
	seen := make(map[string]int, len(kvs))
	out := make([]value.KeyValue, 0, len(kvs))
	for _, kv := range kvs {
		k, err := value.KeyString(kv.Key, d.keys)
		if err != nil {
			// A key the mode does not allow; the value layer reports it.
			return value.Value{}, ErrMalformed
		}
		if at, dup := seen[k]; dup {
			switch d.dup {
			case DuplicateError:
				return value.Value{}, ErrDuplicateKey
			case DuplicateFirstWins:
				continue
			case DuplicateLastWins:
				out[at] = kv
				continue
			}
		}
		seen[k] = len(out)
		out = append(out, kv)
	}
	return value.FromMap(out...), nil
}

func (d *Decoder) pair(depth int) (value.KeyValue, error) {
	k, err := d.decode(depth - 1)
	if err != nil {
		return value.KeyValue{}, err
	}
	v, err := d.decode(depth - 1)
	if err != nil {
		return value.KeyValue{}, err
	}
	return value.KeyValue{Key: k, Value: v}, nil
}

// atBreak consumes a break stop-code if one is next.
func (d *Decoder) atBreak() (bool, error) {
	if d.i >= len(d.b) {
		return false, ErrTruncated
	}
	if d.b[d.i] == 0xff {
		d.i++
		return true, nil
	}
	return false, nil
}
