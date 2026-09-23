// Package indexer follows the canonical chain head, maintains a bounded window
// of recent blocks, detects reorgs by re-verifying the stored tip and parent
// linkage, and publishes block.new / block.reorged events to the event log.
package indexer

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chainforge/gateway/internal/eventlog"
)

// Block is the indexed projection of a chain block.
type Block struct {
	Number     uint64 `json:"number"`
	Hash       string `json:"hash"`
	ParentHash string `json:"parentHash"`
	Timestamp  uint64 `json:"timestamp"`
	TxCount    int    `json:"txCount"`
}

// Errors.
var (
	ErrNotFound       = errors.New("indexer: block not found")
	ErrParentMismatch = errors.New("indexer: parent hash mismatch (concurrent reorg); will retry")
)

// Source abstracts a node.
type Source interface {
	Head(ctx context.Context) (uint64, error)
	Block(ctx context.Context, n uint64) (Block, error)
}

// Indexer is safe for concurrent readers; Sync calls are serialised.
type Indexer struct {
	src    Source
	log    *eventlog.Log
	window int
	syncMu sync.Mutex
	mu     sync.RWMutex
	chain  []Block
}

// New builds an Indexer keeping the last `window` blocks.
func New(src Source, log *eventlog.Log, window int) *Indexer {
	return &Indexer{src: src, log: log, window: max(window, 1)}
}

func (ix *Indexer) emit(typ string, b Block) {
	// Block always marshals, so Append cannot fail here.
	_, _ = ix.log.Append("chain", typ, b)
}

func (ix *Indexer) store(c []Block) {
	ix.mu.Lock()
	ix.chain = c
	ix.mu.Unlock()
}

// Sync performs one catch-up pass.
func (ix *Indexer) Sync(ctx context.Context) error {
	ix.syncMu.Lock()
	defer ix.syncMu.Unlock()
	head, err := ix.src.Head(ctx)
	if err != nil {
		return err
	}
	ix.mu.RLock()
	chain := append([]Block(nil), ix.chain...)
	ix.mu.RUnlock()
	defer func() { ix.store(chain) }()

	for len(chain) > 0 { // unwind until stored tip is canonical again
		tip := chain[len(chain)-1]
		b, err := ix.src.Block(ctx, tip.Number)
		if err == nil && b.Hash == tip.Hash {
			break
		}
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		chain = chain[:len(chain)-1]
		ix.emit("block.reorged", tip)
	}
	var next uint64
	if len(chain) > 0 {
		next = chain[len(chain)-1].Number + 1
	} else if head+1 > uint64(ix.window) {
		next = head + 1 - uint64(ix.window)
	}
	for n := next; n <= head; n++ {
		b, err := ix.src.Block(ctx, n)
		if err != nil {
			return err
		}
		if len(chain) > 0 && b.ParentHash != chain[len(chain)-1].Hash {
			return ErrParentMismatch
		}
		chain = append(chain, b)
		if len(chain) > ix.window {
			chain = chain[1:]
		}
		ix.emit("block.new", b)
	}
	return nil
}

// Run polls until ctx is cancelled, reporting errors via onErr.
func (ix *Indexer) Run(ctx context.Context, every time.Duration, onErr func(error)) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := ix.Sync(ctx); err != nil && ctx.Err() == nil {
			onErr(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Blocks returns up to limit recent blocks, newest first.
func (ix *Indexer) Blocks(limit int) []Block {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	n := min(max(limit, 0), len(ix.chain))
	out := make([]Block, 0, n)
	for i := len(ix.chain) - 1; i >= len(ix.chain)-n; i-- {
		out = append(out, ix.chain[i])
	}
	return out
}

// Head returns the newest indexed block.
func (ix *Indexer) Head() (Block, bool) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	if len(ix.chain) == 0 {
		return Block{}, false
	}
	return ix.chain[len(ix.chain)-1], true
}

// Caller is the subset of upstream.Pool the RPC source needs.
type Caller interface {
	Call(ctx context.Context, body []byte) ([]byte, error)
}

// RPCSource implements Source over JSON-RPC.
type RPCSource struct{ C Caller }

func (s RPCSource) call(ctx context.Context, method string, params []any, out any) error {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	raw, err := s.C.Call(ctx, body)
	if err != nil {
		return err
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return err
	}
	if env.Error != nil {
		return errors.New(env.Error.Message)
	}
	if string(env.Result) == "null" {
		return ErrNotFound
	}
	return json.Unmarshal(env.Result, out)
}

func parseQty(s string) (uint64, error) {
	if !strings.HasPrefix(s, "0x") {
		return 0, errors.New("indexer: quantity missing 0x prefix")
	}
	return strconv.ParseUint(s[2:], 16, 64)
}

// Head implements Source.
func (s RPCSource) Head(ctx context.Context) (uint64, error) {
	var h string
	if err := s.call(ctx, "eth_blockNumber", []any{}, &h); err != nil {
		return 0, err
	}
	return parseQty(h)
}

// Block implements Source.
func (s RPCSource) Block(ctx context.Context, n uint64) (Block, error) {
	var raw struct {
		Number, Hash, ParentHash, Timestamp string
		Transactions                        []string
	}
	if err := s.call(ctx, "eth_getBlockByNumber", []any{"0x" + strconv.FormatUint(n, 16), false}, &raw); err != nil {
		return Block{}, err
	}
	num, err := parseQty(raw.Number)
	if err != nil {
		return Block{}, err
	}
	ts, err := parseQty(raw.Timestamp)
	if err != nil {
		return Block{}, err
	}
	return Block{num, raw.Hash, raw.ParentHash, ts, len(raw.Transactions)}, nil
}
