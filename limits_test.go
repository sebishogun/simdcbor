package simdcbor

import (
	"testing"
	"time"
)

// nest builds n nested single-element arrays around a 0, so depth is the only
// variable.
func nest(n int) []byte {
	b := make([]byte, 0, n+1)
	for i := 0; i < n; i++ {
		b = append(b, 0x81) // array(1)
	}
	return append(b, 0x00)
}

// The depth cap is what stops a nested document from taking the goroutine
// stack with it. It is a cap, so the edge is the interesting part: the last
// accepted depth and the first rejected one, both pinned.
func TestDepthCap(t *testing.T) {
	for _, c := range []struct {
		depth int
		ok    bool
	}{
		{1, true}, {32, true}, {64, true}, {65, false}, {128, false}, {1000, false},
	} {
		b := nest(c.depth)
		_, _, err := Unmarshal(b)
		if (err == nil) != c.ok {
			t.Errorf("depth %d: Unmarshal err=%v, want ok=%v", c.depth, err, c.ok)
		}
		_, serr := Skip(b)
		if (serr == nil) != c.ok {
			t.Errorf("depth %d: Skip err=%v, want ok=%v", c.depth, serr, c.ok)
		}
	}
}

// A header may claim more items than the input can hold. Believing it is how a
// decoder allocates gigabytes from a ten-byte input, so the claim is checked
// against what is actually there before anything is sized from it.
func TestPresizeBounded(t *testing.T) {
	for _, c := range []struct {
		name string
		b    []byte
	}{
		{"array claims 2^32-1", []byte{0x9a, 0xff, 0xff, 0xff, 0xff}},
		{"array claims 2^64-1", []byte{0x9b, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}},
		{"map claims 2^32-1", []byte{0xba, 0xff, 0xff, 0xff, 0xff}},
		{"map claims 2^64-1", []byte{0xbb, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}},
		{"bytes claim 2^64-1", []byte{0x5b, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}},
		{"text claims 2^64-1", []byte{0x7b, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}},
		{"array of 2^32 in 10 bytes", []byte{0x9a, 0xff, 0xff, 0xff, 0xff, 0, 0, 0, 0, 0}},
	} {
		t.Run(c.name, func(t *testing.T) {
			done := make(chan struct{})
			go func() {
				defer close(done)
				if _, _, err := Unmarshal(c.b); err == nil {
					t.Errorf("Unmarshal accepted a header claiming more than the input holds")
				}
				if _, err := Skip(c.b); err == nil {
					t.Errorf("Skip accepted a header claiming more than the input holds")
				}
			}()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("decode did not return; the claimed size was believed")
			}
		})
	}
}

// Every prefix of a valid item is incomplete, so every prefix must error.
// A prefix that decodes cleanly is a decoder inventing the rest.
func TestTruncationNeverDecodesClean(t *testing.T) {
	for _, full := range corpusSeeds() {
		if len(full) < 2 {
			continue
		}
		if _, _, err := Unmarshal(full); err != nil {
			continue // not a valid item; nothing to truncate
		}
		for i := 1; i < len(full); i++ {
			if _, n, err := Unmarshal(full[:i]); err == nil && n == i {
				t.Errorf("%x truncated to %d bytes decoded cleanly", full, i)
			}
		}
	}
}
