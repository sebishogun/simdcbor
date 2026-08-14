package codec

import (
	"bytes"
	"testing"

	"github.com/sebishogun/simdcbor/value"
)

// recordStream builds n small maps, the shape a filter runs over.
func recordStream(t testing.TB, n int) []byte {
	t.Helper()
	e := NewEncoder(nil)
	for i := 0; i < n; i++ {
		must2(t, e.StartMap(3))
		must2(t, e.WriteText("id"))
		must2(t, e.WriteUint(uint64(i)))
		must2(t, e.WriteText("kind"))
		must2(t, e.WriteUint(uint64(i%100)))
		must2(t, e.WriteText("payload"))
		must2(t, e.WriteBytes([]byte("....................")))
		must2(t, e.EndMap())
	}
	b, err := e.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func must2(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestLazyFramesEachItem(t *testing.T) {
	e := NewEncoder(nil)
	must2(t, e.StartArray(5000))
	for i := 0; i < 5000; i++ {
		must2(t, e.WriteUint(uint64(i)))
	}
	must2(t, e.EndArray())
	b, err := e.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	d := New(b, Limits{})
	raws, err := d.RawArray()
	if err != nil {
		t.Fatal(err)
	}
	if len(raws) != 5000 {
		t.Fatalf("framed %d items, want 5000", len(raws))
	}
	// Materializing one item decodes exactly that item.
	v, err := raws[4999].Decode(Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := v.(value.Value).AsUint(); n != 4999 {
		t.Fatalf("last item decoded as %v", v)
	}
	// The frames are subslices of the caller's buffer, not copies.
	if raws[0].Pinned() {
		t.Fatal("a buffer-backed frame was copied")
	}
	if &raws[10].Bytes()[0] != &b[frameOffset(t, b, 10)] {
		t.Fatal("a frame does not point into the input buffer")
	}
}

// frameOffset walks the array head plus n items to find where item n starts,
// independently of RawArray, so the aliasing check above has a second opinion.
func frameOffset(t *testing.T, b []byte, n int) int {
	t.Helper()
	d := New(b, Limits{})
	if _, _, _, _, err := d.head(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if _, err := d.Skip(); err != nil {
			t.Fatal(err)
		}
	}
	return d.Offset()
}

// Framing a stream of records must not allocate per record. That is the whole
// claim of the lazy path: a filter reads the bytes it needs and touches
// nothing else.
func TestLazyFramingAllocatesNothingPerRecord(t *testing.T) {
	b := recordStream(t, 10000)
	n := testing.AllocsPerRun(20, func() {
		d := New(b, Limits{})
		count := 0
		for d.More() {
			r, err := d.RawNext()
			if err != nil {
				t.Fatal(err)
			}
			count += r.Len()
		}
		if count != len(b) {
			t.Fatalf("framed %d bytes of %d", count, len(b))
		}
	})
	// One allocation for the Decoder itself is expected; per-record would be
	// 10,000 more.
	if n > 4 {
		t.Fatalf("%v allocations to frame 10,000 records", n)
	}
}

// A reader-backed decoder refuses to lend its buffer, because the next refill
// overwrites it and the caller has no way to notice.
func TestLazyReaderNeedsPinCopy(t *testing.T) {
	b := recordStream(t, 50)
	s := NewStream(bytes.NewReader(b), Limits{})
	if _, err := s.RawNext(); err != ErrLazyUnavailable {
		t.Fatalf("err=%v, want ErrLazyUnavailable", err)
	}
	s = NewStream(bytes.NewReader(b), Limits{})
	s.SetPinCopy(true)
	r, err := s.RawNext()
	if err != nil {
		t.Fatalf("pin-copy: %v", err)
	}
	if !r.Pinned() {
		t.Fatal("pin-copy returned a borrowed frame")
	}
	v, err := r.Decode(Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v.(value.Value).AsMap(); !ok {
		t.Fatal("the pinned frame does not decode to the record")
	}
}

// Filter-then-read: frame everything, decode the one in a hundred that
// matters. The property is that the frames span the whole input exactly, so
// nothing is skipped or double-counted.
func TestLazyFilterThenRead(t *testing.T) {
	b := recordStream(t, 2000)
	d := New(b, Limits{})
	decoded, framed := 0, 0
	for d.More() {
		r, err := d.RawNext()
		if err != nil {
			t.Fatal(err)
		}
		framed++
		if framed%100 == 0 {
			v, err := r.Decode(Limits{})
			if err != nil {
				t.Fatal(err)
			}
			m, ok := v.(value.Value).AsMap()
			if !ok || len(m) != 3 {
				t.Fatalf("record %d decoded to %v", framed, v)
			}
			decoded++
		}
	}
	if framed != 2000 || decoded != 20 {
		t.Fatalf("framed %d, decoded %d", framed, decoded)
	}
}
