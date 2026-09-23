// Package engineclient is the typed Go side of the Rust simulation engine's
// gRPC contract (contracts/proto/chainforge/engine/v1/engine.proto).
package engineclient

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/chainforge/gateway/internal/pbwire"
)

// Address is a 20-byte account address.
type Address [20]byte

// Invoker performs a unary RPC (grpcx.Client).
type Invoker interface {
	Invoke(ctx context.Context, method string, req []byte) ([]byte, error)
}

const svc = "/chainforge.engine.v1.Engine/"

// Slot is one pre-seeded storage cell.
type Slot struct {
	Key   [32]byte
	Value *big.Int
}

// Account is pre-state supplied to the engine ("shadow fork" of the accounts touched).
type Account struct {
	Address Address
	Balance *big.Int
	Nonce   uint64
	Code    []byte
	Storage []Slot
}

// SimRequest describes one transaction to simulate.
type SimRequest struct {
	ChainID     uint64
	From        Address
	To          *Address // nil = contract creation
	Value       *big.Int
	Data        []byte
	GasLimit    uint64
	GasPrice    *big.Int
	State       []Account
	BlockNumber uint64
	Timestamp   uint64
}

// Log is an emitted EVM log.
type Log struct {
	Address Address
	Topics  [][]byte
	Data    []byte
}

// BalanceChange is a net wei movement for one account.
type BalanceChange struct {
	Address       Address
	Before, After *big.Int
}

// Outcome of an execution.
type Outcome string

// Outcomes.
const (
	Success Outcome = "success"
	Revert  Outcome = "revert"
	Halt    Outcome = "halt"
)

// SimResult is the engine's verdict.
type SimResult struct {
	Outcome        Outcome
	GasUsed        uint64
	Output         []byte
	Reason         string
	Logs           []Log
	BalanceChanges []BalanceChange
	ElapsedUS      uint64
	Created        *Address
}

// Client talks to the engine.
type Client struct{ inv Invoker }

// New wraps an Invoker.
func New(inv Invoker) *Client { return &Client{inv: inv} }

func word(v *big.Int) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	if v.Sign() < 0 || v.BitLen() > 256 {
		return nil, errors.New("engineclient: value must fit in an unsigned 256-bit integer")
	}
	return v.Bytes(), nil
}

func encodeAccount(a Account) ([]byte, error) {
	var w pbwire.Buf
	bal, err := word(a.Balance)
	if err != nil {
		return nil, err
	}
	w.Raw(1, a.Address[:])
	w.Raw(2, bal)
	w.Uint(3, a.Nonce)
	w.Raw(4, a.Code)
	for _, s := range a.Storage {
		v, err := word(s.Value)
		if err != nil {
			return nil, err
		}
		var sw pbwire.Buf
		sw.Raw(1, s.Key[:])
		sw.Raw(2, v)
		w.RawAlways(5, sw.Bytes())
	}
	return w.Bytes(), nil
}

// Encode serialises a request exactly as prost would.
func Encode(r SimRequest) ([]byte, error) {
	val, err := word(r.Value)
	if err != nil {
		return nil, err
	}
	price, err := word(r.GasPrice)
	if err != nil {
		return nil, err
	}
	var w pbwire.Buf
	w.Uint(1, r.ChainID)
	w.Raw(2, r.From[:])
	if r.To != nil {
		w.Raw(3, r.To[:])
	}
	w.Raw(4, val)
	w.Raw(5, r.Data)
	w.Uint(6, r.GasLimit)
	w.Raw(7, price)
	for _, a := range r.State {
		b, err := encodeAccount(a)
		if err != nil {
			return nil, err
		}
		w.RawAlways(8, b)
	}
	w.Uint(9, r.BlockNumber)
	w.Uint(10, r.Timestamp)
	return w.Bytes(), nil
}

func addr(b []byte) (Address, error) {
	var a Address
	if len(b) != len(a) {
		return a, errors.New("engineclient: address must be 20 bytes")
	}
	copy(a[:], b)
	return a, nil
}

