package simdcbor

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

// The adapter must produce exactly what the hand-written implementation
// produced. The snapshots in testdata/adapter were taken from the shipped code
// before it was replaced, so this compares against what shipped rather than
// against what the replacement is supposed to do -- which is the only
// comparison that can catch a change nobody intended.

func TestAdapterMarshalIsByteIdentical(t *testing.T) {
	want, err := os.ReadFile("testdata/adapter/marshal.txt")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(want), "\n"), "\n")
	corpus := adapterCorpus()
	if len(lines) != len(corpus) {
		t.Fatalf("snapshot has %d entries, corpus has %d", len(lines), len(corpus))
	}
	for i, v := range corpus {
		b, err := Marshal(v)
		got := hex.EncodeToString(b)
		if err != nil {
			got = "ERR " + err.Error()
		}
		if got != lines[i] {
			t.Errorf("corpus[%d] (%T %v): %s, shipped %s", i, v, v, got, lines[i])
		}
	}
}

func TestAdapterUnmarshalIsShapeIdentical(t *testing.T) {
	want, err := os.ReadFile("testdata/adapter/unmarshal.txt")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(want), "\n"), "\n")
	corpus := adapterDecodeCorpus()
	if len(lines) != len(corpus) {
		t.Fatalf("snapshot has %d entries, corpus has %d", len(lines), len(corpus))
	}
	for i, h := range corpus {
		b, _ := hex.DecodeString(h)
		v, n, err := Unmarshal(b)
		var got string
		if err != nil {
			got = h + " ERR " + err.Error()
		} else {
			got = h + " " + goSyntax(v) + " n=" + itoa(n)
		}
		if got != lines[i] {
			t.Errorf("corpus[%d]:\n got %s\n shipped %s", i, got, lines[i])
		}
	}
}

// The shipped shapes, stated directly rather than only through the snapshot.
func TestAdapterShapes(t *testing.T) {
	for _, c := range []struct {
		hex  string
		want string
	}{
		{"a26161016162820203", `map[a:1 b:[2 3]]`},
		{"83010203", "[1 2 3]"},
		{"f6", "nil"},
		{"f7", "nil"},        // undefined and null both become nil: Go has one
		{"f5", "bool(true)"}, // goSyntax spells a bool with its type; the snapshot uses the same helper
		{"00", "0"},
		{"fb400921fb54442d18", "3.141592653589793"},
		{"c11a514b67b0", "1.36389624e+09"}, // the tag is dropped
	} {
		b, _ := hex.DecodeString(c.hex)
		v, _, err := Unmarshal(b)
		if err != nil {
			t.Errorf("%s: %v", c.hex, err)
			continue
		}
		if got := goSyntax(v); got != c.want {
			t.Errorf("%s: %s, want %s", c.hex, got, c.want)
		}
	}
}

// What the shipped API refuses, and why each refusal is the shape's limit
// rather than CBOR's.
func TestAdapterRefusals(t *testing.T) {
	for _, c := range []struct {
		hex string
		why string
	}{
		{"a1010102", "an integer map key has no place in map[string]any"},
		{"e0", "simple(0) has no Go equivalent that round-trips"},
		{"f820", "nor does simple(32)"},
		{"61cd", "invalid UTF-8 is not a Go string"},
	} {
		b, _ := hex.DecodeString(c.hex)
		if _, _, err := Unmarshal(b); err != ErrMalformed {
			t.Errorf("%s (%s): err=%v", c.hex, c.why, err)
		}
	}
	// Depth is the shipped 64.
	deep := make([]byte, 0, 200)
	for i := 0; i < 100; i++ {
		deep = append(deep, 0x81)
	}
	deep = append(deep, 0x00)
	if _, _, err := Unmarshal(deep); err != ErrMalformed {
		t.Errorf("depth: %v", err)
	}
}

// The boundary that started this: SkipStrict and Unmarshal agree, and Skip
// accepts a superset. Both are now properties of one walk with one projection
// rather than of two implementations kept in step by hand.
func TestAdapterBoundaryParity(t *testing.T) {
	for h := 0; h < 256; h++ {
		b := []byte{byte(h), 0, 0, 0, 0, 0, 0, 0, 0, 0}
		_, un, uerr := Unmarshal(b)
		sn, serr := SkipStrict(b)
		if (uerr == nil) != (serr == nil) {
			t.Errorf("head %02x: unmarshal %v, skipstrict %v", h, uerr, serr)
			continue
		}
		if uerr == nil && un != sn {
			t.Errorf("head %02x: consumed %d, span %d", h, un, sn)
		}
		if uerr == nil {
			fn, ferr := Skip(b)
			if ferr != nil || fn != un {
				t.Errorf("head %02x: skip %d %v, want %d nil", h, fn, ferr, un)
			}
		}
	}
	for p := 0; p < 256; p++ {
		b := []byte{0xf8, byte(p), 0}
		_, un, uerr := Unmarshal(b)
		sn, serr := SkipStrict(b)
		if (uerr == nil) != (serr == nil) {
			t.Errorf("f8 %02x: unmarshal %v, skipstrict %v", p, uerr, serr)
			continue
		}
		if uerr == nil && un != sn {
			t.Errorf("f8 %02x: consumed %d, span %d", p, un, sn)
		}
	}
}
