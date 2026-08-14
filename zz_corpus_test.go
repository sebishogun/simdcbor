package simdcbor

import (
	"fmt"
	"sort"
	"strconv"
)

func adapterCorpus() []any {
	return []any{
		nil, true, false,
		0, 1, 23, 24, 255, 256, 65535, 65536, 1 << 32,
		-1, -24, -256, -65536,
		int64(1) << 62, int64(-1) << 62,
		0.0, 1.0, 1.5, 100000.0, 0.1, -0.0,
		"", "a", "IETF", "水", "𐅑",
		[]byte{}, []byte{1, 2, 3, 4},
		[]any{}, []any{1, 2, 3}, []any{1, []any{2, 3}},
		map[string]any{}, map[string]any{"a": 1},
		map[string]any{"z": 1, "aa": 2, "b": 3},
		map[string]any{"nested": map[string]any{"x": []any{1, 2}}},
		[]any{nil, true, "x", 1.5, []byte{9}},
	}
}

func adapterDecodeCorpus() []string {
	return []string{
		"00", "17", "1818", "1903e8", "1bffffffffffffffff",
		"20", "3863", "3bffffffffffffffff",
		"40", "4401020304", "60", "6161", "6449455446", "64f0908591",
		"80", "83010203", "8301820203820405",
		"a0", "a26161016162820203", "a201020304",
		"f4", "f5", "f6", "f7", "e0", "f820",
		"f90000", "f93c00", "fa47c35000", "fb400921fb54442d18",
		"c11a514b67b0", "d9d9f701",
		"9fff", "9f00ff", "bfff", "bf6161016162820203ff",
		"5f42010243030405ff", "7f61616162ff",
		"1c", "1f", "ff", "81ff", "f800", "61cd", "a16101616102",
	}
}

func goSyntax(v any) string {
	switch t := v.(type) {
	case nil:
		return "nil"
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		s := "map["
		for i, k := range keys {
			if i > 0 {
				s += " "
			}
			s += k + ":" + goSyntax(t[k])
		}
		return s + "]"
	case []any:
		s := "["
		for i, e := range t {
			if i > 0 {
				s += " "
			}
			s += goSyntax(e)
		}
		return s + "]"
	case []byte:
		return fmt.Sprintf("%v", t)
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	case string:
		return strconv.Quote(t)
	}
	return fmt.Sprintf("%v(%v)", fmt.Sprintf("%T", v), v)
}

func itoa(n int) string { return strconv.Itoa(n) }
