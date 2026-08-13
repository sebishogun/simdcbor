package codec

import (
	"errors"
	"math/big"

	"github.com/sebishogun/simdcbor/value"
)

// Tag handling, as a mode rather than a fixed behavior.
//
// A tag says what a value means, and there are three defensible things a
// decoder can do with that: drop it, keep it, or act on it. Each is right for
// a different caller and wrong for the others, so the choice is explicit.
//
// Dropping is what the shipped Unmarshal does, and it is lossy in a way that
// is easy to miss: tag 1 wrapping 1700000000 and a bare 1700000000 decode to
// the same Go value, so a re-encode turns a timestamp into an integer and
// nothing reports it.

// TagMode selects what happens to a tag.
type TagMode uint8

const (
	// TagKeep stores the tag generically: Tag{Number, Value}. The default,
	// because it is the only one that loses nothing.
	TagKeep TagMode = iota
	// TagDiscard drops the tag number and keeps the tagged value. The adapter
	// does this, and it is lossy.
	TagDiscard
	// TagInterpret converts the well-known tags to native forms and keeps the
	// rest generically.
	TagInterpret
)

// ErrBadTagContent means a well-known tag wrapped something its definition
// does not allow -- a bignum tag around a text string, say. It is reported
// only under TagInterpret: under TagKeep the content is not the decoder's
// business.
var ErrBadTagContent = errors.New("simdcbor: tag content does not match the tag")

// The well-known tags this package interprets, from RFC 8949 section 3.4 and
// the data-model LLD. Only these; an unknown tag stays generic, because
// guessing at a tag's meaning is how a decoder invents data.
const (
	TagDateTimeString = 0
	TagEpochTime      = 1
	TagPositiveBignum = 2
	TagNegativeBignum = 3
	TagDecimalFrac    = 4
	TagBigFloat       = 5
	TagURI            = 32
	TagBase64URL      = 33
	TagBase64         = 34
	TagMIME           = 36
	TagSelfDescribe   = 55799
)

// Interpreted is a tagged value the decoder recognised and converted. It is
// kept alongside the generic form rather than replacing it, so a caller can
// re-encode the original bytes exactly.
type Interpreted struct {
	// Number is the tag.
	Number uint64
	// Bignum is set for tags 2 and 3.
	Bignum *big.Int
	// Text is set for the string-shaped tags: 0, 32, 33, 34, 36.
	Text string
	// Epoch is set for tag 1, with Exact false when the wire carried a float
	// that cannot be represented without loss.
	Epoch      float64
	EpochExact bool
	// Value is the generic form, always present.
	Value value.Value
}

// SetTagMode selects the decoder's tag handling.
func (d *Decoder) SetTagMode(m TagMode) { d.tags = m }

// Interpret converts a generic tag value to its native form when the tag is
// one of the well-known set. ok is false for any other tag, which is not an
// error: an unknown tag is a value this package carries rather than reads.
func Interpret(v value.Value) (Interpreted, bool, error) {
	num, inner, isTag := v.AsTag()
	if !isTag {
		return Interpreted{}, false, nil
	}
	out := Interpreted{Number: num, Value: v}
	switch num {
	case TagDateTimeString, TagURI, TagBase64URL, TagBase64, TagMIME:
		s, ok := inner.AsText()
		if !ok {
			return Interpreted{}, false, ErrBadTagContent
		}
		out.Text = s
		return out, true, nil

	case TagEpochTime:
		f, exact, ok := inner.AsFloat64()
		if !ok {
			return Interpreted{}, false, ErrBadTagContent
		}
		out.Epoch, out.EpochExact = f, exact
		return out, true, nil

	case TagPositiveBignum, TagNegativeBignum:
		b, ok := inner.AsBytes()
		if !ok || inner.Kind() != value.Bytes {
			return Interpreted{}, false, ErrBadTagContent
		}
		n := new(big.Int).SetBytes(b)
		if num == TagNegativeBignum {
			// RFC 8949: the content of tag 3 is n, and the value is -1-n. The
			// same off-by-one the negative-integer major type uses, and the
			// same reason: it makes every magnitude representable.
			n.Neg(n)
			n.Sub(n, big.NewInt(1))
		}
		out.Bignum = n
		return out, true, nil

	case TagDecimalFrac, TagBigFloat:
		// Both are a two-element array of [exponent, mantissa]. The conversion
		// to a native decimal is not attempted -- Go has no such type, and
		// inventing one here would be a data model, not a decoder.
		a, ok := inner.AsArray()
		if !ok || len(a) != 2 {
			return Interpreted{}, false, ErrBadTagContent
		}
		return out, true, nil

	case TagSelfDescribe:
		// A marker only: it wraps whatever the document is.
		return out, true, nil
	}
	return Interpreted{}, false, nil
}
