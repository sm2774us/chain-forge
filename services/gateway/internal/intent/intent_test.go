package intent

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/chainforge/gateway/internal/engineclient"
)

const (
	alice = "0x1111111111111111111111111111111111111111"
	bob   = "0x2222222222222222222222222222222222222222"
)

type rpcFake struct {
	res  map[string]string
	fail map[string]bool
	seen []string
}

func (f *rpcFake) Call(_ context.Context, body []byte) ([]byte, error) {
	var q struct {
		Method string
		Params []any
	}
	json.Unmarshal(body, &q)
	f.seen = append(f.seen, q.Method)
	if f.fail[q.Method] {
		return nil, errors.New("down: " + q.Method)
	}
	return []byte(`{"result":` + f.res[q.Method] + `}`), nil
}

func healthy() *rpcFake {
	return &rpcFake{res: map[string]string{
		"eth_gasPrice": `"0x3b9aca00"`, "eth_getBalance": `"0xde0b6b3a7640000"`, "eth_getTransactionCount": `"0x7"`, "eth_getCode": `"0x"`,
		"eth_getBlockByNumber": `{"number":"0x64","timestamp":"0x65000000"}`,
		"simulateTransaction":  `{"value":{"err":null,"logs":["Program log: ok"],"unitsConsumed":150}}`,
	}}
}

type simFake struct {
	got engineclient.SimRequest
	res engineclient.SimResult
	err error
}

func (s *simFake) Simulate(_ context.Context, r engineclient.SimRequest) (engineclient.SimResult, error) {
	s.got = r
	return s.res, s.err
}

func okResult() engineclient.SimResult {
	created := engineclient.Address{9}
	return engineclient.SimResult{
		Outcome: engineclient.Success, GasUsed: 50_000, Output: []byte{1}, ElapsedUS: 77, Created: &created,
		Logs:           []engineclient.Log{{Address: engineclient.Address{1}, Topics: [][]byte{{0xaa}}, Data: []byte{2}}},
		BalanceChanges: []engineclient.BalanceChange{{Address: engineclient.Address{3}, Before: big.NewInt(10), After: big.NewInt(4)}},
	}
}

func req() EVMRequest {
	return EVMRequest{ChainID: 1337, From: alice, To: bob, Value: "1000", Data: "0xdeadbeef"}
}

func TestPrepareEVMHappyPath(t *testing.T) {
	sim := &simFake{res: okResult()}
	svc := &Service{RPC: healthy(), Sim: sim}
	p, err := svc.PrepareEVM(context.Background(), req())
	if err != nil {
		t.Fatal(err)
	}
	// engine got real pre-state for both parties, block env, zero gas price and the default limit
	if len(sim.got.State) != 2 || sim.got.State[0].Nonce != 7 || sim.got.State[0].Balance.String() != "1000000000000000000" ||
		sim.got.BlockNumber != 100 || sim.got.Timestamp != 0x65000000 || sim.got.GasPrice.Sign() != 0 || sim.got.GasLimit != 1_000_000 || sim.got.Value.Int64() != 1000 {
		t.Fatalf("engine request: %+v", sim.got)
	}
	// tx is priced from the node and gas is measured+20%
	want := Tx{ChainID: 1337, Nonce: 7, GasPrice: "1000000000", GasLimit: 60_000, To: bob, Value: "1000", Data: "0xdeadbeef"}
	if p.Tx != want {
		t.Fatalf("tx: %+v", p.Tx)
	}
	s := p.Simulation
	if s.Outcome != "success" || s.GasUsed != 50_000 || s.ElapsedUS != 77 || s.Created != "0x0900000000000000000000000000000000000000" ||
		len(s.Logs) != 1 || s.Logs[0].Topics[0] != "0xaa" || len(s.BalanceChanges) != 1 || s.BalanceChanges[0].Delta != "-6" {
		t.Fatalf("simulation: %+v", s)
	}
}

func TestPrepareEVMVariants(t *testing.T) {
	ctx := context.Background()
	// same from/to → one account lookup; explicit gas price and limit; failed sim keeps requested gas; tiny use floors at 21000
	rp := healthy()
	sim := &simFake{res: engineclient.SimResult{Outcome: engineclient.Revert, Reason: "nope"}}
	r := EVMRequest{ChainID: 1, From: alice, To: alice, GasLimit: 90_000, GasPrice: "5"}
	p, err := (&Service{RPC: rp, Sim: sim, MaxGas: 100_000}).PrepareEVM(ctx, r)
	if err != nil || len(sim.got.State) != 1 || p.Tx.GasLimit != 90_000 || p.Tx.GasPrice != "5" || p.Simulation.Reason != "nope" || p.Simulation.Created != "" {
		t.Fatalf("%+v %v", p, err)
	}
	for _, m := range rp.seen {
		if m == "eth_gasPrice" {
			t.Fatal("explicit gas price must not hit the node")
		}
	}
	// contract creation (no To); default gas capped by MaxGas; floor at 21000
	sim = &simFake{res: engineclient.SimResult{Outcome: engineclient.Success, GasUsed: 100}}
	p, err = (&Service{RPC: healthy(), Sim: sim, MaxGas: 500}).PrepareEVM(ctx, EVMRequest{ChainID: 1, From: alice})
	if err != nil || sim.got.To != nil || p.Tx.To != "" || sim.got.GasLimit != 500 || p.Tx.GasLimit != 21_000 || p.Tx.Data != "0x" {
		t.Fatalf("%+v %+v %v", p, sim.got, err)
	}
}

