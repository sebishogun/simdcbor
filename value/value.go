package value

import (
	"math"
)

// Kind is what a Value holds.
type Kind uint8

const (
	Invalid Kind = iota
	Uint         // 0 .. 2^64-1
	NegInt       // -1-n for magnitude n, so -1 .. -2^64
	Bytes
	Text
	Array
	Map
	TagKind
	SimpleKind // includes the named False, True, Null, Undefined
	Float16
	Float32
	Float64
)

func (k Kind) String() string {
	switch k {
	case Uint:
		return "uint"
	case NegInt:
		return "negint"
	case Bytes:
		return "bytes"
	case Text:
		return "text"
	case Array:
		return "array"
	case Map:
		return "map"
	case TagKind:
		return "tag"
	case SimpleKind:
		return "simple"
	case Float16:
		return "float16"
	case Float32:
		return "float32"
	case Float64:
		return "float64"
	}
	return "invalid"
}

// Value is one CBOR data item.
//
// The zero Value is Invalid rather than a zero integer, so a value that was
// never set cannot pass for one that was.
type Value struct {
	kind Kind
	// num carries whatever is a number for this kind: an unsigned magnitude,
	// a simple value, the raw bits of a float, or a tag number.
	num uint64
	b   []byte     // Bytes, Text
	a   []Value    // Array, and the single tagged value for TagKind
	m   []KeyValue // Map
}

// KeyValue is one entry of a Map. Maps keep their order: CBOR maps are
// ordered on the wire, and re-encoding has to reproduce that unless a
// deterministic mode is asked for.
type KeyValue struct {
	Key   Value
	Value Value
}

// The named simple values. 20-23 in CBOR's simple space.
var (
	False     = Value{kind: SimpleKind, num: 20}
	True      = Value{kind: SimpleKind, num: 21}
	Null      = Value{kind: SimpleKind, num: 22}
	Undefined = Value{kind: SimpleKind, num: 23}
)

// Kind reports what the value holds.
func (v Value) Kind() Kind { return v.kind }

// ---- constructors ----

// FromUint returns a CBOR unsigned integer.
func FromUint(n uint64) Value { return Value{kind: Uint, num: n} }

// FromNegMagnitude returns the CBOR negative integer -1-n. The magnitude is
// what the wire carries, so n = 2^64-1 is the value -2^64 and is representable
// here even though no int64 holds it.
func FromNegMagnitude(n uint64) Value { return Value{kind: NegInt, num: n} }

// FromInt returns the CBOR integer with the mathematical value i.
func FromInt(i int64) Value {
	if i < 0 {
		return Value{kind: NegInt, num: uint64(-(i + 1))}
	}
	return Value{kind: Uint, num: uint64(i)}
}

// FromBytes returns a CBOR byte string. The slice is retained, not copied.
func FromBytes(b []byte) Value { return Value{kind: Bytes, b: b} }

// FromText returns a CBOR text string.
func FromText(s string) Value { return Value{kind: Text, b: []byte(s)} }

// FromArray returns a CBOR array.
func FromArray(vs ...Value) Value { return Value{kind: Array, a: vs} }

// FromMap returns a CBOR map, keeping the order given.
func FromMap(kvs ...KeyValue) Value { return Value{kind: Map, m: kvs} }

// FromTag returns a CBOR tag applied to v.
func FromTag(number uint64, v Value) Value {
	return Value{kind: TagKind, num: number, a: []Value{v}}
}

// FromSimple returns a CBOR simple value.
//
// 24-31 are reserved: the two-byte form may not carry them, and the one-byte
// form has no room for them, so there is no well-formed encoding to produce.
// ok is false for those, which is the only way to say so without a panic.
func FromSimple(n uint8) (Value, bool) {
	if n >= 24 && n <= 31 {
		return Value{}, false
	}
	return Value{kind: SimpleKind, num: uint64(n)}, true
}

// FromFloat16Bits returns a half-precision float holding exactly these bits.
func FromFloat16Bits(bits uint16) Value { return Value{kind: Float16, num: uint64(bits)} }

// FromFloat32Bits returns a single-precision float holding exactly these bits.
func FromFloat32Bits(bits uint32) Value { return Value{kind: Float32, num: uint64(bits)} }

// FromFloat64Bits returns a double-precision float holding exactly these bits.
func FromFloat64Bits(bits uint64) Value { return Value{kind: Float64, num: bits} }

// FromFloat64 returns a double holding f. It does not narrow: a value that
// would fit a float32 still encodes as a double unless a preferred-serialization
// pass narrows it, because narrowing here would lose the caller's choice.
func FromFloat64(f float64) Value { return Value{kind: Float64, num: math.Float64bits(f)} }

