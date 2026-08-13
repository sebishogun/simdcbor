package codec

import "errors"

// What a decoder refuses, and why each limit exists.
//
// Every one of these is reachable from a handful of bytes off a socket. A
// nine-byte head can claim 2^64-1 elements; a few hundred bytes of nested
// array heads can recurse until the goroutine stack gives out. The limits are
// not tuning knobs, they are the difference between rejecting a hostile input
// and being the denial of service.

var (
	// ErrTruncated means the item continues past the end of the input. More
	// bytes might complete it, which is what separates it from ErrMalformed.
	ErrTruncated = errors.New("simdcbor: truncated")
	// ErrMalformed means the bytes cannot begin or continue a well-formed
	// item. More bytes will not help.
	ErrMalformed = errors.New("simdcbor: malformed")
	// ErrDepth means the item nests deeper than Limits.MaxDepth.
	ErrDepth = errors.New("simdcbor: nesting too deep")
	// ErrTooLarge means a declared length or count exceeds a limit.
	ErrTooLarge = errors.New("simdcbor: item exceeds a limit")
	// ErrUnsupported means a well-formed item this configuration will not
	// build -- an indefinite-length item under a definite-only mode, say.
	ErrUnsupported = errors.New("simdcbor: unsupported item")
)

// Limits bounds one decode.
type Limits struct {
	// MaxDepth caps container nesting. Zero uses DefaultMaxDepth.
	MaxDepth int
	// MaxStringBytes caps one byte or text string, including the total of an
	// indefinite string's chunks.
	MaxStringBytes int
	// MaxArrayElements caps one array's element count.
	MaxArrayElements int
	// MaxMapPairs caps one map's pair count.
	MaxMapPairs int
	// MaxTotalItems caps the items in one decode, so a deeply wide document
	// cannot cost unbounded work under limits that each look reasonable.
	MaxTotalItems int
}

// The defaults. Generous enough for ordinary documents, small enough that a
// hostile one is refused before it costs anything.
const (
	DefaultMaxDepth         = 64
	DefaultMaxStringBytes   = 64 << 20
	DefaultMaxArrayElements = 1 << 24
	DefaultMaxMapPairs      = 1 << 24
	DefaultMaxTotalItems    = 1 << 26
)

// DefaultLimits returns the limits a decoder uses when none are given.
func DefaultLimits() Limits {
	return Limits{
		MaxDepth:         DefaultMaxDepth,
		MaxStringBytes:   DefaultMaxStringBytes,
		MaxArrayElements: DefaultMaxArrayElements,
		MaxMapPairs:      DefaultMaxMapPairs,
		MaxTotalItems:    DefaultMaxTotalItems,
	}
}

// withDefaults fills zero fields, so a caller can set one limit without
// silently disabling the others.
func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	if l.MaxDepth == 0 {
		l.MaxDepth = d.MaxDepth
	}
	if l.MaxStringBytes == 0 {
		l.MaxStringBytes = d.MaxStringBytes
	}
	if l.MaxArrayElements == 0 {
		l.MaxArrayElements = d.MaxArrayElements
	}
	if l.MaxMapPairs == 0 {
		l.MaxMapPairs = d.MaxMapPairs
	}
	if l.MaxTotalItems == 0 {
		l.MaxTotalItems = d.MaxTotalItems
	}
	return l
}
