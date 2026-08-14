package codec

import "errors"

// Lazy values: frame an item without building it, decode later if at all.
//
// The shape this exists for is filtering. A stream of records where one in a
// hundred matters costs, today, either a full decode of every record or a skip
// that discards the bytes it stepped over. Framing keeps the bytes: the walk
// records where each item starts and ends, and a caller decodes only the ones
// it wants, from the buffer it already has.
//
// The borrowed subslice is the point and the hazard. It costs no copy and no
// allocation, and it is only valid while the input buffer is unmodified and
// alive. A reader-backed decoder therefore refuses to hand one out unless the
// caller asks for pinned copies, because the stream's buffer is reused under
// it by design.

// ErrLazyUnavailable means a raw message was requested from a reader-backed
// decoder whose buffer is reused. Enable pin-copy mode to get copies instead.
var ErrLazyUnavailable = errors.New("simdcbor: raw messages need a stable buffer or pin-copy mode")

// RawMessage is an undecoded item.
type RawMessage struct {
	b      []byte
	pinned bool
}

// Bytes returns the item's encoded form.
//
// When the message came from a buffer-backed decoder these are a subslice of
// the caller's own input: no copy, valid as long as that buffer is unmodified.
// Mutating the input after framing changes what a RawMessage means, which is a
// caller bug rather than something this package can detect.
func (r RawMessage) Bytes() []byte { return r.b }

// Len returns the encoded length.
func (r RawMessage) Len() int { return len(r.b) }

// Pinned reports whether the bytes are a private copy rather than borrowed.
func (r RawMessage) Pinned() bool { return r.pinned }

// Decode builds the value this message holds.
func (r RawMessage) Decode(lim Limits) (v any, err error) {
	d := New(r.b, lim)
	return d.Decode()
}

// SetPinCopy makes raw messages copy their bytes instead of borrowing them.
// A reader-backed decoder needs this, since its buffer is reused.
func (d *Decoder) SetPinCopy(on bool) { d.pin = on }

// RawNext frames the next item without building it.
//
// It is the decode walk with the build step replaced by range capture, so an
// item it accepts is an item Decode would build, and the span is the same.
func (d *Decoder) RawNext() (RawMessage, error) {
	start := d.i
	if err := d.skip(d.lim.MaxDepth); err != nil {
		return RawMessage{}, err
	}
	b := d.b[start:d.i]
	if d.pin {
		b = append([]byte(nil), b...)
		return RawMessage{b: b, pinned: true}, nil
	}
	if d.borrowUnsafe {
		return RawMessage{}, ErrLazyUnavailable
	}
	return RawMessage{b: b}, nil
}

// RawArray frames every element of an array without building any of them.
//
// The returned slice is reused across calls on the same decoder, so a caller
// that keeps it past the next call gets the next call's answer. Copy it if
// that matters -- the alternative is an allocation per array, which is the
// cost this whole path exists to avoid.
func (d *Decoder) RawArray() ([]RawMessage, error) {
	mt, _, arg, indef, err := d.head()
	if err != nil {
		return nil, err
	}
	if mt != mtArray {
		return nil, ErrMalformed
	}
	d.raws = d.raws[:0]
	if indef {
		for {
			done, err := d.atBreak()
			if err != nil {
				return nil, err
			}
			if done {
				return d.raws, nil
			}
			r, err := d.RawNext()
			if err != nil {
				return nil, err
			}
			d.raws = append(d.raws, r)
		}
	}
	if arg > uint64(d.lim.MaxArrayElements) {
		return nil, ErrTooLarge
	}
	if arg > uint64(len(d.b)-d.i) {
		return nil, ErrTruncated
	}
	for k := uint64(0); k < arg; k++ {
		r, err := d.RawNext()
		if err != nil {
			return nil, err
		}
		d.raws = append(d.raws, r)
	}
	return d.raws, nil
}
