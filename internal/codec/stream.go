package codec

import (
	"errors"
	"io"

	"github.com/sebishogun/simdcbor/value"
)

// A reader-backed decoder: items arrive across reads, so an item that is
// truncated at the buffer's end is not an error, it is a refill.
//
// That distinction is the whole design. ErrTruncated means "more bytes might
// complete this" and ErrMalformed means "they will not", and the two are
// separate errors precisely so a stream can act on the difference instead of
// guessing.

// Stream decodes a sequence of items from a reader.
type Stream struct {
	r    io.Reader
	buf  []byte
	off  int // where the next item starts
	end  int // how much of buf holds data
	lim  Limits
	err  error // sticky read error, delivered after the buffered items
	max  int
	done bool
}

// DefaultMaxBuffer caps how much one item may hold in the stream's buffer. An
// item larger than this cannot be decoded from a stream at all, which is the
// point: the alternative is growing without limit on the peer's say-so.
const DefaultMaxBuffer = 64 << 20

// NewStream returns a Stream reading from r.
func NewStream(r io.Reader, lim Limits) *Stream {
	return &Stream{r: r, lim: lim.withDefaults(), buf: make([]byte, 0, 4096), max: DefaultMaxBuffer}
}

// SetMaxBuffer caps the stream's buffer.
func (s *Stream) SetMaxBuffer(n int) { s.max = n }

// More reports whether another item might follow. It is false only once the
// buffer is empty and the reader is done, so a caller loops on it without
// having to distinguish "no bytes yet" from "no bytes ever".
func (s *Stream) More() bool {
	if s.off < s.end {
		return true
	}
	if s.done {
		return false
	}
	if err := s.fill(); err != nil {
		return s.off < s.end
	}
	return s.off < s.end
}

// Next decodes the next item, refilling as needed.
func (s *Stream) Next() (value.Value, error) {
	for {
		if s.off < s.end {
			d := New(s.buf[s.off:s.end], s.lim)
			val, derr := d.Decode()
			if derr == nil {
				s.off += d.Offset()
				return val, nil
			}
			// Only truncation is worth another read. Malformed stays malformed
			// however many bytes follow it.
			if derr != ErrTruncated {
				return value.Value{}, derr
			}
			if s.done {
				return value.Value{}, ErrTruncated
			}
		} else if s.done {
			return value.Value{}, io.EOF
		}
		if err := s.fill(); err != nil {
			if s.off >= s.end {
				return value.Value{}, err
			}
		}
	}
}

// SkipNext advances past the next item without building it.
func (s *Stream) SkipNext() (int, error) {
	for {
		if s.off < s.end {
			d := New(s.buf[s.off:s.end], s.lim)
			n, derr := d.Skip()
			if derr == nil {
				s.off += n
				return n, nil
			}
			if derr != ErrTruncated {
				return 0, derr
			}
			if s.done {
				return 0, ErrTruncated
			}
		} else if s.done {
			return 0, io.EOF
		}
		if err := s.fill(); err != nil {
			if s.off >= s.end {
				return 0, err
			}
		}
	}
}

// fill compacts the buffer and reads more.
func (s *Stream) fill() error {
	if s.err != nil {
		s.done = true
		if s.err == io.EOF {
			return io.EOF
		}
		return s.err
	}
	// Compact first: the bytes before off are items already delivered, so
	// holding them would grow the buffer for the length of the stream rather
	// than the length of an item.
	if s.off > 0 {
		copy(s.buf, s.buf[s.off:s.end])
		s.end -= s.off
		s.off = 0
	}
	if s.end == len(s.buf) {
		if len(s.buf) >= s.max {
			return errors.New("simdcbor: item larger than the stream buffer")
		}
		grow := len(s.buf) * 2
		if grow == 0 {
			grow = 4096
		}
		if grow > s.max {
			grow = s.max
		}
		nb := make([]byte, grow)
		copy(nb, s.buf[:s.end])
		s.buf = nb
	}
	s.buf = s.buf[:cap(s.buf)]
	n, err := s.r.Read(s.buf[s.end:])
	s.end += n
	if err != nil {
		s.err = err
		if n == 0 {
			s.done = true
			return err
		}
	}
	return nil
}
