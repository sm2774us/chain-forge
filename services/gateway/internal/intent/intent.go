// Package intent turns "what the user wants to do" into a simulated, priced,
// ready-to-sign transaction. The UI stays chain-agnostic: it sends an intent,
// gets back the predicted outcome (state changes, gas, reverts) plus the exact
// transaction to sign, all before anything touches a public network.
//
// Pre-state comes from the node pool ("shadow fork" of just the touched
// accounts); execution happens in the Rust engine's revm; nothing here holds a key.
package intent

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/chainforge/gateway/internal/engineclient"
	"github.com/chainforge/gateway/internal/rpcx"
)

// Simulator runs a transaction (engineclient.Client).
type Simulator interface {
	Simulate(ctx context.Context, r engineclient.SimRequest) (engineclient.SimResult, error)
}

// Service orchestrates state loading and simulation.
type Service struct {
	RPC    rpcx.Caller
	Sim    Simulator
	MaxGas uint64 // ceiling for requested/default gas (0 = 30M)
}

// InvalidError marks caller mistakes (HTTP 400) as opposed to infrastructure failures.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

func invalid(f string, a ...any) error { return &InvalidError{fmt.Sprintf(f, a...)} }

// EVMRequest is the chain-agnostic intent for EVM chains.
type EVMRequest struct {
	ChainID  uint64 `json:"chain_id"`
	From     string `json:"from"`
	To       string `json:"to"`
	Value    string `json:"value"`     // decimal wei
	Data     string `json:"data"`      // 0x hex
	GasLimit uint64 `json:"gas_limit"` // 0 = default
	GasPrice string `json:"gas_price"` // decimal wei; empty = node's eth_gasPrice
}

// Tx is the exact transaction to sign.
type Tx struct {
	ChainID  uint64 `json:"chain_id"`
	Nonce    uint64 `json:"nonce"`
	GasPrice string `json:"gas_price"`
	GasLimit uint64 `json:"gas_limit"`
	To       string `json:"to"`
	Value    string `json:"value"`
	Data     string `json:"data"`
}

// LogView is a JSON-friendly log.
type LogView struct {
	Address string   `json:"address"`
	Topics  []string `json:"topics"`
	Data    string   `json:"data"`
}

// ChangeView is a net balance movement in decimal wei (never floats).
type ChangeView struct {
	Address string `json:"address"`
	Before  string `json:"before"`
	After   string `json:"after"`
	Delta   string `json:"delta"`
}

// Simulation is the predicted outcome.
type Simulation struct {
	Outcome        string       `json:"outcome"`
	GasUsed        uint64       `json:"gas_used"`
	Output         string       `json:"output"`
	Reason         string       `json:"reason,omitempty"`
	Logs           []LogView    `json:"logs"`
	BalanceChanges []ChangeView `json:"balance_changes"`
	ElapsedUS      uint64       `json:"elapsed_us"`
	Created        string       `json:"created_address,omitempty"`
}

// Prepared is the response to an intent.
type Prepared struct {
	Tx         Tx         `json:"tx"`
	Simulation Simulation `json:"simulation"`
}

func hexAddr(s, field string) (engineclient.Address, error) {
	var a engineclient.Address
	b, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	if !strings.HasPrefix(s, "0x") || err != nil || len(b) != len(a) {
		return a, invalid("%s must be a 0x-prefixed 20-byte address", field)
	}
	copy(a[:], b)
	return a, nil
}

