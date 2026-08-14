package codec

import (
	"encoding/binary"
	"errors"
	"math"

	"github.com/sebishogun/simd"
	"github.com/sebishogun/simdcbor/value"
)

// Forward-append encoding with a container stack.
//
// The stack exists so a mistake is caught where it is made rather than where
// it is noticed. An End over the wrong container type, an End on an empty
// stack, or a definite array given more elements than it declared are all
// refused before a byte is written -- a half-written container cannot be
// unwound, and a caller that gets an error after the bytes are out has no
// recovery except to throw the buffer away.

var (
	// ErrContainerMismatch means an End did not match the container it closes.
	ErrContainerMismatch = errors.New("simdcbor: container mismatch")
	// ErrCountOverrun means a definite container was given more items than it
	// declared, or ended with fewer.
	ErrCountOverrun = errors.New("simdcbor: definite container count overrun")
	// ErrNotWellFormed means the caller asked for bytes that are not
	// well-formed CBOR, such as a reserved simple value.
	ErrNotWellFormed = errors.New("simdcbor: value has no well-formed encoding")
)

type frame struct {
	major     byte // mtArray, mtMap, mtBytes or mtText
	indefinit bool
	want      uint64 // declared item count for a definite container
	got       uint64 // items written so far (map pairs count as two)
	// marks holds the buffer offset where each item of this container starts,
	// recorded only for a map in a deterministic mode. Sorting keys means
	// moving the encoded pairs, and the offsets are what makes that possible
	// without re-encoding them.
	marks []int
}

// Encoder appends CBOR to a buffer.
type Encoder struct {
	buf   []byte
	stack []frame
	err   error
	mode  Mode
}

// NewEncoder returns an Encoder appending to buf, which may be nil.
func NewEncoder(buf []byte) *Encoder { return &Encoder{buf: buf} }

// Bytes returns what has been written. It reports an error if a container is
// still open, because a truncated document is not a document.
func (e *Encoder) Bytes() ([]byte, error) {
	if e.err != nil {
		return nil, e.err
	}
	if len(e.stack) != 0 {
		return nil, ErrContainerMismatch
	}
	return e.buf, nil
}

// Reset clears the encoder, keeping the buffer's capacity.
func (e *Encoder) Reset() {
	e.buf = e.buf[:0]
	e.stack = e.stack[:0]
	e.err = nil
}

// item records one item against the innermost container, which is how the
// definite-count check knows when a container is over-filled.
func (e *Encoder) item() error {
	if e.err != nil {
		return e.err
	}
	if n := len(e.stack); n > 0 {
		f := &e.stack[n-1]
		if f.major == mtBytes || f.major == mtText {
			return nil // chunks are counted by WriteChunk
		}
		if !f.indefinit {
			if f.got >= f.want {
				e.err = ErrCountOverrun
				return e.err
			}
		}
		if f.major == mtMap && e.mode.deterministic() {
			f.marks = append(f.marks, len(e.buf))
		}
		f.got++
	}
	return nil
}

func (e *Encoder) head(major byte, arg uint64) {
	m := major << 5
	switch {
	case arg < 24:
		e.buf = append(e.buf, m|byte(arg))
	case arg <= 0xff:
		e.buf = append(e.buf, m|24, byte(arg))
	case arg <= 0xffff:
		e.buf = append(e.buf, m|25, byte(arg>>8), byte(arg))
	case arg <= 0xffffffff:
		e.buf = append(e.buf, m|26)
		e.buf = binary.BigEndian.AppendUint32(e.buf, uint32(arg))
	default:
		e.buf = append(e.buf, m|27)
		e.buf = binary.BigEndian.AppendUint64(e.buf, arg)
	}
}

// WriteUint writes an unsigned integer.
func (e *Encoder) WriteUint(n uint64) error {
	if err := e.item(); err != nil {
		return err
	}
	e.head(mtUint, n)
	e.popTag()
	return nil
}

// WriteNegMagnitude writes the negative integer -1-n.
func (e *Encoder) WriteNegMagnitude(n uint64) error {
	if err := e.item(); err != nil {
		return err
	}
	e.head(mtNegInt, n)
	e.popTag()
	return nil
}

// WriteInt writes the integer i.
func (e *Encoder) WriteInt(i int64) error {
	if i < 0 {
		return e.WriteNegMagnitude(uint64(-(i + 1)))
	}
	return e.WriteUint(uint64(i))
}

// WriteBytes writes a definite byte string.
func (e *Encoder) WriteBytes(b []byte) error {
	if err := e.item(); err != nil {
		return err
	}
	e.head(mtBytes, uint64(len(b)))
	e.buf = append(e.buf, b...)
	e.popTag()
	return nil
}

