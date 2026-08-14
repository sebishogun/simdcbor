// Package diag renders and parses RFC 8949 diagnostic notation.
//
// The notation is what a CBOR item looks like when a person has to read it,
// and it carries distinctions JSON has no way to write down. Reproducing them
// is the whole job:
//
//   - 1.0 keeps its point, because a float and an integer that happen to be
//     equal are different items and notation that printed "1" would parse back
//     to the wrong one;
//   - a byte string is h'0102', never a text string;
//   - an indefinite container carries an underscore, because a length that was
//     not declared is a fact about the encoding that a re-encode has to
//     reproduce;
//   - simple values outside the four named ones keep their number, so
//     simple(32) survives a round trip through the notation;
//   - NaN, Infinity and -Infinity are spelled with capitals, as RFC 8949
//     section 8 spells them.
//
// Parse and Render are inverses over the value model, and the round trip is
// asserted on the whole vector table: anything the renderer drops, the parser
// cannot recover, so dropping is the thing to watch for.
//
// A truncated document renders the items that were complete, alongside the
// error. That is the useful answer for someone looking at a broken document --
// how far it got -- where returning nothing says only that something is wrong.
package diag
