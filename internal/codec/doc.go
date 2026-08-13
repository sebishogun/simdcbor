// Package codec is the CBOR head-argument-body machine: one decoder, one
// skipper, one stream, sharing a single reading of the grammar.
//
// The sharing is the design. The shipped package has a decoder and a skipper
// written separately, and they disagreed about what a well-formed item is in
// four places -- unsupported simple values, integer map keys, tagged string
// keys, invalid UTF-8 -- none of which any test found until one enumerated the
// whole head space. Two walks over one grammar drift; the fix is not more
// tests, it is one walk.
//
// Three distinctions this machine keeps that a decoder can lose:
//
//   - ErrTruncated against ErrMalformed. The first says more bytes might
//     complete the item, the second says they will not. A stream needs the
//     difference to decide between reading again and giving up, and a decoder
//     that returns one error for both makes that decision impossible.
//   - Indefinite length against an argument's value. Marking "indefinite" with
//     a sentinel argument collides: the sentinel anyone reaches for first,
//     ^uint64(0), is exactly what 1b ffffffffffffffff carries, so the largest
//     integer CBOR can express decodes as malformed. It is a separate return
//     value here, and the RFC appendix vectors caught the sentinel version on
//     their first run.
//   - A chunked text string is validated as its concatenation, not chunk by
//     chunk, because a chunk boundary may fall inside a multi-byte rune.
package codec