// WriteText writes a definite text string. Invalid UTF-8 is refused here
// rather than emitted, because the decoder rejects it: an encoder that wrote
// it would produce a document this package cannot read back.
func (e *Encoder) WriteText(s string) error {
	if !simd.ValidUTF8([]byte(s)) {
		e.err = ErrNotWellFormed
		return e.err
	}
	if err := e.item(); err != nil {
		return err
	}
	e.head(mtText, uint64(len(s)))
	e.buf = append(e.buf, s...)
	e.popTag()
	return nil
}

// WriteTag writes a tag head. The next item written is the tagged value; a tag
// with nothing after it leaves the document truncated, which Bytes reports.
func (e *Encoder) WriteTag(number uint64) error {
	if err := e.item(); err != nil {
		return err
	}
	e.head(mtTag, number)
	// A tag and its content are one item to the enclosing container, and the
	// head has already been counted, so the content must not count again.
	e.stack = append(e.stack, frame{major: mtTag, want: 1})
	return nil
}

// WriteSimple writes a simple value, named or numeric.
func (e *Encoder) WriteSimple(n uint8) error {
	if n >= 24 && n <= 31 {
		e.err = ErrNotWellFormed // reserved: no encoding exists
		return e.err
	}
	if err := e.item(); err != nil {
		return err
	}
	if n < 24 {
		e.buf = append(e.buf, 0xe0|n)
	} else {
		e.buf = append(e.buf, 0xf8, n)
	}
	e.popTag()
	return nil
}

// WriteBool, WriteNull and WriteUndefined are the named simple values.
func (e *Encoder) WriteBool(b bool) error {
	if b {
		return e.WriteSimple(21)
	}
	return e.WriteSimple(20)
}
func (e *Encoder) WriteNull() error      { return e.WriteSimple(22) }
func (e *Encoder) WriteUndefined() error { return e.WriteSimple(23) }

// WriteFloat64 writes a double, narrowing only when the narrower width reads
// back bit-identical. -0.0 and NaN payloads therefore survive.
func (e *Encoder) WriteFloat64(f float64) error {
	if err := e.item(); err != nil {
		return err
	}
	bits := math.Float64bits(f)
	if e.mode.deterministic() {
		// Preferred serialization: the narrowest width that reads back
		// bit-identical, which is the same rule map-key identity uses.
		return e.writeShortestFloat(value.FromFloat64Bits(bits))
	}
	if b32 := math.Float32bits(float32(f)); math.Float64bits(float64(math.Float32frombits(b32))) == bits {
		e.buf = append(e.buf, 0xfa)
		e.buf = binary.BigEndian.AppendUint32(e.buf, b32)
	} else {
		e.buf = append(e.buf, 0xfb)
		e.buf = binary.BigEndian.AppendUint64(e.buf, bits)
	}
	e.popTag()
	return nil
}

// writeShortestFloat emits v at the narrowest width that reproduces its bits.
// The item has already been counted by the caller.
func (e *Encoder) writeShortestFloat(v value.Value) error {
	s := value.ShortestFloat(v)
	b, _ := s.FloatBits()
	switch s.Kind() {
	case value.Float16:
		e.buf = append(e.buf, 0xf9, byte(b>>8), byte(b))
	case value.Float32:
		e.buf = append(e.buf, 0xfa)
		e.buf = binary.BigEndian.AppendUint32(e.buf, uint32(b))
	default:
		e.buf = append(e.buf, 0xfb)
		e.buf = binary.BigEndian.AppendUint64(e.buf, b)
	}
	e.popTag()
	return nil
}

// WriteFloat16Bits, WriteFloat32Bits and WriteFloat64Bits write exact bits at
// a chosen width, for a re-encode that must reproduce what it read.
func (e *Encoder) WriteFloat16Bits(b uint16) error {
	if err := e.item(); err != nil {
		return err
	}
	if e.mode.deterministic() {
		return e.writeShortestFloat(value.FromFloat16Bits(b))
	}
	e.buf = append(e.buf, 0xf9, byte(b>>8), byte(b))
	e.popTag()
	return nil
}

func (e *Encoder) WriteFloat32Bits(b uint32) error {
	if err := e.item(); err != nil {
		return err
	}
	if e.mode.deterministic() {
		return e.writeShortestFloat(value.FromFloat32Bits(b))
	}
	e.buf = append(e.buf, 0xfa)
	e.buf = binary.BigEndian.AppendUint32(e.buf, b)
	e.popTag()
	return nil
}

func (e *Encoder) WriteFloat64Bits(b uint64) error {
	if err := e.item(); err != nil {
		return err
	}
	if e.mode.deterministic() {
		return e.writeShortestFloat(value.FromFloat64Bits(b))
	}
	e.buf = append(e.buf, 0xfb)
	e.buf = binary.BigEndian.AppendUint64(e.buf, b)
	e.popTag()
	return nil
}

