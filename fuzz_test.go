package simdcbor

import (
	"testing"
	"time"
)

// corpusSeeds are RFC 8949 appendix A items plus the shapes that break
// decoders: truncations, reserved additional-information values, the
// indefinite-length forms, break bytes out of place, and the simple values on
// both sides of this package's accept boundary.
func corpusSeeds() [][]byte {
	return [][]byte{
		{0x00},                         // 0
		{0x01},                         // 1
		{0x17},                         // 23
		{0x18, 0x18},                   // 24
		{0x19, 0x03, 0xe8},             // 1000
		{0x1a, 0x00, 0x0f, 0x42, 0x40}, // 1000000
		{0x1b, 0x00, 0x00, 0x00, 0xe8, 0xd4, 0xa5, 0x10, 0x00}, // 1e12
		{0x20},                         // -1
		{0x38, 0x63},                   // -100
		{0x40},                         // h''
		{0x44, 0x01, 0x02, 0x03, 0x04}, // h'01020304'
		{0x60},                         // ""
		{0x61, 0x61},                   // "a"
		{0x64, 0x49, 0x45, 0x54, 0x46}, // "IETF"
		{0x80},                         // []
		{0x83, 0x01, 0x02, 0x03},       // [1,2,3]
		{0x82, 0x01, 0x82, 0x02, 0x03}, // nested arrays
		{0xa0},                         // {}
		{0xa1, 0x61, 0x61, 0x01},       // {"a":1}
		{0xa2, 0x61, 0x61, 0x01, 0x61, 0x62, 0x02}, // two pairs
		{0xa1, 0x00, 0x00},                         // integer key: outside the model
		{0xc0, 0x74, 0x32, 0x30, 0x31, 0x33},       // tag 0 + text
		{0xc1, 0x1a, 0x51, 0x4b, 0x67, 0xb0},       // tag 1 + epoch
		{0xf4}, {0xf5}, {0xf6}, {0xf7},             // false true null undefined
		{0xf9, 0x00, 0x00},                                     // half 0.0
		{0xfa, 0x47, 0xc3, 0x50, 0x00},                         // single 100000.0
		{0xfb, 0x40, 0x09, 0x21, 0xfb, 0x54, 0x44, 0x2d, 0x18}, // double pi
		{0xe0}, {0xf0}, {0xf3}, // simple 0, 16, 19
		{0xf8, 0x18}, {0xf8, 0xff}, // two-byte simple
		{0x1c}, {0x1d}, {0x1e}, {0x1f}, // reserved ai 28-31
		{0x5f, 0x42, 0x01, 0x02, 0xff}, // indefinite bytes
		{0x7f, 0x61, 0x61, 0xff},       // indefinite text
		{0x9f, 0x01, 0x02, 0xff},       // indefinite array
		{0xbf, 0x61, 0x61, 0x01, 0xff}, // indefinite map
		{0xff},                         // a lone break
		{0x82, 0x01},                   // array short one item
		{0xa1, 0x61, 0x61},             // map missing its value
		{0x64, 0x49, 0x45},             // text shorter than declared
		{0x9b, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, // array claiming 2^64-1 items
		{0x5b, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, // bytes claiming 2^64-1
		{}, // empty
	}
}

// FuzzUnmarshalNeverPanics: any input and every prefix of it must error or
// decode. Never a panic, never a hang, and Skip must agree with Unmarshal on
// which it is -- the property the whole-head-space test pins for single items,
// asserted here over arbitrary structure.
func FuzzUnmarshalNeverPanics(f *testing.F) {
	for _, seed := range corpusSeeds() {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		done := make(chan struct{})
		go func() {
			defer close(done)
			// Every prefix, so truncation at any offset is covered rather than
			// only truncation the mutator happens to produce.
			for i := 0; i <= len(data) && i <= 512; i++ {
				p := data[:i]
				_, un, uerr := Unmarshal(p)
				sn, serr := Skip(p)
				if (uerr == nil) != (serr == nil) {
					t.Errorf("prefix %d of %x: unmarshal err=%v, skip err=%v", i, data, uerr, serr)
					return
				}
				if uerr == nil && un != sn {
					t.Errorf("prefix %d of %x: consumed %d, span %d", i, data, un, sn)
					return
				}
			}
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("decode did not terminate on %x", data)
		}
	})
}
