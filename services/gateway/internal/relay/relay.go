// Package relay submits signed transactions through private relays (Flashbots
// Protect-style for EVM, Jito-style block engines for Solana) instead of the
// public mempool, which is the MEV/front-running protection point. Relays are
// plain JSON-RPC endpoints behind an upstream.Pool, so failover, circuit
// breaking and health reporting come for free.
package relay

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/chainforge/gateway/internal/rpcx"
)

// ErrInvalid means the raw transaction is malformed.
var ErrInvalid = errors.New("relay: malformed raw transaction")

const maxRaw = 128 << 10

// Sender submits raw transactions for one chain family.
type Sender struct {
	c      rpcx.Caller
	solana bool
}

// NewEVM submits with eth_sendRawTransaction.
func NewEVM(c rpcx.Caller) *Sender { return &Sender{c: c} }

// NewSolana submits with sendTransaction (base64).
func NewSolana(c rpcx.Caller) *Sender { return &Sender{c: c, solana: true} }

// Send validates and submits raw, returning the transaction hash/signature.
func (s *Sender) Send(ctx context.Context, raw string) (string, error) {
	var id string
	if s.solana {
		b, err := base64.StdEncoding.DecodeString(raw)
		if err != nil || len(b) == 0 || len(b) > maxRaw {
			return "", ErrInvalid
		}
		err = rpcx.Call(ctx, s.c, "sendTransaction", []any{raw, map[string]any{"encoding": "base64", "skipPreflight": false}}, &id)
		return id, err
	}
	body, ok := strings.CutPrefix(raw, "0x")
	if b, err := hex.DecodeString(body); !ok || err != nil || len(b) == 0 || len(b) > maxRaw {
		return "", ErrInvalid
	}
	err := rpcx.Call(ctx, s.c, "eth_sendRawTransaction", []any{raw}, &id)
	return id, err
}
