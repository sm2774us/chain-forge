// Package pbwire is a dependency-free protobuf (proto3) wire codec — just
// enough for the engine contract. It is byte-compatible with prost/tonic on
// the Rust side, which contracts/wire_golden.json proves in both languages.
package pbwire

import (
	"errors"
	"math"
)

// Wire types.
const (
	Varint  = 0
	Fixed64 = 1
	Bytes   = 2
	Fixed32 = 5
)

// ErrTruncated is returned for malformed or cut-off input.
var ErrTruncated = errors.New("pbwire: truncated or malformed message")

// Buf builds a message. Zero values are omitted (proto3 semantics).
type Buf struct{ b []byte }

// Bytes returns the encoded message.
func (w *Buf) Bytes() []byte { return w.b }

func (w *Buf) varint(v uint64) {
	for v >= 0x80 {
		w.b = append(w.b, byte(v)|0x80)
		v >>= 7
	}
	w.b = append(w.b, byte(v))
}

// Uint writes a varint field.
func (w *Buf) Uint(field int, v uint64) {
	if v == 0 {
		return
	}
	w.varint(uint64(field)<<3 | Varint)
	w.varint(v)
}

// Raw writes a length-delimited field (bytes, string or nested message).
func (w *Buf) Raw(field int, v []byte) {
	if len(v) == 0 {
		return
	}
	w.RawAlways(field, v)
}

// RawAlways writes a length-delimited field even when empty (repeated/nested elements).
func (w *Buf) RawAlways(field int, v []byte) {
	w.varint(uint64(field)<<3 | Bytes)
	w.varint(uint64(len(v)))
	w.b = append(w.b, v...)
}

// Reader iterates the fields of one message.
type Reader struct{ b []byte }

// NewReader wraps an encoded message.
func NewReader(b []byte) *Reader { return &Reader{b: b} }

func (r *Reader) varint() (uint64, error) {
	var v uint64
	for shift := uint(0); shift < 64; shift += 7 {
		if len(r.b) == 0 {
			return 0, ErrTruncated
		}
		c := r.b[0]
		r.b = r.b[1:]
		v |= uint64(c&0x7f) << shift
		if c < 0x80 {
			return v, nil
		}
	}
	return 0, ErrTruncated
}

// Field is one decoded field; only the member matching Wire is meaningful.
type Field struct {
	Num  int
	Wire int
	Uint uint64
	Raw  []byte
}

// Next returns the next field, or ok=false at a clean end. Unknown fixed-width
// fields are skipped transparently so newer servers stay compatible.
func (r *Reader) Next() (f Field, ok bool, err error) {
	for len(r.b) > 0 {
		tag, err := r.varint()
		if err != nil {
			return Field{}, false, err
		}
		f = Field{Num: int(tag >> 3), Wire: int(tag & 7)}
		switch f.Wire {
		case Varint:
			if f.Uint, err = r.varint(); err != nil {
				return Field{}, false, err
			}
		case Bytes:
			n, err := r.varint()
			if err != nil || n > uint64(len(r.b)) || n > math.MaxInt32 {
				return Field{}, false, ErrTruncated
			}
			f.Raw, r.b = r.b[:n], r.b[n:]
		case Fixed64, Fixed32:
			n := 8
			if f.Wire == Fixed32 {
				n = 4
			}
			if len(r.b) < n {
				return Field{}, false, ErrTruncated
			}
			r.b = r.b[n:]
			continue
		default:
			return Field{}, false, ErrTruncated
		}
		return f, true, nil
	}
	return Field{}, false, nil
}
