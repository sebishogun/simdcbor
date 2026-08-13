package codec

import (
	"bytes"
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/sebishogun/simdcbor/value"
)

func decodeWithTags(t *testing.T, h string, m TagMode) (value.Value, error) {
	t.Helper()
	b, _ := hex.DecodeString(h)
	d := New(b, Limits{})
	d.SetTagMode(m)
	return d.Decode()
}

// Keep is the default and loses nothing: the tag survives and the bytes
// reproduce exactly.
func TestTagsKeepRoundTrips(t *testing.T) {
	for _, h := range []string{
		"c074323031332d30332d32315432303a30343a30305a", // 0: date-time text
		"c11a514b67b0",               // 1: epoch
		"c249010000000000000000",     // 2: bignum
		"c349010000000000000000",     // 3: negative bignum
		"c48221196ab3",               // 4: decimal fraction
		"c5822003",                   // 5: bigfloat
		"d8206a687474703a2f2f612e62", // 32: URI
		"d9d9f701",                   // 55799: self-describe
		"d87b01",                     // an unknown tag stays generic
		"c1c101",                     // nested tags nest
	} {
		t.Run(h, func(t *testing.T) {
			v, err := decodeWithTags(t, h, TagKeep)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if v.Kind() != value.TagKind {
				t.Fatalf("kind %v, want a tag", v.Kind())
			}
			e := NewEncoder(nil)
			if err := e.WriteValue(v); err != nil {
				t.Fatal(err)
			}
			out, err := e.Bytes()
			if err != nil {
				t.Fatal(err)
			}
			in, _ := hex.DecodeString(h)
			if !bytes.Equal(out, in) {
				t.Fatalf("re-encoded %x, want %x", out, in)
			}
		})
	}
}

// Discard is the adapter's behavior, and the test says what it costs: a tagged
// value and a bare one become indistinguishable.
func TestTagsDiscardIsLossy(t *testing.T) {
	tagged, err := decodeWithTags(t, "c11a514b67b0", TagDiscard)
	if err != nil {
		t.Fatal(err)
	}
	if tagged.Kind() != value.Uint {
		t.Fatalf("kind %v, want the tag dropped to its content", tagged.Kind())
	}
	bare, err := decodeWithTags(t, "1a514b67b0", TagDiscard)
	if err != nil {
		t.Fatal(err)
	}
	kt, _ := value.CanonicalKey(tagged, value.DirectKeys)
	kb, _ := value.CanonicalKey(bare, value.DirectKeys)
	if !bytes.Equal(kt, kb) {
		t.Fatal("the point of this test is that they are indistinguishable; they are not")
	}
	// And Keep tells them apart, which is why it is the default.
	tagged2, _ := decodeWithTags(t, "c11a514b67b0", TagKeep)
	k2, _ := value.CanonicalKey(tagged2, value.DirectKeys)
	if bytes.Equal(k2, kb) {
		t.Fatal("Keep did not distinguish a tagged value from a bare one")
	}
}

func TestTagsInterpret(t *testing.T) {
	t.Run("date-time text", func(t *testing.T) {
		v, err := decodeWithTags(t, "c074323031332d30332d32315432303a30343a30305a", TagInterpret)
		if err != nil {
			t.Fatal(err)
		}
		got, ok, err := Interpret(v)
		if err != nil || !ok {
			t.Fatalf("interpret: %v %v", ok, err)
		}
		if got.Text != "2013-03-21T20:04:00Z" {
			t.Fatalf("text %q", got.Text)
		}
	})
	t.Run("epoch", func(t *testing.T) {
		v, _ := decodeWithTags(t, "c11a514b67b0", TagInterpret)
		got, ok, err := Interpret(v)
		if err != nil || !ok {
			t.Fatalf("interpret: %v %v", ok, err)
		}
		if got.Epoch != 1363896240 || !got.EpochExact {
			t.Fatalf("epoch %v exact=%v", got.Epoch, got.EpochExact)
		}
	})
	t.Run("positive bignum", func(t *testing.T) {
		v, _ := decodeWithTags(t, "c249010000000000000000", TagInterpret)
		got, ok, err := Interpret(v)
		if err != nil || !ok {
			t.Fatalf("interpret: %v %v", ok, err)
		}
		want := new(big.Int).Lsh(big.NewInt(1), 64) // 18446744073709551616
		if got.Bignum.Cmp(want) != 0 {
			t.Fatalf("bignum %v, want %v", got.Bignum, want)
		}
	})
	t.Run("negative bignum is -1-n", func(t *testing.T) {
		v, _ := decodeWithTags(t, "c349010000000000000000", TagInterpret)
		got, ok, err := Interpret(v)
		if err != nil || !ok {
			t.Fatalf("interpret: %v %v", ok, err)
		}
		// RFC 8949: content n, value -1-n. The same off-by-one as major 1.
		want := new(big.Int).Lsh(big.NewInt(1), 64)
		want.Neg(want)
		want.Sub(want, big.NewInt(1))
		if got.Bignum.Cmp(want) != 0 {
			t.Fatalf("bignum %v, want %v", got.Bignum, want)
		}
	})
	t.Run("URI", func(t *testing.T) {
		v, _ := decodeWithTags(t, "d8206a687474703a2f2f612e62", TagInterpret)
		got, ok, _ := Interpret(v)
		if !ok || got.Text != "http://a.b" {
			t.Fatalf("uri %q ok=%v", got.Text, ok)
		}
	})
	t.Run("an unknown tag stays generic", func(t *testing.T) {
		v, err := decodeWithTags(t, "d87b01", TagInterpret)
		if err != nil {
			t.Fatalf("an unknown tag should decode, not error: %v", err)
		}
		if v.Kind() != value.TagKind {
			t.Fatalf("kind %v", v.Kind())
		}
		if _, ok, err := Interpret(v); ok || err != nil {
			t.Fatalf("an unknown tag reported as interpreted: ok=%v err=%v", ok, err)
		}
	})
	t.Run("content that contradicts the tag", func(t *testing.T) {
		// Tag 2 must wrap a byte string; here it wraps an integer.
		if _, err := decodeWithTags(t, "c201", TagInterpret); err != ErrBadTagContent {
			t.Fatalf("err=%v, want ErrBadTagContent", err)
		}
		// Under Keep, the content is not the decoder's business.
		if _, err := decodeWithTags(t, "c201", TagKeep); err != nil {
			t.Fatalf("Keep rejected a tag it should carry: %v", err)
		}
	})
}

// A tag is part of a key: the number and the tagged value both count.
func TestTagsAsKeys(t *testing.T) {
	same, err := value.KeysEqual(
		value.FromTag(1, value.FromText("a")),
		value.FromTag(1, value.FromText("a")), value.DirectKeys)
	if err != nil || !same {
		t.Fatalf("equal tag keys: %v %v", same, err)
	}
	diff, _ := value.KeysEqual(
		value.FromTag(1, value.FromText("a")),
		value.FromTag(2, value.FromText("a")), value.DirectKeys)
	if diff {
		t.Fatal("tag number ignored in key identity")
	}
}
