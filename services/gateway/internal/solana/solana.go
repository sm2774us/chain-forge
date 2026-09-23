// Package solana adapts a Solana JSON-RPC node to the indexer's Source
// interface, so the same reorg-aware indexer, event log, SSE stream and
// webhooks serve Solana with no changes to the core.
//
// The indexer works in contiguous heights with parent-hash links. Solana slots
// are not contiguous (skipped slots), but *block height* is, and every block
// carries `previousBlockhash`. So: height ↔ number, blockhash ↔ hash,
// previousBlockhash ↔ parentHash; slots are resolved through getBlocks.
package solana

import (
	"context"
	"errors"

	"github.com/chainforge/gateway/internal/indexer"
	"github.com/chainforge/gateway/internal/rpcx"
)

// DefaultLookback is how many recent slots are scanned to resolve a height.
const DefaultLookback = 512

// Source implements indexer.Source over Solana JSON-RPC.
type Source struct {
	C        rpcx.Caller
	Lookback uint64 // 0 = DefaultLookback
}

var confirmed = map[string]string{"commitment": "confirmed"}

// Head returns the confirmed block height.
func (s Source) Head(ctx context.Context) (uint64, error) {
	var h uint64
	err := rpcx.Call(ctx, s.C, "getBlockHeight", []any{confirmed}, &h)
	return h, err
}

// Block returns the block at the given height.
func (s Source) Block(ctx context.Context, height uint64) (indexer.Block, error) {
	head, err := s.Head(ctx)
	if err != nil {
		return indexer.Block{}, err
	}
	if height > head {
		return indexer.Block{}, indexer.ErrNotFound
	}
	var slot uint64
	if err := rpcx.Call(ctx, s.C, "getSlot", []any{confirmed}, &slot); err != nil {
		return indexer.Block{}, err
	}
	look := s.Lookback
	if look == 0 {
		look = DefaultLookback
	}
	from := uint64(0)
	if slot > look {
		from = slot - look
	}
	var slots []uint64
	if err := rpcx.Call(ctx, s.C, "getBlocks", []any{from, slot, confirmed}, &slots); err != nil {
		return indexer.Block{}, err
	}
	back := head - height
	if back >= uint64(len(slots)) {
		return indexer.Block{}, errors.New("solana: height outside the lookback window")
	}
	var raw struct {
		Blockhash         string   `json:"blockhash"`
		PreviousBlockhash string   `json:"previousBlockhash"`
		BlockHeight       *uint64  `json:"blockHeight"`
		BlockTime         *uint64  `json:"blockTime"`
		Signatures        []string `json:"signatures"`
	}
	cfg := map[string]any{"encoding": "json", "transactionDetails": "signatures", "rewards": false, "maxSupportedTransactionVersion": 0, "commitment": "confirmed"}
	if err := rpcx.Call(ctx, s.C, "getBlock", []any{slots[uint64(len(slots))-1-back], cfg}, &raw); err != nil {
		return indexer.Block{}, err
	}
	if raw.BlockHeight == nil || *raw.BlockHeight != height {
		return indexer.Block{}, indexer.ErrParentMismatch // the head moved mid-resolution; the indexer retries
	}
	b := indexer.Block{Number: height, Hash: raw.Blockhash, ParentHash: raw.PreviousBlockhash, TxCount: len(raw.Signatures)}
	if raw.BlockTime != nil {
		b.Timestamp = *raw.BlockTime
	}
	return b, nil
}