// ---- accessors ----

// AsUint returns the magnitude of a Uint.
func (v Value) AsUint() (uint64, bool) {
	if v.kind != Uint {
		return 0, false
	}
	return v.num, true
}

// AsNegMagnitude returns n for the negative integer -1-n.
func (v Value) AsNegMagnitude() (uint64, bool) {
	if v.kind != NegInt {
		return 0, false
	}
	return v.num, true
}

// AsInt returns the mathematical value as an int64, and false when it does not
// fit -- which is not an edge case: half of CBOR's unsigned range and the
// bottom of its negative range are both outside int64.
func (v Value) AsInt() (int64, bool) {
	switch v.kind {
	case Uint:
		if v.num > math.MaxInt64 {
			return 0, false
		}
		return int64(v.num), true
	case NegInt:
		if v.num > math.MaxInt64 {
			return 0, false
		}
		return -1 - int64(v.num), true
	}
	return 0, false
}

// AsBytes returns the payload of a byte or text string.
func (v Value) AsBytes() ([]byte, bool) {
	if v.kind != Bytes && v.kind != Text {
		return nil, false
	}
	return v.b, true
}

// AsText returns the contents of a text string.
func (v Value) AsText() (string, bool) {
	if v.kind != Text {
		return "", false
	}
	return string(v.b), true
}

// AsArray returns the elements of an array.
func (v Value) AsArray() ([]Value, bool) {
	if v.kind != Array {
		return nil, false
	}
	return v.a, true
}

// AsMap returns the entries of a map, in wire order.
func (v Value) AsMap() ([]KeyValue, bool) {
	if v.kind != Map {
		return nil, false
	}
	return v.m, true
}

// AsTag returns a tag's number and the value it wraps.
func (v Value) AsTag() (uint64, Value, bool) {
	if v.kind != TagKind || len(v.a) != 1 {
		return 0, Value{}, false
	}
	return v.num, v.a[0], true
}

// AsSimple returns the numeric simple value, including the named ones.
func (v Value) AsSimple() (uint8, bool) {
	if v.kind != SimpleKind {
		return 0, false
	}
	return uint8(v.num), true
}

// FloatBits returns the exact bits of a float, at its own width.
func (v Value) FloatBits() (uint64, bool) {
	switch v.kind {
	case Float16, Float32, Float64:
		return v.num, true
	}
	return 0, false
}

// IsBool reports whether the value is False or True.
func (v Value) IsBool() bool { return v.kind == SimpleKind && (v.num == 20 || v.num == 21) }

// IsNull reports whether the value is Null.
func (v Value) IsNull() bool { return v.kind == SimpleKind && v.num == 22 }

// IsUndefined reports whether the value is Undefined.
func (v Value) IsUndefined() bool { return v.kind == SimpleKind && v.num == 23 }

// AsFloat64 converts any number to a float64, and reports whether the
// conversion was exact.
//
// It is a separate call, and it reports exactness, because the lossy cases are
// ordinary rather than exotic: every unsigned above 2^53 loses precision, and
// so does every negative magnitude above it. A decoder that silently produced
// a float64 would turn 2^63+1 into 2^63 with nothing to say it had.
func (v Value) AsFloat64() (f float64, exact bool, ok bool) {
	switch v.kind {
	case Uint:
		f = float64(v.num)
		return f, uint64(f) == v.num && f < math.MaxUint64, true
	case NegInt:
		mag := float64(v.num)
		return -1 - mag, uint64(mag) == v.num && mag < math.MaxUint64, true
	case Float16:
		return float64(math.Float32frombits(halfToFloat32bits(uint16(v.num)))), true, true
	case Float32:
		return float64(math.Float32frombits(uint32(v.num))), true, true
	case Float64:
		return math.Float64frombits(v.num), true, true
	}
	return 0, false, false
}

// halfToFloat32bits widens an IEEE 754 binary16 to binary32, keeping NaN
// payloads and signalling bits rather than normalizing them.
func halfToFloat32bits(h uint16) uint32 {
	sign := uint32(h&0x8000) << 16
	exp := uint32(h>>10) & 0x1f
	mant := uint32(h & 0x03ff)
	switch exp {
	case 0:
		if mant == 0 {
			return sign // zero, keeping the sign
		}
		// Subnormal: normalize it into binary32's range.
		e := uint32(127 - 15 + 1)
		for mant&0x0400 == 0 {
			mant <<= 1
			e--
		}
		mant &= 0x03ff
		return sign | e<<23 | mant<<13
	case 0x1f:
		// Infinity or NaN; the payload moves up with the mantissa.
		return sign | 0xff<<23 | mant<<13
	}
	return sign | (exp+127-15)<<23 | mant<<13
}