// StartArray opens a definite array of n elements.
func (e *Encoder) StartArray(n uint64) error {
	if err := e.item(); err != nil {
		return err
	}
	e.head(mtArray, n)
	e.stack = append(e.stack, frame{major: mtArray, want: n})
	return nil
}

// StartMap opens a definite map of n pairs.
func (e *Encoder) StartMap(n uint64) error {
	if err := e.item(); err != nil {
		return err
	}
	e.head(mtMap, n)
	e.stack = append(e.stack, frame{major: mtMap, want: 2 * n})
	return nil
}

// StartIndefiniteArray, StartIndefiniteMap, StartIndefiniteBytes and
// StartIndefiniteText open a container whose length is not declared. The
// matching End is the only thing in this package that writes 0xff.
func (e *Encoder) StartIndefiniteArray() error { return e.startIndef(mtArray, 0x9f) }
func (e *Encoder) StartIndefiniteMap() error   { return e.startIndef(mtMap, 0xbf) }
func (e *Encoder) StartIndefiniteBytes() error { return e.startIndef(mtBytes, 0x5f) }
func (e *Encoder) StartIndefiniteText() error  { return e.startIndef(mtText, 0x7f) }

func (e *Encoder) startIndef(major byte, b byte) error {
	if err := e.item(); err != nil {
		return err
	}
	e.buf = append(e.buf, b)
	e.stack = append(e.stack, frame{major: major, indefinit: true})
	return nil
}

// WriteChunk appends one chunk of the open indefinite string.
func (e *Encoder) WriteChunk(b []byte) error {
	if e.err != nil {
		return e.err
	}
	n := len(e.stack)
	if n == 0 || !e.stack[n-1].indefinit ||
		(e.stack[n-1].major != mtBytes && e.stack[n-1].major != mtText) {
		e.err = ErrContainerMismatch
		return e.err
	}
	e.head(e.stack[n-1].major, uint64(len(b)))
	e.buf = append(e.buf, b...)
	e.stack[n-1].got++
	return nil
}

// EndArray, EndMap, EndBytes and EndText close the innermost container. The
// type is named rather than inferred so that closing the wrong one is an error
// instead of a differently-shaped document.
func (e *Encoder) EndArray() error { return e.end(mtArray) }
func (e *Encoder) EndMap() error   { return e.end(mtMap) }
func (e *Encoder) EndBytes() error { return e.end(mtBytes) }
func (e *Encoder) EndText() error  { return e.end(mtText) }

func (e *Encoder) end(major byte) error {
	if e.err != nil {
		return e.err
	}
	n := len(e.stack)
	if n == 0 {
		e.err = ErrContainerMismatch
		return e.err
	}
	f := e.stack[n-1]
	if f.major != major {
		e.err = ErrContainerMismatch
		return e.err
	}
	if !f.indefinit && f.got != f.want {
		// Short of the declared count: the document would claim items that are
		// not there, which a decoder reports as truncation somewhere else
		// entirely.
		e.err = ErrCountOverrun
		return e.err
	}
	e.stack = e.stack[:n-1]
	if f.major == mtMap && e.mode.deterministic() {
		if err := e.sortPairs(f); err != nil {
			e.err = err
			return err
		}
	}
	if f.indefinit {
		e.buf = append(e.buf, 0xff)
	}
	// An indefinite text string is validated as its concatenation, matching
	// the decoder; the chunk boundaries do not have to fall on rune
	// boundaries, so only the whole thing can be checked.
	if f.major == mtText && f.indefinit {
		if !e.validIndefiniteText() {
			e.err = ErrNotWellFormed
			return e.err
		}
	}
	e.popTag()
	return nil
}

// sortPairs reorders an encoded map's pairs into the mode's key order.
//
// It sorts the bytes already written rather than buffering values and encoding
// at the end, which keeps the streaming shape: a caller writes pairs as it has
// them, and only a map in a deterministic mode pays for the reordering.
func (e *Encoder) sortPairs(f frame) error {
	if len(f.marks) < 4 { // fewer than two pairs: nothing to order
		return nil
	}
	if len(f.marks)%2 != 0 {
		return ErrCountOverrun
	}
	type pair struct {
		key   []byte
		whole []byte
	}
	n := len(f.marks) / 2
	pairs := make([]pair, n)
	for i := 0; i < n; i++ {
		ks := f.marks[2*i]
		vs := f.marks[2*i+1]
		ve := len(e.buf)
		if i+1 < n {
			ve = f.marks[2*(i+1)]
		}
		pairs[i] = pair{
			key:   append([]byte(nil), e.buf[ks:vs]...),
			whole: append([]byte(nil), e.buf[ks:ve]...),
		}
	}
	// Insertion sort, stable: two entries with the same key keep their order,
	// so a duplicate-key check downstream still sees which came first.
	order := e.mode.order()
	for i := 1; i < n; i++ {
		for j := i; j > 0 && value.CompareKeys(pairs[j].key, pairs[j-1].key, order) < 0; j-- {
			pairs[j], pairs[j-1] = pairs[j-1], pairs[j]
		}
	}
	out := e.buf[:f.marks[0]]
	for _, p := range pairs {
		out = append(out, p.whole...)
	}
	e.buf = out
	return nil
}

