package diag

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/sebishogun/simdcbor/value"
)

// Parsing diagnostic notation back into the value model.
//
// It exists so the notation is a format rather than a display: a test can
// write the item it means and compare, and a person can edit a document and
// feed it back. The round trip is the contract -- Parse(Render(v)) is v --
// and it is why the renderer keeps the trailing .0 on integral floats and the
// underscore on indefinite containers. Anything the renderer drops, the parser
// cannot recover.

// ErrSyntax means the text is not diagnostic notation.
var ErrSyntax = errors.New("simdcbor/diag: syntax error")

// Parse reads one item of diagnostic notation.
func Parse(s string) (value.Value, error) {
	p := &parser{s: s}
	p.ws()
	v, err := p.value(64)
	if err != nil {
		return value.Value{}, err
	}
	p.ws()
	if p.i != len(p.s) {
		return value.Value{}, fmt.Errorf("%w: trailing text at %d", ErrSyntax, p.i)
	}
	return v, nil
}

type parser struct {
	s string
	i int
}

func (p *parser) ws() {
	for p.i < len(p.s) && (p.s[p.i] == ' ' || p.s[p.i] == '\t' || p.s[p.i] == '\n' || p.s[p.i] == '\r') {
		p.i++
	}
}

func (p *parser) peek() byte {
	if p.i < len(p.s) {
		return p.s[p.i]
	}
	return 0
}

func (p *parser) accept(prefix string) bool {
	if strings.HasPrefix(p.s[p.i:], prefix) {
		p.i += len(prefix)
		return true
	}
	return false
}

func (p *parser) value(depth int) (value.Value, error) {
	if depth < 0 {
		return value.Value{}, fmt.Errorf("%w: too deep", ErrSyntax)
	}
	p.ws()
	switch {
	case p.accept("false"):
		return value.False, nil
	case p.accept("true"):
		return value.True, nil
	case p.accept("null"):
		return value.Null, nil
	case p.accept("undefined"):
		return value.Undefined, nil
	case p.accept("NaN"):
		return value.FromFloat64(math.NaN()), nil
	case p.accept("Infinity"):
		return value.FromFloat64(math.Inf(1)), nil
	case p.accept("-Infinity"):
		return value.FromFloat64(math.Inf(-1)), nil
	case p.accept("simple("):
		n, err := p.uintUntil(')')
		if err != nil {
			return value.Value{}, err
		}
		v, ok := value.FromSimple(uint8(n))
		if !ok || n > 255 {
			return value.Value{}, fmt.Errorf("%w: simple(%d) has no encoding", ErrSyntax, n)
		}
		return v, nil
	case p.accept("h'"):
		return p.hexString()
	case p.peek() == '"':
		s, err := p.text()
		if err != nil {
			return value.Value{}, err
		}
		return value.FromText(s), nil
	case p.peek() == '[':
		return p.array(depth)
	case p.peek() == '{':
		return p.mapValue(depth)
	case p.accept("(_"):
		return p.chunked(depth)
	}
	return p.number(depth)
}

func (p *parser) hexString() (value.Value, error) {
	j := strings.IndexByte(p.s[p.i:], '\'')
	if j < 0 {
		return value.Value{}, fmt.Errorf("%w: unterminated h''", ErrSyntax)
	}
	b, err := hex.DecodeString(p.s[p.i : p.i+j])
	if err != nil {
		return value.Value{}, fmt.Errorf("%w: %v", ErrSyntax, err)
	}
	p.i += j + 1
	return value.FromBytes(b), nil
}

func (p *parser) text() (string, error) {
	if p.peek() != '"' {
		return "", ErrSyntax
	}
	p.i++
	var b strings.Builder
	for p.i < len(p.s) {
		c := p.s[p.i]
		if c == '"' {
			p.i++
			return b.String(), nil
		}
		if c == '\\' {
			p.i++
			if p.i >= len(p.s) {
				break
			}
			switch p.s[p.i] {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case '"':
				b.WriteByte('"')
			case '\\':
				b.WriteByte('\\')
			case 'u':
				if p.i+4 >= len(p.s) {
					return "", ErrSyntax
				}
				n, err := strconv.ParseUint(p.s[p.i+1:p.i+5], 16, 32)
				if err != nil {
					return "", fmt.Errorf("%w: %v", ErrSyntax, err)
				}
				b.WriteRune(rune(n))
				p.i += 4
			default:
				return "", ErrSyntax
			}
			p.i++
			continue
		}
		b.WriteByte(c)
		p.i++
	}
	return "", fmt.Errorf("%w: unterminated string", ErrSyntax)
}

func (p *parser) array(depth int) (value.Value, error) {
	p.i++ // '['
	indef := false
	p.ws()
	if p.peek() == '_' {
		indef = true
		p.i++
	}
	var out []value.Value
	for {
		p.ws()
		if p.peek() == ']' {
			p.i++
			v := value.FromArray(out...)
			if indef {
				v = v.AsIndefinite()
			}
			return v, nil
		}
		if len(out) > 0 && !p.accept(",") {
			return value.Value{}, fmt.Errorf("%w: expected , or ] at %d", ErrSyntax, p.i)
		}
		e, err := p.value(depth - 1)
		if err != nil {
			return value.Value{}, err
		}
		out = append(out, e)
	}
}

