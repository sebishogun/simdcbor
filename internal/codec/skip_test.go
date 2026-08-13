package codec

import (
	"encoding/hex"
	"math/rand"
	"testing"
)

func skipSpan(b []byte) (int, error) {
	d := New(b, Limits{})
	return d.Skip()
}

func decodeSpan(b []byte) (int, error) {
	d := New(b, Limits{})
	if _, err := d.Decode(); err != nil {
		return 0, err
	}
	return d.Offset(), nil
}

// The whole head space, and the two-byte simple form for every payload. This
// is the test the shipped package did not have: its generated corpus never
// produced the values that diverged, and its random loop discarded both
// errors, so four divergences lived there undisturbed.
//
// Here the two walks share a package and a head reader, so the property is
// structural rather than maintained -- but it is asserted anyway, because
// "shared by construction" is a claim like any other.
func TestSkipAgreesWithDecodeOnEveryHead(t *testing.T) {
	for h := 0; h < 256; h++ {
		b := []byte{byte(h), 0, 0, 0, 0, 0, 0, 0, 0, 0}
		dn, derr := decodeSpan(b)
		sn, serr := skipSpan(b)
		if (derr == nil) != (serr == nil) {
			t.Errorf("head %02x: decode err=%v, skip err=%v", h, derr, serr)
			continue
		}
		if derr == nil && dn != sn {
			t.Errorf("head %02x: decode consumed %d, skip spanned %d", h, dn, sn)
		}
	}
	for p := 0; p < 256; p++ {
		b := []byte{0xf8, byte(p), 0}
		dn, derr := decodeSpan(b)
		sn, serr := skipSpan(b)
		if (derr == nil) != (serr == nil) {
			t.Errorf("f8 %02x: decode err=%v, skip err=%v", p, derr, serr)
			continue
		}
		if derr == nil && dn != sn {
			t.Errorf("f8 %02x: decode consumed %d, skip spanned %d", p, dn, sn)
		}
	}
}

// The vectors, both ways.
func TestSkipParityOnVectors(t *testing.T) {
	for _, h := range []string{
		"00", "1bffffffffffffffff", "3bffffffffffffffff", "40", "4401020304",
		"60", "6449455446", "64f0908591", "80", "83010203", "9fff",
		"9f018202039f0405ffff", "a0", "a201020304", "bf61610161629f0203ffff",
		"5f42010243030405ff", "7f657374726561646d696e67ff",
		"c074323031332d30332d32315432303a30343a30305a", "c11a514b67b0",
		"f4", "f5", "f6", "f7", "e0", "f0", "f3", "f820", "f8ff",
		"f90000", "f93c00", "fa47c35000", "fb400921fb54442d18",
		// and the ones that must be refused by both
		"1c", "1f", "ff", "81ff", "f800", "61cd", "5f00ff", "7f42010243030405ff",
	} {
		t.Run(h, func(t *testing.T) {
			b, _ := hex.DecodeString(h)
			dn, derr := decodeSpan(b)
			sn, serr := skipSpan(b)
			if (derr == nil) != (serr == nil) {
				t.Fatalf("decode err=%v, skip err=%v", derr, serr)
			}
			if derr == nil && dn != sn {
				t.Fatalf("decode consumed %d, skip spanned %d", dn, sn)
			}
		})
	}
}

// The random-bytes loop the shipped package had, with the assertion it was
// missing: it discarded both errors to _, so the two walks could disagree on
// every input and it would still pass.
func TestSkipParityOnRandomBytes(t *testing.T) {
	rng := rand.New(rand.NewSource(20260814))
	buf := make([]byte, 24)
	for i := 0; i < 200000; i++ {
		n := 1 + rng.Intn(len(buf))
		for j := 0; j < n; j++ {
			buf[j] = byte(rng.Intn(256))
		}
		b := buf[:n]
		dn, derr := decodeSpan(b)
		sn, serr := skipSpan(b)
		if (derr == nil) != (serr == nil) {
			t.Fatalf("%x: decode err=%v, skip err=%v", b, derr, serr)
		}
		if derr == nil && dn != sn {
			t.Fatalf("%x: decode consumed %d, skip spanned %d", b, dn, sn)
		}
	}
}

// A generated corpus that reaches the values the shipped generator never
// produced: the full simple space, indefinite containers and strings, and tag
// keys.
func TestSkipParityOnGeneratedCorpus(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	var build func(depth int) []byte
	build = func(depth int) []byte {
		switch rng.Intn(12) {
		case 0:
			return []byte{byte(rng.Intn(24))}
		case 1:
			return []byte{0x20 | byte(rng.Intn(24))}
		case 2:
			return append([]byte{0x43}, byte(rng.Intn(256)), byte(rng.Intn(256)), byte(rng.Intn(256)))
		case 3:
			return []byte{0x62, 'a', 'b'}
		case 4: // the simple space, short form
			n := byte(rng.Intn(24))
			if n >= 24 {
				n = 23
			}
			return []byte{0xe0 | n}
		case 5: // the simple space, two-byte form
			return []byte{0xf8, byte(32 + rng.Intn(224))}
		case 6:
			return []byte{0xf9, byte(rng.Intn(256)), byte(rng.Intn(256))}
		case 7: // definite array
			if depth <= 0 {
				return []byte{0x80}
			}
			n := rng.Intn(3)
			out := []byte{0x80 | byte(n)}
			for i := 0; i < n; i++ {
				out = append(out, build(depth-1)...)
			}
			return out
		case 8: // indefinite array
			if depth <= 0 {
				return []byte{0x9f, 0xff}
			}
			out := []byte{0x9f}
			for i := 0; i < rng.Intn(3); i++ {
				out = append(out, build(depth-1)...)
			}
			return append(out, 0xff)
		case 9: // definite map, with keys of several kinds including tags
			if depth <= 0 {
				return []byte{0xa0}
			}
			n := rng.Intn(3)
			out := []byte{0xa0 | byte(n)}
			for i := 0; i < n; i++ {
				out = append(out, build(depth-1)...) // key: any kind
				out = append(out, build(depth-1)...)
			}
			return out
		case 10: // indefinite byte string
			out := []byte{0x5f}
			for i := 0; i < rng.Intn(3); i++ {
				out = append(out, 0x42, byte(rng.Intn(256)), byte(rng.Intn(256)))
			}
			return append(out, 0xff)
		default: // tag
			if depth <= 0 {
				return []byte{0xc0, 0x00}
			}
			return append([]byte{0xc0 | byte(rng.Intn(24))}, build(depth-1)...)
		}
	}
	for i := 0; i < 50000; i++ {
		b := build(3)
		dn, derr := decodeSpan(b)
		sn, serr := skipSpan(b)
		if (derr == nil) != (serr == nil) {
			t.Fatalf("%x: decode err=%v, skip err=%v", b, derr, serr)
		}
		if derr == nil && dn != sn {
			t.Fatalf("%x: decode consumed %d, skip spanned %d", b, dn, sn)
		}
	}
}
