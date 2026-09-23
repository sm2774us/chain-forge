package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/chainforge/gateway/internal/grpcx"
	"github.com/chainforge/gateway/internal/intent"
	"github.com/chainforge/gateway/internal/relay"
	"github.com/chainforge/gateway/internal/rpcx"
	"github.com/chainforge/gateway/internal/signerclient"
)

type intentFake struct {
	prep intent.Prepared
	sol  intent.SolanaSim
	err  error
	got  intent.EVMRequest
}

func (f *intentFake) PrepareEVM(_ context.Context, r intent.EVMRequest) (intent.Prepared, error) {
	f.got = r
	return f.prep, f.err
}
func (f *intentFake) SimulateSolana(context.Context, string) (intent.SolanaSim, error) {
	return f.sol, f.err
}

type relayFake struct {
	id  string
	err error
	raw string
}

func (f *relayFake) Send(_ context.Context, raw string) (string, error) {
	f.raw = raw
	return f.id, f.err
}

func okPrep() intent.Prepared {
	return intent.Prepared{
		Tx:         intent.Tx{ChainID: 1337, Nonce: 3, GasPrice: "9", GasLimit: 25200, To: "0x22", Value: "5", Data: "0x"},
		Simulation: intent.Simulation{Outcome: "success", GasUsed: 21000, ElapsedUS: 55},
	}
}

func decode(t *testing.T, body string) map[string]any {
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return m
}

func TestIntentNotConfigured(t *testing.T) {
	r := newRig(t)
	for _, p := range []string{"/v1/simulate", "/v1/intent"} {
		if c := r.do("POST", p, `{}`).Code; c != http.StatusNotImplemented {
			t.Fatalf("%s: %d", p, c)
		}
	}
	if c := r.do("POST", "/v1/broadcast", `{"raw_tx":"0x01"}`).Code; c != http.StatusNotImplemented {
		t.Fatalf("broadcast: %d", c)
	}
}

func TestSimulateNeverSignsButIntentCan(t *testing.T) {
	f := &intentFake{prep: okPrep()}
	r := newRigWith(t, func(d *Deps) { d.Intents = f })
	r.signer.txOut = signerclient.TxResponse{Address: "0xa", RawTx: "0xraw", TxHash: "0xhash", SigningHash: "0xsh"}
	body := `{"chain_id":1337,"from":"0x11","to":"0x22","value":"5","sign":{"key_id":"alice"}}`

	// /v1/simulate ignores `sign`
	rec := r.do("POST", "/v1/simulate", body)
	m := decode(t, rec.Body.String())
	if rec.Code != 200 || m["signed"] != nil || m["chain"] != "evm" || f.got.ChainID != 1337 || r.signer.txReq.KeyID != "" {
		t.Fatalf("simulate: %d %s", rec.Code, rec.Body)
	}
	// /v1/intent signs the exact simulated tx
	rec = r.do("POST", "/v1/intent", body)
	m = decode(t, rec.Body.String())
	if rec.Code != 200 || m["signed"].(map[string]any)["raw_tx"] != "0xraw" || r.signer.txReq.GasLimit != 25200 || r.signer.txReq.KeyID != "alice" || r.signer.txReq.Nonce != 3 {
		t.Fatalf("intent: %d %s %+v", rec.Code, rec.Body, r.signer.txReq)
	}
	if r.h == nil || !strings.Contains(r.do("GET", "/metrics", "").Body.String(), `gateway_simulations_total{chain="evm",outcome="success"} 2`) {
		t.Fatal("simulation metrics missing")
	}
	// intent without sign returns just the prediction
	rec = r.do("POST", "/v1/intent", `{"chain":"evm","chain_id":1337,"from":"0x11"}`)
	if m := decode(t, rec.Body.String()); rec.Code != 200 || m["signed"] != nil || m["signing_skipped"] != nil {
		t.Fatalf("no-sign: %s", rec.Body)
	}
}

func TestSigningIsSkippedUnlessSimulationSucceeds(t *testing.T) {
	f := &intentFake{prep: okPrep()}
	f.prep.Simulation.Outcome = "revert"
	r := newRigWith(t, func(d *Deps) { d.Intents = f })
	rec := r.do("POST", "/v1/intent", `{"chain_id":1,"from":"0x11","sign":{"key_id":"alice"}}`)
	if m := decode(t, rec.Body.String()); !strings.Contains(m["signing_skipped"].(string), "revert") || m["signed"] != nil || r.signer.txReq.KeyID != "" {
		t.Fatalf("revert must not be signed: %s", rec.Body)
	}
	f.prep = okPrep()
	f.prep.Tx.To = ""
	rec = r.do("POST", "/v1/intent", `{"chain_id":1,"from":"0x11","sign":{"key_id":"alice"}}`)
	if m := decode(t, rec.Body.String()); !strings.Contains(m["signing_skipped"].(string), "creation") {
		t.Fatalf("creation: %s", rec.Body)
	}
}

func TestSignerFailuresDuringIntent(t *testing.T) {
	f := &intentFake{prep: okPrep()}
	r := newRigWith(t, func(d *Deps) { d.Intents = f })
	body := `{"chain_id":1,"from":"0x11","sign":{"key_id":"alice"}}`
	r.signer.txErr = &signerclient.APIError{Status: 403, Message: "chain id not allowed"}
	if rec := r.do("POST", "/v1/intent", body); rec.Code != 403 || !strings.Contains(rec.Body.String(), "chain id not allowed") {
		t.Fatalf("policy: %d %s", rec.Code, rec.Body)
	}
	r.signer.txErr = errors.New("down")
	if rec := r.do("POST", "/v1/intent", body); rec.Code != http.StatusBadGateway {
		t.Fatalf("down: %d", rec.Code)
	}
}

