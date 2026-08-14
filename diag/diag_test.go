package diag

import (
	"encoding/hex"
	"testing"
)

func TestRenderVectors(t *testing.T) {
	for _, c := range []struct {
		hex  string
		want string
	}{
		{"00", "0"},
		{"17", "23"},
		{"1903e8", "1000"},
		{"1bffffffffffffffff", "18446744073709551615"},
		{"20", "-1"},
		{"3863", "-100"},
		// The endpoint no int64 holds, printed exactly.
		{"3bffffffffffffffff", "-18446744073709551616"},
		{"40", "h''"},
		{"420102", "h'0102'"},
		{"60", `""`},
		{"6161", `"a"`},
		{"6449455446", `"IETF"`},
		{"62225c", `"\"\\"`},
		{"63e6b0b4", `"水"`},
		{"f4", "false"},
		{"f5", "true"},
		{"f6", "null"},
		{"f7", "undefined"},
		// Every simple value outside the named four keeps its number.
		{"e0", "simple(0)"},
		{"f3", "simple(19)"},
		{"f820", "simple(32)"},
		{"f8ff", "simple(255)"},
		// A float keeps its point, so the notation parses back to a float.
		{"f93c00", "1.0"},
		{"f93e00", "1.5"},
		{"f90000", "0.0"},
		{"f98000", "-0.0"},
		{"fa47c35000", "100000.0"},
		{"fb400921fb54442d18", "3.141592653589793"},
		// RFC 8949 section 8 spells these with capitals.
		{"f97c00", "Infinity"},
		{"f9fc00", "-Infinity"},
		{"f97e00", "NaN"},
		{"80", "[]"},
		{"83010203", "[1, 2, 3]"},
		{"8301820203820405", "[1, [2, 3], [4, 5]]"},
		{"a0", "{}"},
		{"a201020304", "{1: 2, 3: 4}"},
		{"a26161016162820203", `{"a": 1, "b": [2, 3]}`},
		// The underscore is what says the length was not declared.
		{"9fff", "[_]"},
		{"9f00ff", "[_ 0]"},
		{"9f018202039f0405ffff", "[_ 1, [2, 3], [_ 4, 5]]"},
		{"bfff", "{_}"},
		{"bf6161016162820203ff", `{_ "a": 1, "b": [2, 3]}`},
		{"5f42010243030405ff", "(_ h'0102030405')"},
		{"7f61616162ff", `(_ "ab")`},
		{"c074323031332d30332d32315432303a30343a30305a", `0("2013-03-21T20:04:00Z")`},
		{"c11a514b67b0", "1(1363896240)"},
		{"d9d9f701", "55799(1)"},
	} {
		t.Run(c.hex, func(t *testing.T) {
			b, err := hex.DecodeString(c.hex)
			if err != nil {
				t.Fatalf("bad hex: %v", err)
			}
			got, err := RenderBytes(b)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if got != c.want {
				t.Fatalf("%s, want %s", got, c.want)
			}
		})
	}
}

// A float renders with its point even when integral, because 1.0 and 1 are
// different items and notation that printed "1" would parse back to the wrong
// one.
func TestFloatsKeepTheirPoint(t *testing.T) {
	for _, c := range []struct{ hex, want string }{
		{"f93c00", "1.0"},
		{"01", "1"},
		{"fa47c35000", "100000.0"},
		{"1a000186a0", "100000"},
	} {
		b, _ := hex.DecodeString(c.hex)
		got, err := RenderBytes(b)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("%s rendered %s, want %s", c.hex, got, c.want)
		}
	}
}

// A truncated item renders the well-formed prefix and reports the error, so
// the output says how far the document got.
func TestRenderTruncatedPrefix(t *testing.T) {
	// Two complete items, then a head with no body.
	b, _ := hex.DecodeString("016361626319")
	got, err := RenderBytes(b)
	if err == nil {
		t.Fatal("a truncated document rendered without an error")
	}
	if got != `1, "abc"` {
		t.Fatalf("prefix %q, want the two complete items", got)
	}
}

func TestRenderNothingAtAll(t *testing.T) {
	if _, err := RenderBytes(nil); err == nil {
		t.Fatal("empty input rendered without an error")
	}
}

// The round trip is the contract: Parse(Render(v)) is v. It is why the
// renderer keeps the trailing .0 and the underscore -- anything it drops, the
// parser cannot recover.
func TestParseRoundTripsRender(t *testing.T) {
	for _, h := range []string{
		"00", "17", "1903e8", "1bffffffffffffffff", "20", "3863",
		"3bffffffffffffffff", "40", "420102", "60", "6161", "6449455446",
		"62225c", "63e6b0b4", "f4", "f5", "f6", "f7", "e0", "f3", "f820",
		"f8ff", "f93c00", "f93e00", "fb400921fb54442d18", "f97c00", "f9fc00",
		"80", "83010203", "8301820203820405", "a0", "a201020304",
		"a26161016162820203", "9fff", "9f00ff", "9f018202039f0405ffff",
		"bfff", "bf6161016162820203ff", "5f42010243030405ff",
		"7f61616162ff", "c074323031332d30332d32315432303a30343a30305a",
		"c11a514b67b0", "d9d9f701", "d87b01",
	} {
		t.Run(h, func(t *testing.T) {
			b, _ := hex.DecodeString(h)
			text, err := RenderBytes(b)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			v, err := Parse(text)
			if err != nil {
				t.Fatalf("parse %q: %v", text, err)
			}
			back := Render(v)
			if back != text {
				t.Fatalf("round trip: %q -> %q", text, back)
			}
		})
	}
}

// NaN survives as NaN. It cannot be compared with ==, which is exactly why it
// needs its own case rather than being folded into the table above.
func TestParseNaN(t *testing.T) {
	v, err := Parse("NaN")
	if err != nil {
		t.Fatal(err)
	}
	if Render(v) != "NaN" {
		t.Fatalf("rendered %q", Render(v))
	}
}

// An integer and a float that print the same number are different items, and
// the parser must keep them apart.
func TestParseKeepsFloatsAndIntsApart(t *testing.T) {
	i, err := Parse("1")
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse("1.0")
	if err != nil {
		t.Fatal(err)
	}
	if i.Kind() == f.Kind() {
		t.Fatalf("both parsed as %v", i.Kind())
	}
	if Render(i) != "1" || Render(f) != "1.0" {
		t.Fatalf("rendered %q and %q", Render(i), Render(f))
	}
}

func TestParseRejects(t *testing.T) {
	for _, s := range []string{
		"", "  ", "[", "]", "{", "{1}", "{1: }", "h'zz'", `"unterminated`,
		"simple(24)", "simple(300)", "1 2", "(_ 1)", "-", "--1", "[1 2]",
	} {
		if _, err := Parse(s); err == nil {
			t.Errorf("%q parsed without error", s)
		}
	}
}