func (p *parser) mapValue(depth int) (value.Value, error) {
	p.i++ // '{'
	indef := false
	p.ws()
	if p.peek() == '_' {
		indef = true
		p.i++
	}
	var out []value.KeyValue
	for {
		p.ws()
		if p.peek() == '}' {
			p.i++
			v := value.FromMap(out...)
			if indef {
				v = v.AsIndefinite()
			}
			return v, nil
		}
		if len(out) > 0 && !p.accept(",") {
			return value.Value{}, fmt.Errorf("%w: expected , or } at %d", ErrSyntax, p.i)
		}
		k, err := p.value(depth - 1)
		if err != nil {
			return value.Value{}, err
		}
		p.ws()
		if !p.accept(":") {
			return value.Value{}, fmt.Errorf("%w: expected : at %d", ErrSyntax, p.i)
		}
		val, err := p.value(depth - 1)
		if err != nil {
			return value.Value{}, err
		}
		out = append(out, value.KeyValue{Key: k, Value: val})
	}
}

// chunked reads the (_ ...) form, which the renderer emits for an indefinite
// string. Only one chunk comes back, because that is all the value holds.
func (p *parser) chunked(depth int) (value.Value, error) {
	p.ws()
	if p.accept("h'") {
		v, err := p.hexString()
		if err != nil {
			return value.Value{}, err
		}
		p.ws()
		if !p.accept(")") {
			return value.Value{}, fmt.Errorf("%w: expected ) at %d", ErrSyntax, p.i)
		}
		return v.AsIndefinite(), nil
	}
	if p.peek() == '"' {
		s, err := p.text()
		if err != nil {
			return value.Value{}, err
		}
		p.ws()
		if !p.accept(")") {
			return value.Value{}, fmt.Errorf("%w: expected ) at %d", ErrSyntax, p.i)
		}
		return value.FromText(s).AsIndefinite(), nil
	}
	return value.Value{}, fmt.Errorf("%w: (_ must hold a string", ErrSyntax)
}

func (p *parser) uintUntil(end byte) (uint64, error) {
	j := strings.IndexByte(p.s[p.i:], end)
	if j < 0 {
		return 0, ErrSyntax
	}
	n, err := strconv.ParseUint(strings.TrimSpace(p.s[p.i:p.i+j]), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrSyntax, err)
	}
	p.i += j + 1
	return n, nil
}

// number reads an integer, a float, or a tag -- which is an integer followed
// by a parenthesis, and is therefore only distinguishable after the number.
func (p *parser) number(depth int) (value.Value, error) {
	start := p.i
	if p.peek() == '-' {
		p.i++
	}
	for p.i < len(p.s) && (p.s[p.i] >= '0' && p.s[p.i] <= '9') {
		p.i++
	}
	if p.i == start || (p.i == start+1 && p.s[start] == '-') {
		return value.Value{}, fmt.Errorf("%w: expected a value at %d", ErrSyntax, start)
	}
	intPart := p.s[start:p.i]

	// A tag: the number is followed by the tagged item in parentheses.
	if p.peek() == '(' {
		n, err := strconv.ParseUint(intPart, 10, 64)
		if err != nil {
			return value.Value{}, fmt.Errorf("%w: tag %s", ErrSyntax, intPart)
		}
		p.i++
		inner, err := p.value(depth - 1)
		if err != nil {
			return value.Value{}, err
		}
		p.ws()
		if !p.accept(")") {
			return value.Value{}, fmt.Errorf("%w: expected ) at %d", ErrSyntax, p.i)
		}
		return value.FromTag(n, inner), nil
	}

	// A float: a point or an exponent makes it one, and the renderer always
	// writes one of those for a float, so the distinction survives the trip.
	isFloat := false
	if p.peek() == '.' {
		isFloat = true
		p.i++
		for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
			p.i++
		}
	}
	if c := p.peek(); c == 'e' || c == 'E' {
		isFloat = true
		p.i++
		if c := p.peek(); c == '+' || c == '-' {
			p.i++
		}
		for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
			p.i++
		}
	}
	text := p.s[start:p.i]
	if isFloat {
		f, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return value.Value{}, fmt.Errorf("%w: %v", ErrSyntax, err)
		}
		return value.FromFloat64(f), nil
	}
	if text[0] == '-' {
		// -1-n, so the magnitude is the printed value minus one -- and the
		// -2^64 endpoint has to be spelled out, since no int64 parses it.
		if text == "-18446744073709551616" {
			return value.FromNegMagnitude(math.MaxUint64), nil
		}
		mag, err := strconv.ParseUint(text[1:], 10, 64)
		if err != nil || mag == 0 {
			return value.Value{}, fmt.Errorf("%w: %s", ErrSyntax, text)
		}
		return value.FromNegMagnitude(mag - 1), nil
	}
	n, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return value.Value{}, fmt.Errorf("%w: %s", ErrSyntax, text)
	}
	return value.FromUint(n), nil
}