// validIndefiniteText re-reads what was just written and validates it, which
// is the only way to check a concatenation assembled chunk by chunk.
func (e *Encoder) validIndefiniteText() bool {
	d := New(e.buf, DefaultLimits())
	// Walk to the last item: the buffer may hold a whole document.
	for {
		start := d.Offset()
		if _, err := d.Skip(); err != nil {
			return false
		}
		if !d.More() {
			_ = start
			return true
		}
	}
}

// popTag closes the pseudo-frame a tag pushes, now that its content is
// written. Tags do not nest as containers do -- a tag holds exactly one item --
// so every writer that completes an item calls this, and a chain of tags
// unwinds in one pass.
func (e *Encoder) popTag() {
	for len(e.stack) > 0 && e.stack[len(e.stack)-1].major == mtTag {
		e.stack = e.stack[:len(e.stack)-1]
	}
}

// WriteValue writes a whole value from the value model, reproducing widths and
// bits rather than normalizing them.
func (e *Encoder) WriteValue(v value.Value) error {
	switch v.Kind() {
	case value.Uint:
		n, _ := v.AsUint()
		return e.WriteUint(n)
	case value.NegInt:
		n, _ := v.AsNegMagnitude()
		return e.WriteNegMagnitude(n)
	case value.Bytes:
		b, _ := v.AsBytes()
		if v.Indefinite() && !e.mode.deterministic() {
			// One chunk holding the concatenation: the chunk boundaries the
			// original used are not recoverable from the value, and the RFC
			// does not make them meaningful.
			if err := e.StartIndefiniteBytes(); err != nil {
				return err
			}
			if len(b) > 0 {
				if err := e.WriteChunk(b); err != nil {
					return err
				}
			}
			return e.EndBytes()
		}
		return e.WriteBytes(b)
	case value.Text:
		s, _ := v.AsText()
		if v.Indefinite() && !e.mode.deterministic() {
			if err := e.StartIndefiniteText(); err != nil {
				return err
			}
			if len(s) > 0 {
				if err := e.WriteChunk([]byte(s)); err != nil {
					return err
				}
			}
			return e.EndText()
		}
		return e.WriteText(s)
	case value.Array:
		a, _ := v.AsArray()
		// The indefinite form is reproduced only where it is legal: a
		// deterministic encoding requires definite lengths, so the mode wins
		// over what the value remembers.
		if v.Indefinite() && !e.mode.deterministic() {
			if err := e.StartIndefiniteArray(); err != nil {
				return err
			}
			for _, el := range a {
				if err := e.WriteValue(el); err != nil {
					return err
				}
			}
			return e.EndArray()
		}
		if err := e.StartArray(uint64(len(a))); err != nil {
			return err
		}
		for _, el := range a {
			if err := e.WriteValue(el); err != nil {
				return err
			}
		}
		return e.EndArray()
	case value.Map:
		m, _ := v.AsMap()
		if v.Indefinite() && !e.mode.deterministic() {
			if err := e.StartIndefiniteMap(); err != nil {
				return err
			}
			for _, kv := range m {
				if err := e.WriteValue(kv.Key); err != nil {
					return err
				}
				if err := e.WriteValue(kv.Value); err != nil {
					return err
				}
			}
			return e.EndMap()
		}
		if err := e.StartMap(uint64(len(m))); err != nil {
			return err
		}
		for _, kv := range m {
			if err := e.WriteValue(kv.Key); err != nil {
				return err
			}
			if err := e.WriteValue(kv.Value); err != nil {
				return err
			}
		}
		return e.EndMap()
	case value.TagKind:
		n, inner, _ := v.AsTag()
		if err := e.WriteTag(n); err != nil {
			return err
		}
		return e.WriteValue(inner)
	case value.SimpleKind:
		n, _ := v.AsSimple()
		return e.WriteSimple(n)
	case value.Float16:
		b, _ := v.FloatBits()
		return e.WriteFloat16Bits(uint16(b))
	case value.Float32:
		b, _ := v.FloatBits()
		return e.WriteFloat32Bits(uint32(b))
	case value.Float64:
		b, _ := v.FloatBits()
		return e.WriteFloat64Bits(b)
	}
	e.err = ErrNotWellFormed
	return e.err
}