func decimal(s, field string) (*big.Int, error) {
	if s == "" {
		return new(big.Int), nil
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok || v.Sign() < 0 || v.BitLen() > 256 {
		return nil, invalid("%s must be a non-negative decimal integer", field)
	}
	return v, nil
}

func qty(ctx context.Context, c rpcx.Caller, method string, params ...any) (*big.Int, error) {
	var s string
	if err := rpcx.Call(ctx, c, method, params, &s); err != nil {
		return nil, err
	}
	v, ok := new(big.Int).SetString(strings.TrimPrefix(s, "0x"), 16)
	if !ok || !strings.HasPrefix(s, "0x") {
		return nil, fmt.Errorf("intent: %s returned a malformed quantity %q", method, s)
	}
	return v, nil
}

func (s *Service) account(ctx context.Context, a engineclient.Address) (engineclient.Account, error) {
	h := "0x" + hex.EncodeToString(a[:])
	bal, err := qty(ctx, s.RPC, "eth_getBalance", h, "latest")
	if err != nil {
		return engineclient.Account{}, err
	}
	nonce, err := qty(ctx, s.RPC, "eth_getTransactionCount", h, "latest")
	if err != nil {
		return engineclient.Account{}, err
	}
	var code string
	if err := rpcx.Call(ctx, s.RPC, "eth_getCode", []any{h, "latest"}, &code); err != nil {
		return engineclient.Account{}, err
	}
	cb, err := hex.DecodeString(strings.TrimPrefix(code, "0x"))
	if err != nil {
		return engineclient.Account{}, fmt.Errorf("intent: eth_getCode returned malformed code: %w", err)
	}
	return engineclient.Account{Address: a, Balance: bal, Nonce: nonce.Uint64(), Code: cb}, nil
}

// PrepareEVM validates the intent, loads pre-state, simulates and prices it.
func (s *Service) PrepareEVM(ctx context.Context, in EVMRequest) (Prepared, error) {
	if s.Sim == nil {
		return Prepared{}, invalid("EVM simulation is not configured on this gateway")
	}
	maxGas := s.MaxGas
	if maxGas == 0 {
		maxGas = 30_000_000
	}
	if in.ChainID == 0 {
		return Prepared{}, invalid("chain_id is required")
	}
	from, err := hexAddr(in.From, "from")
	if err != nil {
		return Prepared{}, err
	}
	var to *engineclient.Address
	if in.To != "" {
		a, err := hexAddr(in.To, "to")
		if err != nil {
			return Prepared{}, err
		}
		to = &a
	}
	value, err := decimal(in.Value, "value")
	if err != nil {
		return Prepared{}, err
	}
	data, err := hex.DecodeString(strings.TrimPrefix(in.Data, "0x"))
	if err != nil || (in.Data != "" && !strings.HasPrefix(in.Data, "0x")) {
		return Prepared{}, invalid("data must be 0x-prefixed hex")
	}
	gasLimit := in.GasLimit
	if gasLimit == 0 {
		gasLimit = min(1_000_000, maxGas)
	}
	if gasLimit > maxGas {
		return Prepared{}, invalid("gas_limit exceeds the %d ceiling", maxGas)
	}

	var gasPrice *big.Int
	if in.GasPrice == "" {
		if gasPrice, err = qty(ctx, s.RPC, "eth_gasPrice"); err != nil {
			return Prepared{}, err
		}
	} else if gasPrice, err = decimal(in.GasPrice, "gas_price"); err != nil {
		return Prepared{}, err
	}

	var head struct{ Number, Timestamp string }
	if err := rpcx.Call(ctx, s.RPC, "eth_getBlockByNumber", []any{"latest", false}, &head); err != nil {
		return Prepared{}, err
	}
	number, ok1 := new(big.Int).SetString(strings.TrimPrefix(head.Number, "0x"), 16)
	stamp, ok2 := new(big.Int).SetString(strings.TrimPrefix(head.Timestamp, "0x"), 16)
	if !ok1 || !ok2 {
		return Prepared{}, fmt.Errorf("intent: malformed latest block")
	}

	state := make([]engineclient.Account, 0, 2)
	fromAcct, err := s.account(ctx, from)
	if err != nil {
		return Prepared{}, err
	}
	state = append(state, fromAcct)
	if to != nil && *to != from {
		a, err := s.account(ctx, *to)
		if err != nil {
			return Prepared{}, err
		}
		state = append(state, a)
	}

	// Gas price is zero inside the simulation so an under-funded wallet still
	// gets an accurate prediction; the fee is reported via the returned tx.
	res, err := s.Sim.Simulate(ctx, engineclient.SimRequest{
		ChainID: in.ChainID, From: from, To: to, Value: value, Data: data, GasLimit: gasLimit,
		GasPrice: new(big.Int), State: state, BlockNumber: number.Uint64(), Timestamp: stamp.Uint64(),
	})
	if err != nil {
		return Prepared{}, err
	}

	tx := Tx{ChainID: in.ChainID, Nonce: fromAcct.Nonce, GasPrice: gasPrice.String(), GasLimit: gasLimit, Value: value.String(), Data: "0x" + hex.EncodeToString(data)}
	if to != nil {
		tx.To = "0x" + hex.EncodeToString(to[:])
	}
	if res.Outcome == engineclient.Success {
		tx.GasLimit = max(21_000, res.GasUsed*12/10) // 20% headroom over the measured use
	}
	return Prepared{Tx: tx, Simulation: view(res)}, nil
}

func view(r engineclient.SimResult) Simulation {
	v := Simulation{Outcome: string(r.Outcome), GasUsed: r.GasUsed, Output: "0x" + hex.EncodeToString(r.Output), Reason: r.Reason, ElapsedUS: r.ElapsedUS, Logs: []LogView{}, BalanceChanges: []ChangeView{}}
	for _, l := range r.Logs {
		lv := LogView{Address: "0x" + hex.EncodeToString(l.Address[:]), Topics: []string{}, Data: "0x" + hex.EncodeToString(l.Data)}
		for _, t := range l.Topics {
			lv.Topics = append(lv.Topics, "0x"+hex.EncodeToString(t))
		}
		v.Logs = append(v.Logs, lv)
	}
	for _, c := range r.BalanceChanges {
		v.BalanceChanges = append(v.BalanceChanges, ChangeView{
			Address: "0x" + hex.EncodeToString(c.Address[:]), Before: c.Before.String(), After: c.After.String(),
			Delta: new(big.Int).Sub(c.After, c.Before).String(),
		})
	}
	if r.Created != nil {
		v.Created = "0x" + hex.EncodeToString(r.Created[:])
	}
	return v
}

// SolanaSim is the node-side simulation of a Solana transaction.
type SolanaSim struct {
	Success       bool            `json:"success"`
	Err           json.RawMessage `json:"err,omitempty"`
	Logs          []string        `json:"logs"`
	UnitsConsumed uint64          `json:"units_consumed"`
}

// SimulateSolana simulates a base64 transaction via the node's simulateTransaction
// (signature verification off, recent blockhash replaced) — no signature needed yet.
func (s *Service) SimulateSolana(ctx context.Context, txB64 string) (SolanaSim, error) {
	if b, err := base64.StdEncoding.DecodeString(txB64); err != nil || len(b) == 0 {
		return SolanaSim{}, invalid("tx must be base64")
	}
	var out struct {
		Value struct {
			Err           json.RawMessage `json:"err"`
			Logs          []string        `json:"logs"`
			UnitsConsumed uint64          `json:"unitsConsumed"`
		} `json:"value"`
	}
	cfg := map[string]any{"encoding": "base64", "sigVerify": false, "replaceRecentBlockhash": true, "commitment": "confirmed"}
	if err := rpcx.Call(ctx, s.RPC, "simulateTransaction", []any{txB64, cfg}, &out); err != nil {
		return SolanaSim{}, err
	}
	sim := SolanaSim{Success: len(out.Value.Err) == 0 || string(out.Value.Err) == "null", Logs: out.Value.Logs, UnitsConsumed: out.Value.UnitsConsumed}
	if sim.Logs == nil {
		sim.Logs = []string{}
	}
	if !sim.Success {
		sim.Err = out.Value.Err
	}
	return sim, nil
}
