package relay

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type fake func(body []byte) ([]byte, error)

func (f fake) Call(_ context.Context, b []byte) ([]byte, error) { return f(b) }

func TestEVMSend(t *testing.T) {
	var method string
	c := fake(func(b []byte) ([]byte, error) {
		var q struct{ Method string }
		json.Unmarshal(b, &q)
		method = q.Method
		return []byte(`{"result":"0xabc"}`), nil
	})
	id, err := NewEVM(c).Send(context.Background(), "0x0102")
	if err != nil || id != "0xabc" || method != "eth_sendRawTransaction" {
		t.Fatalf("%q %q %v", id, method, err)
	}
}

func TestSolanaSend(t *testing.T) {
	var got map[string]any
	c := fake(func(b []byte) ([]byte, error) {
		json.Unmarshal(b, &got)
		return []byte(`{"result":"5sig"}`), nil
	})
	id, err := NewSolana(c).Send(context.Background(), "AQID")
	if err != nil || id != "5sig" || got["method"] != "sendTransaction" {
		t.Fatalf("%q %v %v", id, got, err)
	}
}

func TestRejectsMalformedAndPropagatesErrors(t *testing.T) {
	never := fake(func([]byte) ([]byte, error) { t.Fatal("must not reach the relay"); return nil, nil })
	huge := "0x" + strings.Repeat("00", maxRaw+1)
	for _, raw := range []string{"", "0x", "0xzz", "0102", huge} {
		if _, err := NewEVM(never).Send(context.Background(), raw); !errors.Is(err, ErrInvalid) {
			t.Errorf("evm %q: %v", raw[:min(len(raw), 8)], err)
		}
	}
	bigB64 := strings.Repeat("AAAA", maxRaw/3+10)
	for _, raw := range []string{"", "!!!", bigB64} {
		if _, err := NewSolana(never).Send(context.Background(), raw); !errors.Is(err, ErrInvalid) {
			t.Errorf("solana %q: %v", raw[:min(len(raw), 8)], err)
		}
	}
	down := fake(func([]byte) ([]byte, error) { return nil, errors.New("relay down") })
	if _, err := NewEVM(down).Send(context.Background(), "0x01"); err == nil {
		t.Fatal("relay failure must surface")
	}
}
