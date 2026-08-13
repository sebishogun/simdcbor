package value

import (
	"sort"
	"testing"
)

// Both RFC orderings sort the encoded key, head byte included. The head is
// what makes them differ from sorting the decoded strings, and that difference
// is the whole reason a comparator exists here rather than a call to
// sort.Strings.
func TestHeadParticipatesInTheOrder(t *testing.T) {
	z, aa := FromText("z"), FromText("aa")
	kz, kaa := mustKey(t, z, DirectKeys), mustKey(t, aa, DirectKeys)
	// "z" is 61 7a, "aa" is 62 61 61: the head decides before any content.
	for _, order := range []Order{CoreDeterministic, LengthFirst} {
		if CompareKeys(kz, kaa, order) >= 0 {
			t.Errorf("order %v put %q after %q", order, "z", "aa")
		}
	}
	// Sorting the decoded strings gives the opposite answer, which is the
	// mistake this pins against.
	s := []string{"z", "aa"}
	sort.Strings(s)
	if s[0] != "aa" {
		t.Fatal("sort.Strings no longer disagrees; the contrast this test rests on is gone")
	}
}

// A pair that separates the two rules. It has to cross major types: within one
// major type a shorter encoding almost always carries a smaller head, so the
// two rules agree and the pair proves nothing.
//
// The plan proposed h'ff' against h'0000' on the grounds that bytewise would
// take 0x00 before 0xff. It does not: with heads included those keys are 41 ff
// and 42 00 00, so bytewise decides on 0x41 < 0x42 and puts h'ff' first, which
// is the same answer length-first gives. Comparing contents rather than
// encodings is the mistake this comparator exists to prevent, and the example
// had made it.
func TestTheTwoOrdersDisagree(t *testing.T) {
	// uint 500 encodes as 19 01 f4 (three bytes); h'ff' as 41 ff (two).
	long := mustKey(t, FromUint(500), DirectKeys)
	short := mustKey(t, FromBytes([]byte{0xff}), DirectKeys)
	if CompareKeys(long, short, CoreDeterministic) >= 0 {
		t.Error("bytewise should put uint 500 first: head 0x19 against 0x41")
	}
	if CompareKeys(long, short, LengthFirst) <= 0 {
		t.Error("length-first should put h'ff' first: two bytes against three")
	}
	// The plan's pair, pinned to what the rules actually do: both put h'ff'
	// first, so it does not separate them.
	ff := mustKey(t, FromBytes([]byte{0xff}), DirectKeys)
	zeros := mustKey(t, FromBytes([]byte{0x00, 0x00}), DirectKeys)
	for _, order := range []Order{CoreDeterministic, LengthFirst} {
		if CompareKeys(ff, zeros, order) >= 0 {
			t.Errorf("order %v put h'0000' before h'ff'", order)
		}
	}

	// Same encoded length: both rules agree, bytewise.
	a := mustKey(t, FromBytes([]byte{0xff}), DirectKeys)
	b := mustKey(t, FromBytes([]byte{0x00}), DirectKeys)
	for _, order := range []Order{CoreDeterministic, LengthFirst} {
		if CompareKeys(a, b, order) <= 0 {
			t.Errorf("order %v: h'ff' should follow h'00' at equal length", order)
		}
	}
}

func TestSortMap(t *testing.T) {
	m := FromMap(
		KeyValue{FromText("aa"), FromUint(2)},
		KeyValue{FromText("z"), FromUint(1)},
		KeyValue{FromUint(10), FromUint(3)},
		KeyValue{FromUint(1), FromUint(4)},
	)
	if err := SortMap(&m, CoreDeterministic, DirectKeys); err != nil {
		t.Fatal(err)
	}
	entries, _ := m.AsMap()
	// Integers encode with major 0 (head 0x01, 0x0a) and text with major 3
	// (0x61, 0x62), so every integer key sorts before every text key.
	wantKinds := []Kind{Uint, Uint, Text, Text}
	for i, kv := range entries {
		if kv.Key.Kind() != wantKinds[i] {
			t.Fatalf("entry %d is %v, want %v", i, kv.Key.Kind(), wantKinds[i])
		}
	}
	if n, _ := entries[0].Key.AsUint(); n != 1 {
		t.Errorf("first key is %d, want 1", n)
	}
	if s, _ := entries[2].Key.AsText(); s != "z" {
		t.Errorf("third key is %q, want \"z\" -- the head sorts it before \"aa\"", s)
	}
}

// Sorting is stable, so a duplicate key keeps the order it arrived in and the
// duplicate check can still say which entry came first.
func TestSortMapIsStable(t *testing.T) {
	m := FromMap(
		KeyValue{FromFloat16Bits(0x3c00), FromUint(1)}, // 1.0
		KeyValue{FromFloat64(1), FromUint(2)},          // the same key
		KeyValue{FromText("a"), FromUint(3)},
	)
	if err := SortMap(&m, CoreDeterministic, DirectKeys); err != nil {
		t.Fatal(err)
	}
	entries, _ := m.AsMap()
	var vals []uint64
	for _, kv := range entries {
		if kv.Key.Kind() == Float16 || kv.Key.Kind() == Float64 {
			n, _ := kv.Value.AsUint()
			vals = append(vals, n)
		}
	}
	if len(vals) != 2 || vals[0] != 1 || vals[1] != 2 {
		t.Fatalf("equal keys reordered: %v", vals)
	}
}
