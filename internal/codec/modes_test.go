package codec

import (
	"bytes"
	"encoding/hex"
	"math"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/sebishogun/simdcbor/value"
)

func encodeInMode(t *testing.T, m Mode, f func(e *Encoder)) []byte {
	t.Helper()
	e := NewEncoder(nil)
	e.SetMode(m)
	f(e)
	b, err := e.Bytes()
	if err != nil {
		t.Fatalf("mode %d: %v", m, err)
	}
	return b
}

// Both orderings sort the encoded key, head included. The head is what makes
// them differ from sorting decoded strings, and it is also why the pair that
// separates the two rules has to cross major types -- see docs/wrong.md, where
// the plan's proposed pair turned out to agree under both.
func TestModesKeyOrdering(t *testing.T) {
	write := func(e *Encoder) {
		e.StartMap(3)
		e.WriteText("z") // 61 7a
		e.WriteUint(1)
		e.WriteText("aa") // 62 61 61
		e.WriteUint(2)
		e.WriteUint(500) // 19 01 f4
		e.WriteUint(3)
		e.EndMap()
	}
	t.Run("core deterministic is bytewise", func(t *testing.T) {
		got := encodeInMode(t, CoreDeterministic, write)
		// 19 01 f4 (uint) < 61 7a ("z") < 62 61 61 ("aa")
		want, _ := hex.DecodeString("a31901f403617a0162616102")
		if !bytes.Equal(got, want) {
			t.Fatalf("%x, want %x", got, want)
		}
	})
	t.Run("length first is shortest then bytewise", func(t *testing.T) {
		got := encodeInMode(t, LengthFirst, write)
		// "z" (2 bytes) < uint 500 (3) < "aa" (3, and 0x19 < 0x62 decides)
		want, _ := hex.DecodeString("a3617a011901f40362616102")
		if !bytes.Equal(got, want) {
			t.Fatalf("%x, want %x", got, want)
		}
	})
	t.Run("adapter keeps the order given", func(t *testing.T) {
		got := encodeInMode(t, Adapter, write)
		want, _ := hex.DecodeString("a3617a01626161021901f403")
		if !bytes.Equal(got, want) {
			t.Fatalf("%x, want %x", got, want)
		}
	})
}

// The deterministic modes emit the shortest float that reads back
// bit-identical; the adapter never emits float16.
func TestModesFloatRule(t *testing.T) {
	for _, c := range []struct {
		f       float64
		det     string
		adapter string
	}{
		{1, "f93c00", "fa3f800000"},
		{1.5, "f93e00", "fa3fc00000"},
		{100000, "fa47c35000", "fa47c35000"},
		{0.1, "fb3fb999999999999a", "fb3fb999999999999a"},
	} {
		det := encodeInMode(t, CoreDeterministic, func(e *Encoder) { e.WriteFloat64(c.f) })
		if hex.EncodeToString(det) != c.det {
			t.Errorf("%v deterministic: %x, want %s", c.f, det, c.det)
		}
		ad := encodeInMode(t, Adapter, func(e *Encoder) { e.WriteFloat64(c.f) })
		if hex.EncodeToString(ad) != c.adapter {
			t.Errorf("%v adapter: %x, want %s", c.f, ad, c.adapter)
		}
	}
	// Narrowing is exact or not at all, in every mode: a NaN payload that does
	// not survive a half stays wide.
	nan := encodeInMode(t, CoreDeterministic, func(e *Encoder) {
		e.WriteFloat64Bits(0x7ff8000000000001)
	})
	if hex.EncodeToString(nan) != "fb7ff8000000000001" {
		t.Fatalf("a NaN payload was narrowed away: %x", nan)
	}
}

func TestDuplicatePolicies(t *testing.T) {
	// Two spellings of 1.0 as keys, then a text key. Same key, different bytes.
	// {1.0(half): 1, "a": 2, 1.0(double): 3} -- the first and last keys are the
	// same key, spelled two ways.
	doc, _ := hex.DecodeString("a3f93c0001616102fb3ff000000000000003")
	for _, c := range []struct {
		policy DuplicatePolicy
		pairs  int
		first  uint64
		err    error
	}{
		{DuplicateKeep, 3, 1, nil},
		{DuplicateFirstWins, 2, 1, nil},
		{DuplicateLastWins, 2, 3, nil},
		{DuplicateError, 0, 0, ErrDuplicateKey},
	} {
		d := New(doc, Limits{})
		d.SetDuplicatePolicy(c.policy)
		v, err := d.Decode()
		if err != c.err {
			t.Errorf("policy %d: err=%v, want %v", c.policy, err, c.err)
			continue
		}
		if err != nil {
			continue
		}
		m, _ := v.AsMap()
		if len(m) != c.pairs {
			t.Errorf("policy %d: %d pairs, want %d", c.policy, len(m), c.pairs)
			continue
		}
		// The surviving 1.0 entry carries the expected value.
		for _, kv := range m {
			if kv.Key.Kind() == value.Float16 || kv.Key.Kind() == value.Float64 {
				n, _ := kv.Value.AsUint()
				if n != c.first {
					t.Errorf("policy %d: kept the value %d, want %d", c.policy, n, c.first)
				}
				break
			}
		}
	}
}