func decodeLog(b []byte) (l Log, err error) {
	r := pbwire.NewReader(b)
	for {
		f, ok, err := r.Next()
		if err != nil || !ok {
			return l, err
		}
		switch {
		case f.Num == 1 && f.Wire == pbwire.Bytes:
			if l.Address, err = addr(f.Raw); err != nil {
				return l, err
			}
		case f.Num == 2 && f.Wire == pbwire.Bytes:
			l.Topics = append(l.Topics, append([]byte(nil), f.Raw...))
		case f.Num == 3 && f.Wire == pbwire.Bytes:
			l.Data = append([]byte(nil), f.Raw...)
		}
	}
}

func decodeChange(b []byte) (c BalanceChange, err error) {
	c.Before, c.After = new(big.Int), new(big.Int)
	r := pbwire.NewReader(b)
	for {
		f, ok, err := r.Next()
		if err != nil || !ok {
			return c, err
		}
		switch {
		case f.Num == 1 && f.Wire == pbwire.Bytes:
			if c.Address, err = addr(f.Raw); err != nil {
				return c, err
			}
		case f.Num == 2 && f.Wire == pbwire.Bytes:
			c.Before.SetBytes(f.Raw)
		case f.Num == 3 && f.Wire == pbwire.Bytes:
			c.After.SetBytes(f.Raw)
		}
	}
}

// Decode parses a SimulateResponse.
func Decode(b []byte) (SimResult, error) {
	var out SimResult
	r := pbwire.NewReader(b)
	for {
		f, ok, err := r.Next()
		if err != nil {
			return out, err
		}
		if !ok {
			break
		}
		switch {
		case f.Num == 1 && f.Wire == pbwire.Varint:
			switch f.Uint {
			case 1:
				out.Outcome = Success
			case 2:
				out.Outcome = Revert
			case 3:
				out.Outcome = Halt
			default:
				return out, fmt.Errorf("engineclient: unknown outcome %d", f.Uint)
			}
		case f.Num == 2 && f.Wire == pbwire.Varint:
			out.GasUsed = f.Uint
		case f.Num == 3 && f.Wire == pbwire.Bytes:
			out.Output = append([]byte(nil), f.Raw...)
		case f.Num == 4 && f.Wire == pbwire.Bytes:
			out.Reason = string(f.Raw)
		case f.Num == 5 && f.Wire == pbwire.Bytes:
			l, err := decodeLog(f.Raw)
			if err != nil {
				return out, err
			}
			out.Logs = append(out.Logs, l)
		case f.Num == 6 && f.Wire == pbwire.Bytes:
			c, err := decodeChange(f.Raw)
			if err != nil {
				return out, err
			}
			out.BalanceChanges = append(out.BalanceChanges, c)
		case f.Num == 7 && f.Wire == pbwire.Varint:
			out.ElapsedUS = f.Uint
		case f.Num == 8 && f.Wire == pbwire.Bytes:
			a, err := addr(f.Raw)
			if err != nil {
				return out, err
			}
			out.Created = &a
		}
	}
	if out.Outcome == "" {
		return out, errors.New("engineclient: response has no outcome")
	}
	return out, nil
}

// Simulate runs one transaction in the engine.
func (c *Client) Simulate(ctx context.Context, r SimRequest) (SimResult, error) {
	req, err := Encode(r)
	if err != nil {
		return SimResult{}, err
	}
	raw, err := c.inv.Invoke(ctx, svc+"Simulate", req)
	if err != nil {
		return SimResult{}, err
	}
	return Decode(raw)
}

// Health returns the engine version string.
func (c *Client) Health(ctx context.Context) (string, error) {
	raw, err := c.inv.Invoke(ctx, svc+"Health", nil)
	if err != nil {
		return "", err
	}
	r := pbwire.NewReader(raw)
	for {
		f, ok, err := r.Next()
		if err != nil {
			return "", err
		}
		if !ok {
			return "", nil
		}
		if f.Num == 1 && f.Wire == pbwire.Bytes {
			return string(f.Raw), nil
		}
	}
}
