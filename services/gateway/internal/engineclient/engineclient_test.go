package engineclient

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"testing"

	"github.com/chainforge/gateway/internal/pbwire"
)

func golden(t *testing.T) (req, resp string) {
	raw, err := os.ReadFile("../../../../contracts/wire_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var g struct {
		Req  string `json:"simulate_request_hex"`
		Resp string `json:"simulate_response_hex"`
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	return g.Req, g.Resp
}

func TestEncodeMatchesSharedGolden(t *testing.T) {
	req, resp := golden(t)
	b, err := Encode(goldenRequest())
	if err != nil || hex.EncodeToString(b) != req {
		t.Fatalf("request drifted from golden:\n got %x\nwant %s (%v)", b, req, err)
	}
	if hex.EncodeToString(goldenResponseBytes()) != resp {
		t.Fatal("response fixture drifted from golden")
	}
}

func TestDecodeGoldenResponse(t *testing.T) {
	_, resp := golden(t)
	raw, _ := hex.DecodeString(resp)
	r, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Outcome != Success || r.GasUsed != 21000 || len(r.Output) != 1 || r.Output[0] != 0x2a || r.Reason != "ok" || r.ElapsedUS != 42 {
		t.Fatalf("%+v", r)
	}
	if len(r.Logs) != 1 || r.Logs[0].Address != addr20(0x22) || len(r.Logs[0].Topics) != 1 || r.Logs[0].Data[0] != 1 {
		t.Fatalf("logs: %+v", r.Logs)
	}
	bc := r.BalanceChanges[0]
	if bc.Address != addr20(0x11) || bc.Before.Cmp(eth(1)) != 0 || bc.After.Cmp(new(big.Int).Sub(eth(1), big.NewInt(1000))) != 0 {
		t.Fatalf("balance change: %+v", bc)
	}
	if r.Created == nil || *r.Created != addr20(0x33) {
		t.Fatalf("created: %v", r.Created)
	}
}

func TestDecodeOutcomesAndErrors(t *testing.T) {
	for n, want := range map[uint64]Outcome{1: Success, 2: Revert, 3: Halt} {
		var w pbwire.Buf
		w.Uint(1, n)
		if r, err := Decode(w.Bytes()); err != nil || r.Outcome != want {
			t.Fatalf("%d: %v %v", n, r.Outcome, err)
		}
	}
	bad := func(name string, mk func(w *pbwire.Buf)) {
		var w pbwire.Buf
		mk(&w)
		if _, err := Decode(w.Bytes()); err == nil {
			t.Errorf("%s must fail", name)
		}
	}
	bad("unknown outcome", func(w *pbwire.Buf) { w.Uint(1, 9) })
	bad("no outcome", func(w *pbwire.Buf) { w.Uint(2, 5) })
	bad("bad created", func(w *pbwire.Buf) { w.Uint(1, 1); w.Raw(8, []byte{1}) })
	bad("truncated log", func(w *pbwire.Buf) { w.Uint(1, 1); w.RawAlways(5, []byte{0x0a, 9}) })
	bad("bad log address", func(w *pbwire.Buf) {
		var l pbwire.Buf
		l.Raw(1, []byte{1})
		w.Uint(1, 1)
		w.RawAlways(5, l.Bytes())
	})
	bad("truncated change", func(w *pbwire.Buf) { w.Uint(1, 1); w.RawAlways(6, []byte{0x0a, 9}) })
	bad("bad change address", func(w *pbwire.Buf) {
		var c pbwire.Buf
		c.Raw(1, []byte{1})
		w.Uint(1, 1)
		w.RawAlways(6, c.Bytes())
	})
	if _, err := Decode([]byte{0x80}); err == nil {
		t.Error("truncated message must fail")
	}
	// unknown fields and mismatched wire types are ignored
	var w pbwire.Buf
	w.Uint(1, 1)
	w.Uint(99, 7)
	w.Uint(4, 7)
	if r, err := Decode(w.Bytes()); err != nil || r.Outcome != Success {
		t.Fatalf("forward compat: %v", err)
	}
}

func TestEncodeValidation(t *testing.T) {
	neg := goldenRequest()
	neg.Value = big.NewInt(-1)
	huge := goldenRequest()
	huge.GasPrice = new(big.Int).Lsh(big.NewInt(1), 256)
	badAcct := goldenRequest()
	badAcct.State[0].Balance = big.NewInt(-1)
	badSlot := goldenRequest()
	badSlot.State[0].Storage[0].Value = big.NewInt(-1)
	for name, r := range map[string]SimRequest{"negative": neg, "too wide": huge, "acct balance": badAcct, "slot value": badSlot} {
		if _, err := Encode(r); err == nil {
			t.Errorf("%s must fail", name)
		}
	}
	// creation (no To) with nil numbers encodes fine
	if _, err := Encode(SimRequest{ChainID: 1}); err != nil {
		t.Fatal(err)
	}
}

type inv func(ctx context.Context, method string, req []byte) ([]byte, error)

func (f inv) Invoke(ctx context.Context, m string, r []byte) ([]byte, error) { return f(ctx, m, r) }

func TestSimulateAndHealth(t *testing.T) {
	_, resp := golden(t)
	raw, _ := hex.DecodeString(resp)
	var method string
	c := New(inv(func(_ context.Context, m string, req []byte) ([]byte, error) {
		method = m
		if m == svc+"Health" {
			var w pbwire.Buf
			w.Uint(9, 1) // unknown field first
			w.Raw(1, []byte("0.1.0"))
			return w.Bytes(), nil
		}
		return raw, nil
	}))
	r, err := c.Simulate(context.Background(), goldenRequest())
	if err != nil || r.GasUsed != 21000 || method != svc+"Simulate" {
		t.Fatalf("%+v %v %q", r, err, method)
	}
	if v, err := c.Health(context.Background()); err != nil || v != "0.1.0" {
		t.Fatalf("%q %v", v, err)
	}
	// error paths
	if _, err := c.Simulate(context.Background(), SimRequest{Value: big.NewInt(-1)}); err == nil {
		t.Fatal("encode error must surface")
	}
	down := New(inv(func(context.Context, string, []byte) ([]byte, error) { return nil, errors.New("down") }))
	if _, err := down.Simulate(context.Background(), goldenRequest()); err == nil {
		t.Fatal("transport error must surface")
	}
	if _, err := down.Health(context.Background()); err == nil {
		t.Fatal("health transport error must surface")
	}
	empty := New(inv(func(context.Context, string, []byte) ([]byte, error) { return nil, nil }))
	if v, err := empty.Health(context.Background()); err != nil || v != "" {
		t.Fatalf("empty health: %q %v", v, err)
	}
	trunc := New(inv(func(context.Context, string, []byte) ([]byte, error) { return []byte{0x80}, nil }))
	if _, err := trunc.Health(context.Background()); err == nil {
		t.Fatal("truncated health must fail")
	}
}
