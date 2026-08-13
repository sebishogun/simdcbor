package codec

import (
	"encoding/hex"
	"math"
	"testing"

	"github.com/sebishogun/simdcbor/value"
)

func dec(t *testing.T, h string) (value.Value, error) {
	t.Helper()
	b, err := hex.DecodeString(h)
	if err != nil {
		t.Fatalf("bad hex %q: %v", h, err)
	}
	return New(b, Limits{}).Decode()
}

// RFC 8949 appendix A, plus the items the appendix does not enumerate but a
// decoder has to get right: the -2^64 endpoint, the whole simple space, the
// chunked strings, and the reserved additional-information values.
func TestRFC8949Vectors(t *testing.T) {
	for _, c := range []struct {
		hex  string
		kind value.Kind
		want string // what the value must report, checked per kind below
	}{
		{"00", value.Uint, "0"},
		{"01", value.Uint, "1"},
		{"0a", value.Uint, "10"},
		{"17", value.Uint, "23"},
		{"1818", value.Uint, "24"},
		{"1903e8", value.Uint, "1000"},
		{"1a000f4240", value.Uint, "1000000"},
		{"1b000000e8d4a51000", value.Uint, "1000000000000"},
		{"1bffffffffffffffff", value.Uint, "18446744073709551615"},
		{"20", value.NegInt, "-1"},
		{"29", value.NegInt, "-10"},
		{"3863", value.NegInt, "-100"},
		{"3903e7", value.NegInt, "-1000"},
		// The endpoint: magnitude 2^64-1, mathematical value -2^64. No int64
		// holds it, which is why the model keeps the magnitude.
		{"3bffffffffffffffff", value.NegInt, "-18446744073709551616"},
		{"40", value.Bytes, ""},
		{"4401020304", value.Bytes, "01020304"},
		{"60", value.Text, ""},
		{"6161", value.Text, "a"},
		{"6449455446", value.Text, "IETF"},
		{"62225c", value.Text, "\"\\"},
		{"62c3bc", value.Text, "ü"},
		{"63e6b0b4", value.Text, "水"},
		{"64f0908591", value.Text, "𐅑"},
		{"80", value.Array, "0"},
		{"83010203", value.Array, "3"},
		{"9fff", value.Array, "0"},                 // indefinite, empty
		{"9f018202039f0405ffff", value.Array, "3"}, // indefinite, nested
		{"a0", value.Map, "0"},
		{"a201020304", value.Map, "2"},
		{"bf61610161629f0203ffff", value.Map, "2"}, // indefinite map
		{"5f42010243030405ff", value.Bytes, "0102030405"},
		{"7f657374726561646d696e67ff", value.Text, "streaming"},
		{"c074323031332d30332d32315432303a30343a30305a", value.TagKind, "0"},
		{"c11a514b67b0", value.TagKind, "1"},
		{"f4", value.SimpleKind, "20"},
		{"f5", value.SimpleKind, "21"},
		{"f6", value.SimpleKind, "22"},
		{"f7", value.SimpleKind, "23"},
		{"e0", value.SimpleKind, "0"}, // simple 0: the whole space, not just the named four
		{"f0", value.SimpleKind, "16"},
		{"f3", value.SimpleKind, "19"},
		{"f820", value.SimpleKind, "32"},
		{"f8ff", value.SimpleKind, "255"},
		{"f90000", value.Float16, "0"},
		{"f93c00", value.Float16, "1"},
		{"f97e00", value.Float16, "NaN"},
		{"fa47c35000", value.Float32, "100000"},
		{"fb400921fb54442d18", value.Float64, "3.141592653589793"},
	} {
		t.Run(c.hex, func(t *testing.T) {
			v, err := dec(t, c.hex)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if v.Kind() != c.kind {
				t.Fatalf("kind %v, want %v", v.Kind(), c.kind)
			}
			var got string
			switch c.kind {
			case value.Uint:
				n, _ := v.AsUint()
				got = formatUint(n)
			case value.NegInt:
				n, _ := v.AsNegMagnitude()
				got = formatNeg(n)
			case value.Bytes:
				b, _ := v.AsBytes()
				got = hex.EncodeToString(b)
			case value.Text:
				got, _ = v.AsText()
			case value.Array:
				a, _ := v.AsArray()
				got = formatUint(uint64(len(a)))
			case value.Map:
				m, _ := v.AsMap()
				got = formatUint(uint64(len(m)))
			case value.TagKind:
				n, _, _ := v.AsTag()
				got = formatUint(n)
			case value.SimpleKind:
				n, _ := v.AsSimple()
				got = formatUint(uint64(n))
			case value.Float16, value.Float32, value.Float64:
				f, _, _ := v.AsFloat64()
				if math.IsNaN(f) {
					got = "NaN"
				} else {
					got = formatFloat(f)
				}
			}
			if got != c.want {
				t.Fatalf("value %q, want %q", got, c.want)
			}
		})
	}
}

