package diag

import (
	"encoding/hex"
	"math"
	"strconv"
	"strings"

	"github.com/sebishogun/simdcbor/internal/codec"
	"github.com/sebishogun/simdcbor/value"
)

// RFC 8949 diagnostic notation: what a CBOR item looks like when a human has
// to read it.
//
// The notation carries distinctions JSON cannot, and reproducing them is the
// whole job. An integer and a float that happen to be equal are different
// items, so 1.0 renders with its point; a byte string is not a text string, so
// it renders as h'..'; an indefinite container is not a definite one, so it
// renders with the underscore that says so. A renderer that dropped any of
// those would produce output that reads fine and cannot be parsed back.

// Render returns the diagnostic notation for v.
func Render(v value.Value) string {
	var b strings.Builder
	render(&b, v)
	return b.String()
}

// RenderBytes renders every item in b, separated by ", ".
//
// A truncated document returns the notation for the items that were complete,
// alongside the error. That is the useful answer: it says how far the document
// got, which is what a person looking at a broken document needs, where
// returning nothing says only that something was wrong.
func RenderBytes(b []byte) (string, error) {
	d := codec.New(b, codec.Limits{})
	var parts []string
	for d.More() {
		v, err := d.Decode()
		if err != nil {
			return strings.Join(parts, ", "), err
		}
		parts = append(parts, Render(v))
	}
	if len(parts) == 0 {
		return "", codec.ErrTruncated
	}
	return strings.Join(parts, ", "), nil
}

func render(b *strings.Builder, v value.Value) {
	switch v.Kind() {
	case value.Uint:
		n, _ := v.AsUint()
		b.WriteString(strconv.FormatUint(n, 10))
	case value.NegInt:
		n, _ := v.AsNegMagnitude()
		b.WriteString(formatNegative(n))
	case value.Bytes:
		s, _ := v.AsBytes()
		// An indefinite string renders as a chunked group, which is what says
		// it arrived that way: (_ h'0102').
		if v.Indefinite() {
			b.WriteString("(_ h'")
			b.WriteString(hex.EncodeToString(s))
			b.WriteString("')")
			return
		}
		b.WriteString("h'")
		b.WriteString(hex.EncodeToString(s))
		b.WriteByte('\'')
	case value.Text:
		s, _ := v.AsText()
		if v.Indefinite() {
			b.WriteString("(_ ")
			b.WriteString(quote(s))
			b.WriteByte(')')
			return
		}
		b.WriteString(quote(s))
	case value.Array:
		a, _ := v.AsArray()
		b.WriteByte('[')
		if v.Indefinite() {
			b.WriteByte('_')
			if len(a) > 0 {
				b.WriteByte(' ')
			}
		}
		for i, e := range a {
			if i > 0 {
				b.WriteString(", ")
			}
			render(b, e)
		}
		b.WriteByte(']')
	case value.Map:
		m, _ := v.AsMap()
		b.WriteByte('{')
		if v.Indefinite() {
			b.WriteByte('_')
			if len(m) > 0 {
				b.WriteByte(' ')
			}
		}
		for i, kv := range m {
			if i > 0 {
				b.WriteString(", ")
			}
			render(b, kv.Key)
			b.WriteString(": ")
			render(b, kv.Value)
		}
		b.WriteByte('}')
	case value.TagKind:
		n, inner, _ := v.AsTag()
		b.WriteString(strconv.FormatUint(n, 10))
		b.WriteByte('(')
		render(b, inner)
		b.WriteByte(')')
	case value.SimpleKind:
		n, _ := v.AsSimple()
		switch n {
		case 20:
			b.WriteString("false")
		case 21:
			b.WriteString("true")
		case 22:
			b.WriteString("null")
		case 23:
			b.WriteString("undefined")
		default:
			b.WriteString("simple(")
			b.WriteString(strconv.FormatUint(uint64(n), 10))
			b.WriteByte(')')
		}
	case value.Float16, value.Float32, value.Float64:
		f, _, _ := v.AsFloat64()
		b.WriteString(formatFloat(f))
	default:
		b.WriteString("<invalid>")
	}
}

// formatNegative prints -1-n exactly, including the -2^64 endpoint that no
// int64 holds and strconv therefore cannot be asked for.
func formatNegative(n uint64) string {
	if n == math.MaxUint64 {
		return "-18446744073709551616"
	}
	return "-" + strconv.FormatUint(n+1, 10)
}

// formatFloat renders the shortest decimal that reads back to the same value,
// keeping a trailing .0 on integral values.
//
// The point is the float/int distinction: 1.0 and 1 are different items, and a
// renderer that printed "1" for a float would produce notation that parses
// back to an integer.
func formatFloat(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

// quote writes a text string with the escapes the notation defines.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				b.WriteString(`\u`)
				const hexd = "0123456789abcdef"
				b.WriteByte('0')
				b.WriteByte('0')
				b.WriteByte(hexd[(r>>4)&0xf])
				b.WriteByte(hexd[r&0xf])
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
