// Package chainsim is a deterministic, in-memory EVM-style JSON-RPC node used
// for local development, CI and e2e. It supports forced reorgs so the indexer's
// fork handling can be exercised without a real network.
package chainsim

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/chainforge/gateway/internal/jsonrpc"
	"github.com/chainforge/gateway/internal/keccak"
)

const (
	blockTime   = 12
	genesisTime = 1_700_000_000
)

type blk struct {
	num    uint64
	hash   [32]byte
	parent [32]byte
	ts     uint64
	txs    int
}

// Node is a simulated chain.
type Node struct {
	mu      sync.RWMutex
	chainID uint64
	salt    uint64
	blocks  []blk
}

// New creates a chain containing only the genesis block.
func New(chainID uint64) *Node {
	n := &Node{chainID: chainID}
	n.mine()
	return n
}

func (n *Node) mine() {
	var parent [32]byte
	num := uint64(len(n.blocks))
	if num > 0 {
		parent = n.blocks[num-1].hash
	}
	var pre [48]byte
	copy(pre[:32], parent[:])
	binary.BigEndian.PutUint64(pre[32:], num)
	binary.BigEndian.PutUint64(pre[40:], n.salt)
	h := keccak.Sum256(pre[:])
	n.blocks = append(n.blocks, blk{num, h, parent, genesisTime + num*blockTime, int(h[0] % 8)})
}

// Mine appends one block.
func (n *Node) Mine() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.mine()
}

// Reorg drops the last depth blocks and mines depth+1 replacements on a new
// fork, so the canonical chain gets longer while the old tip hashes vanish.
func (n *Node) Reorg(depth int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if depth >= len(n.blocks) {
		depth = len(n.blocks) - 1
	}
	n.blocks = n.blocks[:len(n.blocks)-depth]
	n.salt++
	for i := 0; i <= depth; i++ {
		n.mine()
	}
}

func hx(b []byte) string  { return "0x" + hex.EncodeToString(b) }
func qty(v uint64) string { return "0x" + strconv.FormatUint(v, 16) }

func (b blk) json(full bool) map[string]any {
	txs := make([]string, b.txs)
	for i := range txs {
		var pre [40]byte
		copy(pre[:32], b.hash[:])
		binary.BigEndian.PutUint64(pre[32:], uint64(i))
		d := keccak.Sum256(pre[:])
		txs[i] = hx(d[:])
	}
	return map[string]any{"number": qty(b.num), "hash": hx(b.hash[:]), "parentHash": hx(b.parent[:]),
		"timestamp": qty(b.ts), "transactions": txs}
}

func (n *Node) resolve(tag string) (blk, bool) {
	head := uint64(len(n.blocks) - 1)
	switch tag {
	case "latest", "finalized", "safe", "pending":
		return n.blocks[head], true
	case "earliest":
		return n.blocks[0], true
	}
	v, err := strconv.ParseUint(strings.TrimPrefix(tag, "0x"), 16, 64)
	if err != nil || !strings.HasPrefix(tag, "0x") || v > head {
		return blk{}, false
	}
	return n.blocks[v], true
}

func params(raw json.RawMessage, want int) ([]string, bool) {
	var ps []string
	var mixed []json.RawMessage
	if json.Unmarshal(raw, &mixed) != nil || len(mixed) < want {
		return nil, false
	}
	for _, m := range mixed {
		var s string
		if json.Unmarshal(m, &s) != nil {
			s = string(m) // non-string (e.g. bool) keeps raw text
		}
		ps = append(ps, s)
	}
	return ps, true
}

// Handle implements jsonrpc.Handler.
func (n *Node) Handle(_ context.Context, q jsonrpc.Request) jsonrpc.Response {
	n.mu.RLock()
	defer n.mu.RUnlock()
	head := uint64(len(n.blocks) - 1)
	switch q.Method {
	case "eth_chainId":
		return jsonrpc.OK(q.ID, qty(n.chainID))
	case "eth_blockNumber":
		return jsonrpc.OK(q.ID, qty(head))
	case "eth_gasPrice":
		return jsonrpc.OK(q.ID, qty(20_000_000_000+(head%10)*1_000_000_000))
	case "eth_getBalance":
		ps, ok := params(q.Params, 1)
		if !ok {
			return jsonrpc.Fail(q.ID, jsonrpc.CodeInvalidParams, "expected [address, tag]")
		}
		a, err := keccak.ParseAddress(ps[0])
		if err != nil {
			return jsonrpc.Fail(q.ID, jsonrpc.CodeInvalidParams, err.Error())
		}
		d := keccak.Sum256(a[:])
		return jsonrpc.OK(q.ID, qty(binary.BigEndian.Uint64(d[:8])%1_000_000*1_000_000_000_000))
	case "eth_getTransactionCount":
		ps, ok := params(q.Params, 1)
		if _, err := keccak.ParseAddress(strings.Join(ps[:min(1, len(ps))], "")); !ok || err != nil {
			return jsonrpc.Fail(q.ID, jsonrpc.CodeInvalidParams, "expected [address, tag]")
		}
		return jsonrpc.OK(q.ID, qty(0)) // the simulator tracks no sender nonces
	case "eth_getCode":
		ps, ok := params(q.Params, 1)
		if _, err := keccak.ParseAddress(strings.Join(ps[:min(1, len(ps))], "")); !ok || err != nil {
			return jsonrpc.Fail(q.ID, jsonrpc.CodeInvalidParams, "expected [address, tag]")
		}
		return jsonrpc.OK(q.ID, "0x") // every account is an EOA
	case "eth_sendRawTransaction":
		ps, ok := params(q.Params, 1)
		if !ok {
			return jsonrpc.Fail(q.ID, jsonrpc.CodeInvalidParams, "expected [rawTx]")
		}
		raw, err := hex.DecodeString(strings.TrimPrefix(ps[0], "0x"))
		if err != nil || !strings.HasPrefix(ps[0], "0x") || len(raw) == 0 {
			return jsonrpc.Fail(q.ID, jsonrpc.CodeInvalidParams, "rawTx must be non-empty 0x hex")
		}
		d := keccak.Sum256(raw)
		return jsonrpc.OK(q.ID, hx(d[:])) // accepted; the tx hash is keccak256(raw), as on a real node
	case "eth_getBlockByNumber":
		ps, ok := params(q.Params, 1)
		if !ok {
			return jsonrpc.Fail(q.ID, jsonrpc.CodeInvalidParams, "expected [tag, fullTx]")
		}
		if b, found := n.resolve(ps[0]); found {
			return jsonrpc.OK(q.ID, b.json(false))
		}
		return jsonrpc.OK(q.ID, nil)
	case "eth_getBlockByHash":
		ps, ok := params(q.Params, 1)
		if !ok {
			return jsonrpc.Fail(q.ID, jsonrpc.CodeInvalidParams, "expected [hash, fullTx]")
		}
		for _, b := range n.blocks {
			if hx(b.hash[:]) == ps[0] {
				return jsonrpc.OK(q.ID, b.json(false))
			}
		}
		return jsonrpc.OK(q.ID, nil)
	}
	return jsonrpc.Fail(q.ID, jsonrpc.CodeMethodNotFound, fmt.Sprintf("method %s not found", q.Method))
}

// ServeHTTP exposes the node over HTTP JSON-RPC.
func (n *Node) ServeHTTP(w http.ResponseWriter, r *http.Request) { jsonrpc.Serve(w, r, n.Handle) }
