package rpcx

import (
	"context"
	"errors"
	"testing"
)

type fake func(body []byte) ([]byte, error)

func (f fake) Call(_ context.Context, b []byte) ([]byte, error) { return f(b) }

func TestCall(t *testing.T) {
	ctx := context.Background()
	var got string
	ok := fake(func(b []byte) ([]byte, error) { return []byte(`{"result":"0x1"}`), nil })
	if err := Call(ctx, ok, "m", []any{1}, &got); err != nil || got != "0x1" {
		t.Fatalf("%q %v", got, err)
	}
	if err := Call(ctx, ok, "m", nil, nil); err != nil {
		t.Fatal(err)
	}
	var re *Error
	rpcErr := fake(func([]byte) ([]byte, error) { return []byte(`{"error":{"code":-32000,"message":"boom"}}`), nil })
	if err := Call(ctx, rpcErr, "m", nil, &got); !errors.As(err, &re) || re.Code != -32000 || re.Error() != "rpc error -32000: boom" {
		t.Fatalf("%v", err)
	}
	null := fake(func([]byte) ([]byte, error) { return []byte(`{"result":null}`), nil })
	if err := Call(ctx, null, "m", nil, &got); !errors.Is(err, ErrNull) {
		t.Fatalf("%v", err)
	}
	bad := fake(func([]byte) ([]byte, error) { return []byte(`nope`), nil })
	if err := Call(ctx, bad, "m", nil, &got); err == nil {
		t.Fatal("malformed response must fail")
	}
	down := fake(func([]byte) ([]byte, error) { return nil, errors.New("down") })
	if err := Call(ctx, down, "m", nil, &got); err == nil {
		t.Fatal("transport error must propagate")
	}
	if err := Call(ctx, ok, "m", []any{make(chan int)}, &got); err == nil {
		t.Fatal("unmarshalable params must fail")
	}
	var n int
	if err := Call(ctx, ok, "m", nil, &n); err == nil {
		t.Fatal("type mismatch must fail")
	}
}
