package codec

import (
	"errors"

	"github.com/sebishogun/simdcbor/value"
)

// Deterministic encoding modes, and what a decoder does about duplicate keys.
//
// "Deterministic" means two encoders given the same value produce the same
// bytes, which is what makes a CBOR document hashable and signable. RFC 8949
// defines two orderings and this package implements both, under the names the
// RFC uses rather than under "canonical" -- a word both orderings have been
// called at different times, and which therefore identifies neither.

// Mode selects an encoding profile.
type Mode uint8

const (
	// Adapter is the default: shortest heads, floats narrowed to float32 when
	// exact, map order as given. It does not emit float16.
	Adapter Mode = iota
	// CoreDeterministic is RFC 8949 section 4.2.1: shortest everything
	// including float16, and map keys sorted bytewise over their full
	// encodings.
	CoreDeterministic
	// LengthFirst is RFC 8949 section 4.2.3, the legacy RFC 7049 section 3.9
	// ordering: shorter encoded keys first, then bytewise. Everything else
	// matches CoreDeterministic.
	LengthFirst
)

func (m Mode) deterministic() bool { return m == CoreDeterministic || m == LengthFirst }

func (m Mode) order() value.Order {
	if m == LengthFirst {
		return value.LengthFirst
	}
	return value.CoreDeterministic
}

// DuplicatePolicy says what a decoder does when two keys of one map are the
// same key.
//
// CBOR does not forbid duplicates on the wire, so a decoder has to choose, and
// the choice cannot be left implicit: a map built into Go silently loses one
// of them, and which one it loses depends on iteration order the caller never
// sees.
type DuplicatePolicy uint8

const (
	// DuplicateKeep keeps every entry in wire order. It is the default, and it
	// is the only policy that loses nothing: the value model's maps are
	// ordered lists rather than Go maps, so a duplicate is representable and
	// the caller can ask value.DuplicateKey about it afterwards.
	//
	// It is also what keeps the decoder's accept boundary equal to Skip's. A
	// duplicate key is a fact about the value, not about the framing, and a
	// decoder that rejected it while Skip accepted it would split the two
	// walks this package exists to keep together.
	DuplicateKeep DuplicatePolicy = iota
	// DuplicateError rejects the document. For callers that treat a repeated
	// key as the producer bug or the attack it usually is.
	DuplicateError
	// DuplicateFirstWins keeps the earlier entry.
	DuplicateFirstWins
	// DuplicateLastWins keeps the later entry.
	DuplicateLastWins
)

// ErrDuplicateKey means a map held the same key twice under DuplicateError.
var ErrDuplicateKey = errors.New("simdcbor: duplicate map key")

// SetMode selects the encoder's profile. It must be set before anything is
// written, since the profile decides how floats and maps are emitted.
func (e *Encoder) SetMode(m Mode) { e.mode = m }

// SetDuplicatePolicy selects what the decoder does with duplicate map keys.
func (d *Decoder) SetDuplicatePolicy(p DuplicatePolicy) { d.dup = p }
