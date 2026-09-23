package solana

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/chainforge/gateway/internal/indexer"
)

// script answers each JSON-RPC method from a table of raw results.
type script struct {
	res  map[string]string
	fail map[string]bool
	seen map[string][]any
}

func (s *script) Call(_ context.Context, body []byte) ([]byte, error) {
	var q struct {
		Method string
		Params []any
	}
	json.Unmarshal(body, &q)
	if s.seen == nil {
		s.seen = map[string][]any{}
	}
	s.seen[q.Method] = q.Params
	if s.fail[q.Method] {
		return nil, errors.New("down: " + q.Method)
	}
	return []byte(`{"result":` + s.res[q.Method] + `}`), nil
}

func base() *script {
	return &script{res: map[string]string{
		"getBlockHeight": "1000",
		"getSlot":        "1300",
		"getBlocks":      "[1290,1291,1293,1295,1300]", // skipped slots in between
		"getBlock":       `{"blockhash":"H999","previousBlockhash":"H998","blockHeight":999,"blockTime":1700000000,"signatures":["a","b"]}`,
	}}
}

func TestBlockMapsHeightToSlotAcrossSkippedSlots(t *testing.T) {
	s := base()
	b, err := Source{C: s}.Block(context.Background(), 999)
	if err != nil {
		t.Fatal(err)
	}
	want := indexer.Block{Number: 999, Hash: "H999", ParentHash: "H998", Timestamp: 1700000000, TxCount: 2}
	if b != want {
		t.Fatalf("got %+v", b)
	}
	if s.seen["getBlock"][0].(float64) != 1295 { // head-1 → second-to-last produced slot
		t.Fatalf("resolved wrong slot: %v", s.seen["getBlock"])
	}
	if s.seen["getBlocks"][0].(float64) != 788 { // 1300 - default lookback 512
		t.Fatalf("lookback: %v", s.seen["getBlocks"])
	}
}

func TestHeadAndEdges(t *testing.T) {
	ctx := context.Background()
	s := base()
	if h, err := (Source{C: s}).Head(ctx); err != nil || h != 1000 {
		t.Fatalf("%d %v", h, err)
	}
	if _, err := (Source{C: s}).Block(ctx, 1001); !errors.Is(err, indexer.ErrNotFound) {
		t.Fatalf("beyond head: %v", err)
	}
	if _, err := (Source{C: s}).Block(ctx, 900); err == nil {
		t.Fatal("height outside window must fail")
	}
	// tiny slot number clamps the range at 0; custom lookback honoured
	s = base()
	s.res["getSlot"] = "10"
	s.res["getBlocks"] = "[10]"
	s.res["getBlockHeight"] = "5"
	s.res["getBlock"] = `{"blockhash":"h","previousBlockhash":"p","blockHeight":5,"signatures":[]}`
	b, err := Source{C: s, Lookback: 100}.Block(ctx, 5)
	if err != nil || b.Timestamp != 0 || b.TxCount != 0 || s.seen["getBlocks"][0].(float64) != 0 {
		t.Fatalf("%+v %v %v", b, err, s.seen["getBlocks"])
	}
}

func TestMidResolutionHeadMoveIsRetryable(t *testing.T) {
	s := base()
	s.res["getBlock"] = `{"blockhash":"h","previousBlockhash":"p","blockHeight":1000,"signatures":[]}`
	if _, err := (Source{C: s}).Block(context.Background(), 999); !errors.Is(err, indexer.ErrParentMismatch) {
		t.Fatalf("wrong height: %v", err)
	}
	s.res["getBlock"] = `{"blockhash":"h","previousBlockhash":"p","signatures":[]}`
	if _, err := (Source{C: s}).Block(context.Background(), 999); !errors.Is(err, indexer.ErrParentMismatch) {
		t.Fatalf("missing height: %v", err)
	}
}

func TestEachRPCFailurePropagates(t *testing.T) {
	for _, m := range []string{"getBlockHeight", "getSlot", "getBlocks", "getBlock"} {
		s := base()
		s.fail = map[string]bool{m: true}
		if _, err := (Source{C: s}).Block(context.Background(), 999); err == nil {
			t.Errorf("%s failure must surface", m)
		}
	}
}

// Compile-time proof that Source satisfies the indexer contract.
var _ indexer.Source = Source{}