func TestPrepareEVMValidation(t *testing.T) {
	ctx := context.Background()
	svc := &Service{RPC: healthy(), Sim: &simFake{res: okResult()}, MaxGas: 200_000}
	for name, mut := range map[string]func(*EVMRequest){
		"no chain":     func(r *EVMRequest) { r.ChainID = 0 },
		"bad from":     func(r *EVMRequest) { r.From = "0x12" },
		"from prefix":  func(r *EVMRequest) { r.From = strings.TrimPrefix(alice, "0x") },
		"bad to":       func(r *EVMRequest) { r.To = "nope" },
		"neg value":    func(r *EVMRequest) { r.Value = "-1" },
		"huge value":   func(r *EVMRequest) { r.Value = strings.Repeat("9", 80) },
		"junk value":   func(r *EVMRequest) { r.Value = "1.5" },
		"bad data":     func(r *EVMRequest) { r.Data = "0xzz" },
		"data prefix":  func(r *EVMRequest) { r.Data = "dead" },
		"gas ceiling":  func(r *EVMRequest) { r.GasLimit = 200_001 },
		"bad gasprice": func(r *EVMRequest) { r.GasPrice = "abc" },
	} {
		r := req()
		mut(&r)
		_, err := svc.PrepareEVM(ctx, r)
		var ie *InvalidError
		if !errors.As(err, &ie) || ie.Error() == "" {
			t.Errorf("%s: expected InvalidError, got %v", name, err)
		}
	}
}

func TestPrepareEVMInfrastructureFailures(t *testing.T) {
	ctx := context.Background()
	for _, m := range []string{"eth_gasPrice", "eth_getBlockByNumber", "eth_getBalance", "eth_getTransactionCount", "eth_getCode"} {
		rp := healthy()
		rp.fail = map[string]bool{m: true}
		if _, err := (&Service{RPC: rp, Sim: &simFake{}}).PrepareEVM(ctx, req()); err == nil {
			t.Errorf("%s failure must surface", m)
		}
	}
	// failure while loading the *recipient* account (second lookup)
	calls := 0
	rp := healthy()
	flaky := rpcFlaky{rpcFake: rp, failAt: 7, calls: &calls}
	if _, err := (&Service{RPC: flaky, Sim: &simFake{}}).PrepareEVM(ctx, req()); err == nil {
		t.Error("recipient lookup failure must surface")
	}
	if _, err := (&Service{RPC: healthy(), Sim: &simFake{err: errors.New("engine down")}}).PrepareEVM(ctx, req()); err == nil {
		t.Error("engine failure must surface")
	}
	for name, res := range map[string]map[string]string{
		"bad qty":       {"eth_getBalance": `"12"`},
		"bad code":      {"eth_getCode": `"0xzz"`},
		"bad block":     {"eth_getBlockByNumber": `{"number":"zz","timestamp":"0x1"}`},
		"bad timestamp": {"eth_getBlockByNumber": `{"number":"0x1","timestamp":"zz"}`},
		"bad gas price": {"eth_gasPrice": `"nope"`},
	} {
		rp := healthy()
		for k, v := range res {
			rp.res[k] = v
		}
		if _, err := (&Service{RPC: rp, Sim: &simFake{res: okResult()}}).PrepareEVM(ctx, req()); err == nil {
			t.Errorf("%s must fail", name)
		}
	}
}

// rpcFlaky fails the Nth call overall.
type rpcFlaky struct {
	*rpcFake
	failAt int
	calls  *int
}

func (f rpcFlaky) Call(ctx context.Context, b []byte) ([]byte, error) {
	*f.calls++
	if *f.calls == f.failAt {
		return nil, errors.New("flaky")
	}
	return f.rpcFake.Call(ctx, b)
}

func TestSolanaSimulation(t *testing.T) {
	ctx := context.Background()
	svc := &Service{RPC: healthy()}
	out, err := svc.SimulateSolana(ctx, "AQID")
	if err != nil || !out.Success || out.UnitsConsumed != 150 || len(out.Logs) != 1 || out.Err != nil {
		t.Fatalf("%+v %v", out, err)
	}
	rp := healthy()
	rp.res["simulateTransaction"] = `{"value":{"err":{"InstructionError":[0,"Custom"]},"logs":null,"unitsConsumed":0}}`
	out, err = (&Service{RPC: rp}).SimulateSolana(ctx, "AQID")
	if err != nil || out.Success || !strings.Contains(string(out.Err), "InstructionError") || out.Logs == nil {
		t.Fatalf("%+v %v", out, err)
	}
	for _, bad := range []string{"", "!!!"} {
		var ie *InvalidError
		if _, err := svc.SimulateSolana(ctx, bad); !errors.As(err, &ie) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	rp = healthy()
	rp.fail = map[string]bool{"simulateTransaction": true}
	if _, err := (&Service{RPC: rp}).SimulateSolana(ctx, "AQID"); err == nil {
		t.Error("rpc failure must surface")
	}
}

func TestPrepareEVMWithoutEngineIsRejected(t *testing.T) {
	var ie *InvalidError
	if _, err := (&Service{RPC: healthy()}).PrepareEVM(context.Background(), req()); !errors.As(err, &ie) {
		t.Fatalf("got %v", err)
	}
}