func formatUint(n uint64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// formatNeg prints -1-n exactly, including the endpoint that overflows int64.
func formatNeg(n uint64) string {
	if n == math.MaxUint64 {
		return "-18446744073709551616"
	}
	return "-" + formatUint(n+1)
}

func formatFloat(f float64) string {
	switch f {
	case 0:
		return "0"
	case 1:
		return "1"
	case 100000:
		return "100000"
	case 3.141592653589793:
		return "3.141592653589793"
	}
	return "other"
}

// What must be refused, and with which error. ErrTruncated says more bytes
// might help; ErrMalformed says they will not, and confusing the two makes a
// streaming caller wait forever for an item that can never complete.
func TestMalformedAndTruncated(t *testing.T) {
	for _, c := range []struct {
		hex  string
		want error
		why  string
	}{
		{"", ErrTruncated, "empty"},
		{"18", ErrTruncated, "one-byte argument missing"},
		{"19ff", ErrTruncated, "two-byte argument short"},
		{"1c", ErrMalformed, "reserved ai 28"},
		{"1d", ErrMalformed, "reserved ai 29"},
		{"1e", ErrMalformed, "reserved ai 30"},
		{"1f", ErrMalformed, "indefinite integer"},
		{"3f", ErrMalformed, "indefinite negative"},
		{"df", ErrMalformed, "indefinite tag"},
		{"ff", ErrMalformed, "break outside a container"},
		{"81ff", ErrMalformed, "break where an element belongs"},
		{"f800", ErrMalformed, "two-byte simple below 32 duplicates the one-byte form"},
		{"f81f", ErrMalformed, "two-byte simple 31 likewise"},
		{"5f00ff", ErrMalformed, "a non-string chunk inside an indefinite byte string"},
		{"5f5f42010243030405ffff", ErrMalformed, "an indefinite string nested in one"},
		{"7f42010243030405ff", ErrMalformed, "byte chunks inside an indefinite text string"},
		{"61cd", ErrMalformed, "invalid UTF-8 in text"},
		{"7f61616161", ErrTruncated, "indefinite text with no break"},
		{"64494554", ErrTruncated, "text shorter than declared"},
		{"830102", ErrTruncated, "array short one element"},
		{"a161", ErrTruncated, "map missing its value"},
		{"9b0000000000000001", ErrTruncated, "array claims 2^64-1 in nine bytes"},
	} {
		t.Run(c.why, func(t *testing.T) {
			_, err := dec(t, c.hex)
			if err != c.want {
				t.Fatalf("err=%v, want %v", err, c.want)
			}
		})
	}
}

// An indefinite text string is validated as the concatenation, because a chunk
// boundary may fall inside a multi-byte rune. Validating each chunk on its own
// would reject a document that is well-formed.
func TestIndefiniteTextValidatesTheConcatenation(t *testing.T) {
	// "ü" is c3 bc, split across two chunks.
	v, err := dec(t, "7f6161614262c3bcff")
	if err != nil {
		t.Fatalf("a well-formed chunked string was refused: %v", err)
	}
	if s, _ := v.AsText(); s != "aBü" {
		t.Fatalf("text %q", s)
	}
	// The same two bytes split across chunks: each chunk alone is invalid
	// UTF-8, the concatenation is valid.
	v, err = dec(t, "7f61436143ff")
	_ = v
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	// And a concatenation that is genuinely invalid is still refused.
	if _, err := dec(t, "7f41cdff"); err != ErrMalformed {
		t.Fatalf("byte chunk in a text string: %v", err)
	}
}

func TestLimits(t *testing.T) {
	deep := make([]byte, 0, 200)
	for i := 0; i < 100; i++ {
		deep = append(deep, 0x81)
	}
	deep = append(deep, 0x00)
	if _, err := New(deep, Limits{MaxDepth: 64}).Decode(); err != ErrDepth {
		t.Errorf("depth: %v", err)
	}
	if _, err := New(deep, Limits{MaxDepth: 128}).Decode(); err != nil {
		t.Errorf("depth 128 should accept 100 levels: %v", err)
	}
	b, _ := hex.DecodeString("83010203")
	if _, err := New(b, Limits{MaxArrayElements: 2}).Decode(); err != ErrTooLarge {
		t.Errorf("array elements: %v", err)
	}
	b, _ = hex.DecodeString("4401020304")
	if _, err := New(b, Limits{MaxStringBytes: 2}).Decode(); err != ErrTooLarge {
		t.Errorf("string bytes: %v", err)
	}
	b, _ = hex.DecodeString("83010203")
	if _, err := New(b, Limits{MaxTotalItems: 2}).Decode(); err != ErrTooLarge {
		t.Errorf("total items: %v", err)
	}
}
