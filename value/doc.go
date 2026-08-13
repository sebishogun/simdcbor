// Package value is the exact CBOR data model: kinds, values, map keys and the
// deterministic orderings.
//
// It holds no decoder and no encoder. Its job is to be the shape a decoder
// fills and an encoder reads, defined precisely enough that a round trip
// through it changes nothing:
//
//   - integers keep the full CBOR range, including the -2^64 endpoint that no
//     int64 holds, by storing a negative as its unsigned magnitude;
//   - floats keep their width and their exact bits, so -0.0 stays apart from
//     0.0 and two NaNs with different payloads stay two values;
//   - simple values cover the whole space rather than the four named ones;
//   - maps keep wire order, and a deterministic order is applied only when
//     asked for.
//
// Map keys are compared as their canonical encoding, because that is the only
// definition that makes the three spellings of 1.0 one key while keeping -0.0
// and a NaN payload distinct. The two RFC 8949 orderings sort those encodings
// including the head byte, which is why "z" sorts before "aa".
package value
