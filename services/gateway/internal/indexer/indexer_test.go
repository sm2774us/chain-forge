package indexer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainforge/gateway/internal/chainsim"
	"github.com/chainforge/gateway/internal/eventlog"
)

type simCaller struct{ n *chainsim.Node }

func (c simCaller) Call(_ context.Context, body []byte) ([]byte, error) {
	rec := httptest.NewRecorder()
	c.n.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body))))
	return rec.Body.Bytes(), nil
}

func events(l *eventlog.Log) (types []string) {
	for _, e := range l.Read(0, 0, 1000) {
		types = append(types, e.Type)
	}
	return
}

func TestFollowAndReorgAgainstSimNode(t *testing.T) {
	node := chainsim.New(1)
	for i := 0; i < 9; i++ {
		node.Mine()
	}
	log := eventlog.New(1)
	ix := New(RPCSource{simCaller{node}}, log, 4)
	if _, ok := ix.Head(); ok {
		t.Fatal("empty before sync")
	}
	if err := ix.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	bs := ix.Blocks(100)
	if len(bs) != 4 || bs[0].Number != 9 || bs[3].Number != 6 {
		t.Fatalf("window: %+v", bs)
	}
	old := bs[0].Hash
	node.Mine()
	_ = ix.Sync(context.Background())
	if h, _ := ix.Head(); h.Number != 10 || len(ix.Blocks(2)) != 2 || len(ix.Blocks(-1)) != 0 {
		t.Fatal("follow head")
	}
	node.Reorg(3) // replaces 8..10, mines 11
	if err := ix.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	h, _ := ix.Head()
	if h.Number != 11 || h.Hash == old {
		t.Fatalf("head after reorg: %+v", h)
	}
	reorged := 0
	for _, ty := range events(log) {
		if ty == "block.reorged" {
			reorged++
		}
	}
	if reorged != 3 {
		t.Fatalf("want 3 reorged events, got %d (%v)", reorged, events(log))
	}
}

type fakeSrc struct {
	head    uint64
	headErr error
	blocks  map[uint64]Block
	errAt   map[uint64]error
}

func (f *fakeSrc) Head(context.Context) (uint64, error) { return f.head, f.headErr }
func (f *fakeSrc) Block(_ context.Context, n uint64) (Block, error) {
	if e := f.errAt[n]; e != nil {
		return Block{}, e
	}
	if b, ok := f.blocks[n]; ok {
		return b, nil
	}
	return Block{}, ErrNotFound
}

func TestSyncErrorPaths(t *testing.T) {
	boom := errors.New("boom")
	src := &fakeSrc{head: 2, blocks: map[uint64]Block{
		0: {Number: 0, Hash: "a"}, 1: {Number: 1, Hash: "b", ParentHash: "a"}, 2: {Number: 2, Hash: "c", ParentHash: "WRONG"},
	}, errAt: map[uint64]error{}}
	ix := New(src, eventlog.New(1), 10)
	if err := ix.Sync(context.Background()); !errors.Is(err, ErrParentMismatch) {
		t.Fatalf("want parent mismatch, got %v", err)
	}
	if len(ix.Blocks(10)) != 2 {
		t.Fatal("partial progress must be kept")
	}
	src.errAt[1] = boom // tip verification fails with non-notfound error
	if err := ix.Sync(context.Background()); !errors.Is(err, boom) {
		t.Fatal("verify error must surface")
	}
	src.errAt = map[uint64]error{2: boom}
	src.blocks[1] = Block{Number: 1, Hash: "b", ParentHash: "a"}
	if err := ix.Sync(context.Background()); !errors.Is(err, boom) {
		t.Fatal("fetch error must surface")
	}
	src.headErr = boom
	if err := ix.Sync(context.Background()); !errors.Is(err, boom) {
		t.Fatal("head error must surface")
	}
}

func TestRunLoop(t *testing.T) {
	src := &fakeSrc{headErr: errors.New("down")}
	ix := New(src, eventlog.New(1), 0)
	var errs atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { ix.Run(ctx, time.Millisecond, func(error) { errs.Add(1) }); close(done) }()
	for errs.Load() < 2 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
}

type badCaller struct {
	out string
	err error
}

func (b badCaller) Call(context.Context, []byte) ([]byte, error) { return []byte(b.out), b.err }

func TestRPCSourceFailures(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		out string
		err error
	}{
		{"", errors.New("net")}, {"not json", nil}, {`{"error":{"message":"rpc boom"}}`, nil},
		{`{"result":"nope"}`, nil}, {`{"result":5}`, nil},
	}
	for _, c := range cases {
		if _, err := (RPCSource{badCaller{c.out, c.err}}).Head(ctx); err == nil {
			t.Errorf("Head(%q) should fail", c.out)
		}
	}
	if _, err := (RPCSource{badCaller{`{"result":null}`, nil}}).Block(ctx, 1); !errors.Is(err, ErrNotFound) {
		t.Fatal("null → ErrNotFound")
	}
	if _, err := (RPCSource{badCaller{`{"result":{"number":"1","timestamp":"0x1"}}`, nil}}).Block(ctx, 1); err == nil {
		t.Fatal("bad number")
	}
	if _, err := (RPCSource{badCaller{`{"result":{"number":"0x1","timestamp":"1"}}`, nil}}).Block(ctx, 1); err == nil {
		t.Fatal("bad timestamp")
	}
}
