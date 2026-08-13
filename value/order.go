package value

import "bytes"

// Map key ordering, for the deterministic encodings RFC 8949 defines.
//
// Both comparators sort the *encoded* keys, head byte included, which is the
// part that surprises: "z" sorts before "aa" because 0x61 < 0x62, where
// sort.Strings puts "aa" first. A comparator that sorted the decoded strings
// would produce a different byte stream and fail every interop check against
// an encoder that follows the RFC.

// Order selects a deterministic key ordering.
type Order uint8

const (
	// CoreDeterministic is RFC 8949 section 4.2.1: bytewise lexicographic over
	// the full encoded key.
	CoreDeterministic Order = iota
	// LengthFirst is RFC 8949 section 4.2.3, the legacy rule from RFC 7049
	// section 3.9: shorter encoded keys first, then bytewise.
	LengthFirst
)

// CompareKeys orders two encoded keys under the given rule. It returns a
// negative number, zero, or a positive number, like bytes.Compare.
func CompareKeys(a, b []byte, order Order) int {
	if order == LengthFirst && len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	return bytes.Compare(a, b)
}

// SortMap orders a map's entries in place under the given rule and key mode.
// It is stable, so entries with equal keys keep their relative order and a
// duplicate-key check can still see which came first.
func SortMap(v *Value, order Order, mode KeyMode) error {
	if v.kind != Map {
		return nil
	}
	encoded := make([][]byte, len(v.m))
	for i, kv := range v.m {
		k, err := CanonicalKey(kv.Key, mode)
		if err != nil {
			return err
		}
		encoded[i] = k
	}
	// Insertion sort keeps it stable and allocation-free beyond the keys; a
	// map large enough for that to matter is not the shape this runs on.
	for i := 1; i < len(v.m); i++ {
		for j := i; j > 0 && CompareKeys(encoded[j], encoded[j-1], order) < 0; j-- {
			v.m[j], v.m[j-1] = v.m[j-1], v.m[j]
			encoded[j], encoded[j-1] = encoded[j-1], encoded[j]
		}
	}
	return nil
}

// DuplicateKey reports the first duplicated key in a map, if any. CBOR does
// not forbid duplicates on the wire, but a decoder producing a Go map silently
// loses one, so the caller gets to decide rather than find out later.
func DuplicateKey(v Value, mode KeyMode) (Value, bool, error) {
	if v.kind != Map {
		return Value{}, false, nil
	}
	seen := make(map[string]struct{}, len(v.m))
	for _, kv := range v.m {
		k, err := KeyString(kv.Key, mode)
		if err != nil {
			return Value{}, false, err
		}
		if _, dup := seen[k]; dup {
			return kv.Key, true, nil
		}
		seen[k] = struct{}{}
	}
	return Value{}, false, nil
}
