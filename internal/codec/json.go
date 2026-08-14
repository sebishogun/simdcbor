package codec

import (
	"math"

	"github.com/sebishogun/simdcbor/value"
)

// Building JSON-shaped Go values directly off the walk.
//
// The root package promises map[string]any, []any and float64. Going through
// the full value model to get there costs two allocations and two passes per
// item, and measured 2-3x slower than the hand-written decoder it replaced --
// the bench gate caught it the moment the adapter landed.
//
// The fix is not to go back to two decoders. The defect that started this was
// two *walks* disagreeing about well-formedness; two *builders* over one walk
// share the head reader, the limits and the accept boundary, so the
// disagreement has nowhere to live. This is that second builder: same walk,
// different build step, no intermediate.

// DecodeJSON reads the next item as JSON-shaped Go values.
//
// It applies the projection's limits as it goes rather than afterwards: a map
// key that is not a string, or a simple value with no Go equivalent, fails
// where it is found, so nothing is built that then has to be thrown away.
func (d *Decoder) DecodeJSON() (any, error) {
	return d.decodeJSON(d.lim.MaxDepth)
}

func (d *Decoder) decodeJSON(depth int) (any, error) {
	if depth < 0 {
		return nil, ErrDepth
	}
	if err := d.count(); err != nil {
		return nil, err
	}
	mt, ai, arg, indef, err := d.head()
	if err != nil {
		return nil, err
	}
	switch mt {
	case mtUint:
		if indef {
			return nil, ErrMalformed
		}
		return float64(arg), nil
	case mtNegInt:
		if indef {
			return nil, ErrMalformed
		}
		// -1-n, computed in float64 because that is the shape being built;
		// above 2^53 it is lossy, which is the projection's cost, not a bug.
		return -1 - float64(arg), nil
	case mtBytes:
		b, err := d.stringBody(mt, arg, indef, depth)
		if err != nil {
			return nil, err
		}
		return append([]byte(nil), b...), nil
	case mtText:
		b, err := d.stringBody(mt, arg, indef, depth)
		if err != nil {
			return nil, err
		}
		return string(b), nil
	case mtArray:
		return d.jsonArray(arg, indef, depth)
	case mtMap:
		return d.jsonMap(arg, indef, depth)
	case mtTag:
		if indef {
			return nil, ErrMalformed
		}
		// Dropped: the shipped shapes have no way to carry a tag number.
		return d.decodeJSON(depth - 1)
	default:
		switch ai {
		case 20:
			return false, nil
		case 21:
			return true, nil
		case 22, 23:
			return nil, nil // null and undefined both, since Go has one
		case 25:
			// A half widens through the value model's conversion, which keeps
			// NaN payloads rather than normalizing them.
			f, _, _ := value.FromFloat16Bits(uint16(arg)).AsFloat64()
			return f, nil
		case 26:
			return float64(math.Float32frombits(uint32(arg))), nil
		case 27:
			return math.Float64frombits(arg), nil
		}
		// Every other simple value, including the reserved range and break.
		return nil, ErrMalformed
	}
}

func (d *Decoder) jsonArray(arg uint64, indef bool, depth int) (any, error) {
	if depth < 0 {
		return nil, ErrDepth
	}
	if indef {
		out := []any{}
		for {
			done, err := d.atBreak()
			if err != nil {
				return nil, err
			}
			if done {
				return out, nil
			}
			v, err := d.decodeJSON(depth - 1)
			if err != nil {
				return nil, err
			}
			if len(out) >= d.lim.MaxArrayElements {
				return nil, ErrTooLarge
			}
			out = append(out, v)
		}
	}
	if arg > uint64(d.lim.MaxArrayElements) {
		return nil, ErrTooLarge
	}
	if arg > uint64(len(d.b)-d.i) {
		return nil, ErrTruncated
	}
	out := make([]any, 0, arg)
	for k := uint64(0); k < arg; k++ {
		v, err := d.decodeJSON(depth - 1)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (d *Decoder) jsonMap(arg uint64, indef bool, depth int) (any, error) {
	if depth < 0 {
		return nil, ErrDepth
	}
	if indef {
		out := map[string]any{}
		for {
			done, err := d.atBreak()
			if err != nil {
				return nil, err
			}
			if done {
				return out, nil
			}
			if err := d.jsonPair(out, depth); err != nil {
				return nil, err
			}
			if len(out) >= d.lim.MaxMapPairs {
				return nil, ErrTooLarge
			}
		}
	}
	if arg > uint64(d.lim.MaxMapPairs) {
		return nil, ErrTooLarge
	}
	if arg > uint64(len(d.b)-d.i)/2 {
		return nil, ErrTruncated
	}
	out := make(map[string]any, min(int(arg), 1024))
	for k := uint64(0); k < arg; k++ {
		if err := d.jsonPair(out, depth); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// jsonPair reads one key and value. The key is read without building a value:
// only a string can be a key here, so anything else fails at its head.
func (d *Decoder) jsonPair(out map[string]any, depth int) error {
	if err := d.count(); err != nil {
		return err
	}
	mt, _, arg, indef, err := d.head()
	if err != nil {
		return err
	}
	var key string
	switch mt {
	case mtText, mtBytes:
		b, err := d.stringBody(mt, arg, indef, depth-1)
		if err != nil {
			return err
		}
		key = string(b)
	case mtTag:
		// A tagged key: the tag is dropped, so the key is whatever it wraps.
		if indef {
			return ErrMalformed
		}
		k, err := d.decodeJSON(depth - 1)
		if err != nil {
			return err
		}
		s, ok := k.(string)
		if !ok {
			return ErrMalformed
		}
		key = s
	default:
		// An integer, a float, an array: no place in map[string]any.
		return ErrMalformed
	}
	v, err := d.decodeJSON(depth - 1)
	if err != nil {
		return err
	}
	// Last wins, which is what a Go map does anyway; stating it means the
	// caller is not relying on iteration order to find out.
	out[key] = v
	return nil
}
