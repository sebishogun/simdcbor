package codec

import (
	"bytes"
	"encoding/hex"
	"io"
	"testing"

	"github.com/sebishogun/simdcbor/value"
)

// oneByteReader delivers a byte at a time, so every item in the stream is
// truncated at some point and every refill path is exercised. A stream that
// only works when a whole item happens to arrive at once is not a stream.
type oneByteReader struct {
	b []byte
	i int
}

func (r *oneByteReader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	p[0] = r.b[r.i]
	r.i++
	return 1, nil
}

func streamHex(t *testing.T, items ...string) []byte {
	t.Helper()
	var out []byte
	for _, h := range items {
		b, err := hex.DecodeString(h)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, b...)
	}
	return out
}

func TestStreamReadsEveryItem(t *testing.T) {
	items := []string{"01", "6449455446", "83010203", "bf61610161629f0203ffff", "fb400921fb54442d18", "f6"}
	data := streamHex(t, items...)
	for _, name := range []string{"whole", "one-byte"} {
		t.Run(name, func(t *testing.T) {
			var r io.Reader = bytes.NewReader(data)
			if name == "one-byte" {
				r = &oneByteReader{b: data}
			}
			s := NewStream(r, Limits{})
			var kinds []value.Kind
			for s.More() {
				v, err := s.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatalf("item %d: %v", len(kinds), err)
				}
				kinds = append(kinds, v.Kind())
			}
			want := []value.Kind{value.Uint, value.Text, value.Array, value.Map, value.Float64, value.SimpleKind}
			if len(kinds) != len(want) {
				t.Fatalf("%d items, want %d (%v)", len(kinds), len(want), kinds)
			}
			for i := range want {
				if kinds[i] != want[i] {
					t.Fatalf("item %d is %v, want %v", i, kinds[i], want[i])
				}
			}
		})
	}
}

// Truncation at the end of the reader is an error, not an EOF: the item was
// promised and never arrived, and reporting EOF would present a partial stream
// as a complete one.
func TestStreamTruncatedAtEOF(t *testing.T) {
	data := streamHex(t, "01", "8301") // the second item is short
	s := NewStream(&oneByteReader{b: data}, Limits{})
	if _, err := s.Next(); err != nil {
		t.Fatalf("first item: %v", err)
	}
	if _, err := s.Next(); err != ErrTruncated {
		t.Fatalf("second item: err=%v, want ErrTruncated", err)
	}
}

// Malformed is not a refill condition. A stream that retried on it would wait
// for bytes that cannot help.
func TestStreamMalformedDoesNotWaitForMore(t *testing.T) {
	data := streamHex(t, "1c", "01") // reserved ai, then a valid item
	s := NewStream(&oneByteReader{b: data}, Limits{})
	if _, err := s.Next(); err != ErrMalformed {
		t.Fatalf("err=%v, want ErrMalformed", err)
	}
}

func TestStreamSkipNext(t *testing.T) {
	data := streamHex(t, "83010203", "6449455446", "01")
	s := NewStream(&oneByteReader{b: data}, Limits{})
	n, err := s.SkipNext()
	if err != nil || n != 4 {
		t.Fatalf("skip: %d, %v", n, err)
	}
	v, err := s.Next()
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := v.AsText(); got != "IETF" {
		t.Fatalf("after skipping, got %q", got)
	}
}

// The buffer holds one item, not the stream: bytes already delivered are
// compacted away, so a long stream of small items does not grow it.
func TestStreamBufferDoesNotGrowWithTheStream(t *testing.T) {
	var data []byte
	for i := 0; i < 20000; i++ {
		data = append(data, 0x64, 'I', 'E', 'T', 'F')
	}
	s := NewStream(bytes.NewReader(data), Limits{})
	n := 0
	for s.More() {
		if _, err := s.Next(); err != nil {
			break
		}
		n++
	}
	if n != 20000 {
		t.Fatalf("read %d items, want 20000", n)
	}
	if len(s.buf) > 1<<16 {
		t.Fatalf("the buffer grew to %d bytes for 5-byte items", len(s.buf))
	}
}

func TestStreamRefusesAnItemLargerThanItsBuffer(t *testing.T) {
	// A head claiming 1 MiB of text, with the body never arriving.
	head := []byte{0x7a, 0x00, 0x10, 0x00, 0x00}
	body := make([]byte, 4096)
	s := NewStream(bytes.NewReader(append(head, body...)), Limits{})
	s.SetMaxBuffer(8192)
	if _, err := s.Next(); err == nil {
		t.Fatal("an item larger than the buffer decoded")
	}
}
