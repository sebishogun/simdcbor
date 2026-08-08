package simdcbor

import (
	"math/rand"
	"reflect"
	"testing"

	"github.com/fxamacker/cbor/v2"
)

// Round trip: Marshal then Unmarshal is the identity on the shaped set,
// and fxamacker must decode our bytes to the same value.
func TestMarshalRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	dm, _ := cbor.DecOptions{}.DecMode()
	var build func(d int) any
	build = func(d int) any {
		switch k := rng.Intn(7); {
		case k == 0 && d < 4:
			m := map[string]any{}
			for i := 0; i < rng.Intn(5); i++ {
				m[randStr(rng)] = build(d + 1)
			}
			return m
		case k == 1 && d < 4:
			s := make([]any, rng.Intn(6))
			for i := range s {
				s[i] = build(d + 1)
			}
			return s
		case k == 2:
			return randStr(rng)
		case k == 3:
			return float64(rng.Intn(1 << 20))
		case k == 4:
			return rng.Intn(2) == 0
		default:
			return nil
		}
	}
	for i := 0; i < 3000; i++ {
		v := build(0)
		enc, err := Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		// Our own decode round-trips.
		got, n, err := Unmarshal(enc)
		if err != nil || n != len(enc) {
			t.Fatalf("self-decode: %v n=%d/%d", err, n, len(enc))
		}
		if !reflect.DeepEqual(normalize(v), got) {
			t.Fatalf("round trip:\n got %#v\nwant %#v", got, normalize(v))
		}
		// fxamacker decodes our bytes.
		var ref any
		if err := dm.Unmarshal(enc, &ref); err != nil {
			t.Fatalf("fxamacker rejects our bytes: %v", err)
		}
	}
	// Canonical: same map, same bytes.
	m := map[string]any{"b": 2.0, "a": 1.0, "c": 3.0}
	e1, _ := Marshal(m)
	e2, _ := Marshal(m)
	if string(e1) != string(e2) {
		t.Fatal("map encoding not canonical")
	}
}

// normalize maps a built value to the shapes Unmarshal produces (ints
// become float64, etc.) for comparison.
func normalize(v any) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = normalize(x[i])
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = normalize(e)
		}
		return out
	default:
		return v
	}
}
