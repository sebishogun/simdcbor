package simdcbor

import "github.com/sebishogun/simdcbor/internal/codec"

// Skip advances past the CBOR item at the front of data without building
// any Go value, returning the number of bytes it spans. It is the hot
// path for filtering a stream of records -- frame each item, decode only
// the ones that match -- and CBOR's explicit lengths make it pure
// arithmetic: a string skips its byte count, a container skips its
// declared number of items, no allocation and no per-value interface.
//
// Errors are ErrTruncated for a short buffer and ErrMalformed for a head that
// cannot begin an item. Skip judges framing: the head is a head, the lengths
// fit, the nesting closes. It does not judge whether the item is one this
// package's value model can represent, so it accepts a superset of what
// Unmarshal does -- an integer map key, a simple value outside the model, a
// text string that is not valid UTF-8.
//
// That split is measured, not assumed. Skip once claimed the identical
// boundary, and making the claim true cost +92.5% instructions on the
// filter-stream benchmark: +10% for the value-model checks and +75% more for
// validating UTF-8 on strings it is skipping past. Skip exists to be the cheap
// arm of a filter, and paying to reject content the caller is discarding is
// backwards. Use SkipStrict where the identical boundary is what matters --
// docs/wrong.md carries the numbers.
func Skip(data []byte) (int, error) {
	d := codec.New(data, adapterLimits())
	n, err := d.Skip()
	return n, mapErr(err)
}

// SkipStrict is Skip with Unmarshal's boundary rather than framing's: a
// SkipStrict that succeeds is an item Unmarshal would decode, with the same
// span. It additionally rejects simple values outside the value model, map
// keys that would not decode to a Go string, and text strings that are not
// valid UTF-8.
//
// It is the arm to use when a skipped item still has to be known-decodable --
// the adapter's case. It costs about 1.9x what Skip does on a filtering
// workload, which is why the two are separate rather than one strict default.
func SkipStrict(data []byte) (int, error) {
	// Strict means "an item Unmarshal would decode", so it runs the same walk
	// and then the same projection, which is the only way to be sure the two
	// answers cannot drift apart again.
	d := adapterDecoder(data)
	if _, err := d.DecodeJSON(); err != nil {
		return 0, mapErr(err)
	}
	return d.Offset(), nil
}