// A duplicate tag key is caught the same way: sameness is the whole canonical
// encoding, tag number included.
func TestDuplicateTagKeys(t *testing.T) {
	e := NewEncoder(nil)
	e.StartMap(2)
	e.WriteTag(1)
	e.WriteText("a")
	e.WriteUint(1)
	e.WriteTag(1)
	e.WriteText("a")
	e.WriteUint(2)
	e.EndMap()
	b, err := e.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	d := New(b, Limits{})
	d.SetDuplicatePolicy(DuplicateError)
	if _, err := d.Decode(); err != ErrDuplicateKey {
		t.Fatalf("err=%v, want ErrDuplicateKey", err)
	}
	// A different tag number is a different key.
	e = NewEncoder(nil)
	e.StartMap(2)
	e.WriteTag(1)
	e.WriteText("a")
	e.WriteUint(1)
	e.WriteTag(2)
	e.WriteText("a")
	e.WriteUint(2)
	e.EndMap()
	b, _ = e.Bytes()
	d = New(b, Limits{})
	d.SetDuplicatePolicy(DuplicateError)
	if _, err := d.Decode(); err != nil {
		t.Fatalf("Tag{1,a} and Tag{2,a} were treated as one key: %v", err)
	}
}

// Profile interop: fxamacker's SortCoreDeterministic is bytewise and its
// SortCanonical is the legacy length-first rule, so ours must pair with theirs
// by ordering rather than by name.
func TestModesInteropWithFxA(t *testing.T) {
	m := map[any]any{"z": 1, "aa": 2, uint64(500): 3}
	t.Run("bytewise", func(t *testing.T) {
		opts := cbor.CoreDetEncOptions()
		em, err := opts.EncMode()
		if err != nil {
			t.Fatal(err)
		}
		theirs, err := em.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		v, err := New(theirs, Limits{}).Decode()
		if err != nil {
			t.Fatalf("we could not read their bytewise output %x: %v", theirs, err)
		}
		if _, ok := v.AsMap(); !ok {
			t.Fatal("not a map")
		}
		// Ours, same ordering, must decode on their side.
		ours := encodeInMode(t, CoreDeterministic, func(e *Encoder) {
			e.StartMap(3)
			e.WriteText("z")
			e.WriteUint(1)
			e.WriteText("aa")
			e.WriteUint(2)
			e.WriteUint(500)
			e.WriteUint(3)
			e.EndMap()
		})
		var back map[any]any
		if err := cbor.Unmarshal(ours, &back); err != nil {
			t.Fatalf("fxamacker could not read our bytewise output %x: %v", ours, err)
		}
		if len(back) != 3 {
			t.Fatalf("they read %d pairs", len(back))
		}
		if !bytes.Equal(ours, theirs) {
			t.Errorf("byte-for-byte: ours %x, theirs %x", ours, theirs)
		}
	})
	t.Run("length first", func(t *testing.T) {
		em, err := cbor.CanonicalEncOptions().EncMode()
		if err != nil {
			t.Fatal(err)
		}
		theirs, err := em.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		ours := encodeInMode(t, LengthFirst, func(e *Encoder) {
			e.StartMap(3)
			e.WriteText("z")
			e.WriteUint(1)
			e.WriteText("aa")
			e.WriteUint(2)
			e.WriteUint(500)
			e.WriteUint(3)
			e.EndMap()
		})
		if !bytes.Equal(ours, theirs) {
			t.Errorf("byte-for-byte: ours %x, theirs %x", ours, theirs)
		}
	})
}

// Where the deterministic profiles differ from fxamacker's, and why. Their
// deterministic options normalize NaN to f9 7e00 and Inf to float16; this
// package keeps the payload, because the data model treats a NaN payload as
// part of the value. The RFC permits both, so this is a profile difference
// rather than a bug on either side.
func TestModesNaNProfileDiffers(t *testing.T) {
	ours := encodeInMode(t, CoreDeterministic, func(e *Encoder) {
		e.WriteFloat64Bits(0x7ff8000000000001)
	})
	if hex.EncodeToString(ours) != "fb7ff8000000000001" {
		t.Fatalf("we normalized a NaN payload: %x", ours)
	}
	em, err := cbor.CoreDetEncOptions().EncMode()
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := em.Marshal(math.Float64frombits(0x7ff8000000000001))
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(theirs) == hex.EncodeToString(ours) {
		t.Fatal("fxamacker no longer normalizes NaN; this pinned difference is stale")
	}
	// Both still decode on the other side.
	if _, err := New(theirs, Limits{}).Decode(); err != nil {
		t.Fatalf("we cannot read their normalized NaN: %v", err)
	}
	var f float64
	if err := cbor.Unmarshal(ours, &f); err != nil || !math.IsNaN(f) {
		t.Fatalf("they cannot read our NaN payload: %v %v", f, err)
	}
}