func TestSolanaSimulation(t *testing.T) {
	f := &intentFake{sol: intent.SolanaSim{Success: true, Logs: []string{"ok"}, UnitsConsumed: 9}}
	r := newRigWith(t, func(d *Deps) { d.Intents = f })
	rec := r.do("POST", "/v1/simulate", `{"chain":"solana","tx":"AQID"}`)
	if m := decode(t, rec.Body.String()); rec.Code != 200 || m["chain"] != "solana" || m["simulation"].(map[string]any)["units_consumed"] != float64(9) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	f.sol.Success = false
	if rec := r.do("POST", "/v1/simulate", `{"chain":"solana","tx":"AQID"}`); rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	if rec := r.do("POST", "/v1/intent", `{"chain":"solana","tx":"AQID","sign":{"key_id":"a"}}`); rec.Code != 400 {
		t.Fatalf("custodial solana must be refused: %d", rec.Code)
	}
	f.err = &intent.InvalidError{Msg: "tx must be base64"}
	if rec := r.do("POST", "/v1/simulate", `{"chain":"solana","tx":"!"}`); rec.Code != 400 {
		t.Fatalf("%d", rec.Code)
	}
}

func TestBadIntentBodies(t *testing.T) {
	r := newRigWith(t, func(d *Deps) { d.Intents = &intentFake{prep: okPrep()} })
	for _, b := range []string{`nope`, `{"chain_id":1,"extra":true}`} {
		if c := r.do("POST", "/v1/simulate", b).Code; c != 400 {
			t.Errorf("%s: %d", b, c)
		}
	}
	if c := r.do("POST", "/v1/simulate", `{"chain":"bitcoin"}`).Code; c != 400 {
		t.Errorf("unknown chain: %d", c)
	}
}

func TestErrorMapping(t *testing.T) {
	f := &intentFake{}
	r := newRigWith(t, func(d *Deps) { d.Intents = f })
	for name, tc := range map[string]struct {
		err  error
		code int
		frag string
	}{
		"invalid":       {&intent.InvalidError{Msg: "bad from"}, 400, "bad from"},
		"grpc invalid":  {&grpcx.Status{Code: grpcx.InvalidArgument, Message: "from must be 20 bytes"}, 400, "20 bytes"},
		"grpc gas":      {&grpcx.Status{Code: grpcx.ResourceExhausted, Message: "gas ceiling"}, 422, "gas ceiling"},
		"grpc rejected": {&grpcx.Status{Code: grpcx.FailedPrecondition, Message: "no funds"}, 422, "no funds"},
		"grpc down":     {&grpcx.Status{Code: grpcx.Unavailable, Message: "refused"}, 503, "unavailable"},
		"grpc timeout":  {&grpcx.Status{Code: grpcx.DeadlineExceeded}, 503, "unavailable"},
		"grpc other":    {&grpcx.Status{Code: grpcx.Internal}, 502, "engine error"},
		"node":          {&rpcx.Error{Code: -32000, Message: "boom"}, 502, "node error: boom"},
		"generic":       {errors.New("x"), 502, "upstream unavailable"},
		"relay invalid": {relay.ErrInvalid, 400, "malformed"},
	} {
		f.err = tc.err
		rec := r.do("POST", "/v1/simulate", `{"chain_id":1,"from":"0x11"}`)
		if rec.Code != tc.code || !strings.Contains(rec.Body.String(), tc.frag) {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
}

func TestBroadcast(t *testing.T) {
	evm, sol := &relayFake{id: "0xabc"}, &relayFake{id: "5sig"}
	r := newRigWith(t, func(d *Deps) { d.Relays = map[string]Broadcaster{"evm": evm, "solana": sol} })
	rec := r.do("POST", "/v1/broadcast", `{"raw_tx":"0x01"}`) // chain defaults to evm
	if m := decode(t, rec.Body.String()); rec.Code != 200 || m["tx_id"] != "0xabc" || m["chain"] != "evm" || evm.raw != "0x01" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	rec = r.do("POST", "/v1/broadcast", `{"chain":"solana","raw_tx":"AQID"}`)
	if m := decode(t, rec.Body.String()); m["tx_id"] != "5sig" {
		t.Fatalf("%s", rec.Body)
	}
	for _, b := range []string{`nope`, `{"chain":"evm"}`, `{"raw_tx":"0x01","x":1}`} {
		if c := r.do("POST", "/v1/broadcast", b).Code; c != 400 {
			t.Errorf("%s: %d", b, c)
		}
	}
	if c := r.do("POST", "/v1/broadcast", `{"chain":"bitcoin","raw_tx":"x"}`).Code; c != http.StatusNotImplemented {
		t.Errorf("unknown relay: %d", c)
	}
	evm.err = relay.ErrInvalid
	if c := r.do("POST", "/v1/broadcast", `{"raw_tx":"zz"}`).Code; c != 400 {
		t.Errorf("invalid raw: %d", c)
	}
	evm.err = &rpcx.Error{Code: -32000, Message: "nonce too low"}
	if rec := r.do("POST", "/v1/broadcast", `{"raw_tx":"0x01"}`); rec.Code != 502 || !strings.Contains(rec.Body.String(), "nonce too low") {
		t.Errorf("relay rejection: %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(r.do("GET", "/metrics", "").Body.String(), `gateway_broadcasts_total{chain="evm",outcome="error"} 2`) {
		t.Error("broadcast metrics missing")
	}
}
