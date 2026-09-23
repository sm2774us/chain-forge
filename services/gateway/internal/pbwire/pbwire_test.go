package pbwire

import (
	"bytes"
	"errors"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	var w Buf
	w.Uint(1, 0)   // omitted
	w.Uint(2, 300) // multi-byte varint
	w.Raw(3, nil)  // omitted
	w.Raw(4, []byte("hi"))
	w.RawAlways(5, nil) // kept
	r := NewReader(w.Bytes())
	var got []Field
	for {
		f, ok, err := r.Next()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		got = append(got, f)
	}
	if len(got) != 3 || got[0].Num != 2 || got[0].Uint != 300 || got[1].Num != 4 || !bytes.Equal(got[1].Raw, []byte("hi")) || got[2].Num != 5 || len(got[2].Raw) != 0 {
		t.Fatalf("unexpected fields: %+v", got)
	}
}

func TestKnownEncoding(t *testing.T) {
	var w Buf
	w.Uint(1, 150) // canonical example from the protobuf docs: 08 96 01
	if !bytes.Equal(w.Bytes(), []byte{0x08, 0x96, 0x01}) {
		t.Fatalf("got %x", w.Bytes())
	}
}

func TestSkipsFixedWidthFields(t *testing.T) {
	// field 1 fixed64, field 2 fixed32, field 3 varint=7
	msg := []byte{0x09, 1, 2, 3, 4, 5, 6, 7, 8, 0x15, 1, 2, 3, 4, 0x18, 7}
	f, ok, err := NewReader(msg).Next()
	if err != nil || !ok || f.Num != 3 || f.Uint != 7 {
		t.Fatalf("got %+v %v %v", f, ok, err)
	}
}

func TestMalformedInputs(t *testing.T) {
	for name, msg := range map[string][]byte{
		"cut tag":            {0x80},
		"tag then no varint": {0x08},
		"varint too long":    {0x08, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01},
		"len past end":       {0x12, 0x05, 'a'},
		"len varint cut":     {0x12, 0x80},
		"short fixed64":      {0x09, 1, 2},
		"short fixed32":      {0x15, 1},
		"group wire type":    {0x0b},
	} {
		if _, ok, err := NewReader(msg).Next(); ok || !errors.Is(err, ErrTruncated) {
			t.Errorf("%s: ok=%v err=%v", name, ok, err)
		}
	}
	if _, ok, err := NewReader(nil).Next(); ok || err != nil {
		t.Fatalf("empty: %v %v", ok, err)
	}
}
