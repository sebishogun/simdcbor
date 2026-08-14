package simdcbor

import (
	"github.com/sebishogun/simdcbor/internal/codec"
	"github.com/sebishogun/simdcbor/value"
)

// The shipped API as an adapter over the full codec.
//
// The root package promises JSON shapes: map[string]any, []any, float64. That
// is a lossy projection of CBOR, and naming it a projection is the point --
// it is what a program migrating from encoding/json wants, and it is not what
// a program that has to re-encode what it read wants. The full model lives in
// simdcbor/value and the codec that fills it in internal/codec; this file is
// the translation, with each loss stated where it happens.
//
// Before this, the root package had its own decoder and its own skipper, and
// they disagreed about what a well-formed item is in four places. Now both are
// one walk with a projection on top, so the disagreement has nowhere to live.

// adapterLimits are the shipped limits: depth 64, everything else default.
func adapterLimits() codec.Limits {
	return codec.Limits{MaxDepth: 64}
}

// adapterDecoder configures the codec for the shipped API's shapes.
func adapterDecoder(b []byte) *codec.Decoder {
	d := codec.New(b, adapterLimits())
	// Tags are dropped: the shipped API has no way to express one, so a
	// timestamp arrives as the integer underneath it. Lossy, and the reason
	// the value model exists for callers who cannot afford that.
	d.SetTagMode(codec.TagDiscard)
	// A Go map keeps one entry per key, so a duplicate loses either way; the
	// shipped behavior is that the last one wins.
	d.SetDuplicatePolicy(codec.DuplicateLastWins)
	return d
}

// project turns a value into the shipped API's Go shapes.
//
// Everything it refuses, it refuses because the shape cannot hold it: a map
// key that is not a string has no place in map[string]any, and a simple value
// outside the named four has no Go equivalent that survives a round trip.
// Refusing is the honest answer -- the alternative is inventing a value the
// caller cannot tell from a real one.
func project(v value.Value) (any, error) {
	switch v.Kind() {
	case value.Uint:
		n, _ := v.AsUint()
		return float64(n), nil
	case value.NegInt:
		f, _, _ := v.AsFloat64()
		return f, nil
	case value.Bytes:
		b, _ := v.AsBytes()
		return append([]byte(nil), b...), nil
	case value.Text:
		s, _ := v.AsText()
		return s, nil
	case value.Array:
		a, _ := v.AsArray()
		out := make([]any, len(a))
		for i, e := range a {
			p, err := project(e)
			if err != nil {
				return nil, err
			}
			out[i] = p
		}
		return out, nil
	case value.Map:
		m, _ := v.AsMap()
		out := make(map[string]any, len(m))
		for _, kv := range m {
			// JSON-shaped: string keys only. A byte string decodes to a Go
			// string too, which is what the shipped decoder accepted.
			var key string
			switch kv.Key.Kind() {
			case value.Text:
				key, _ = kv.Key.AsText()
			case value.Bytes:
				b, _ := kv.Key.AsBytes()
				key = string(b)
			default:
				return nil, ErrMalformed
			}
			p, err := project(kv.Value)
			if err != nil {
				return nil, err
			}
			out[key] = p
		}
		return out, nil
	case value.SimpleKind:
		n, _ := v.AsSimple()
		switch n {
		case 20:
			return false, nil
		case 21:
			return true, nil
		case 22, 23:
			// null and undefined both become nil: Go has one absent value and
			// CBOR has two, so re-encoding turns undefined into null.
			return nil, nil
		}
		return nil, ErrMalformed
	case value.Float16, value.Float32, value.Float64:
		f, _, _ := v.AsFloat64()
		return f, nil
	}
	return nil, ErrMalformed
}

// mapErr translates the codec's taxonomy to the two errors the root package
// exports. The distinction it keeps is the one a caller can act on: truncated
// means read more, malformed means stop.
func mapErr(err error) error {
	switch err {
	case nil:
		return nil
	case codec.ErrTruncated:
		return ErrTruncated
	}
	return ErrMalformed
}
