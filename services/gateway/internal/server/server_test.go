package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chainforge/gateway/internal/cache"
	"github.com/chainforge/gateway/internal/chainsim"
	"github.com/chainforge/gateway/internal/eventlog"
	"github.com/chainforge/gateway/internal/indexer"
	"github.com/chainforge/gateway/internal/metrics"
	"github.com/chainforge/gateway/internal/ratelimit"
	"github.com/chainforge/gateway/internal/signerclient"
	"github.com/chainforge/gateway/internal/upstream"
	"github.com/chainforge/gateway/internal/webhook"
)

type simCaller struct {
	n      *chainsim.Node
	err    error
	raw    string
	health []upstream.Status
	calls  int
}

func (c *simCaller) Call(_ context.Context, body []byte) ([]byte, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	if c.raw != "" {
		return []byte(c.raw), nil
	}
	rec := httptest.NewRecorder()
	c.n.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body))))
	return rec.Body.Bytes(), nil
}
func (c *simCaller) Health() []upstream.Status { return c.health }

type fakeSigner struct {
	out   signerclient.SignResponse
	err   error
	txOut signerclient.TxResponse
	txErr error
	txReq signerclient.TxRequest
}

func (f *fakeSigner) SignTx(_ context.Context, r signerclient.TxRequest) (signerclient.TxResponse, error) {
	f.txReq = r
	return f.txOut, f.txErr
}

func (f *fakeSigner) Sign(context.Context, signerclient.SignRequest) (signerclient.SignResponse, error) {
	return f.out, f.err
}

type rig struct {
	h      http.Handler
	caller *simCaller
	signer *fakeSigner
	log    *eventlog.Log
	ix     *indexer.Indexer
	node   *chainsim.Node
	now    *time.Time
}

func newRig(t *testing.T) *rig { return newRigWith(t, nil) }

func newRigWith(t *testing.T, mod func(*Deps)) *rig {
	t.Helper()
	node := chainsim.New(1)
	node.Mine()
	c := &simCaller{n: node, health: []upstream.Status{{URL: "a", State: "closed"}}}
	lg := eventlog.New(2)
	ix := indexer.New(indexer.RPCSource{C: c}, lg, 10)
	now := time.Unix(0, 0)
	sg := &fakeSigner{out: signerclient.SignResponse{Address: "0xa", Signature: "0xs"}}
	d := Deps{Caller: c, Limiter: ratelimit.New(1, 3, func() time.Time { return now }), Cache: cache.New(8, time.Minute, nil),
		Signer: sg, Index: ix, Log: lg, Hooks: webhook.New(webhook.Options{}), Metrics: metrics.New(),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), APIKeys: map[string]string{"k1": "alice"}, AllowOrigin: "http://web"}
	if mod != nil {
		mod(&d)
	}
	return &rig{New(d), c, sg, lg, ix, node, &now}
}

func (r *rig) do(method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if len(hdr) == 0 {
		req.Header.Set("X-API-Key", "k1")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, req)
	return rec
}

func rpc(method string, id int) string {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": []any{}, "id": id})
	return string(b)
}

func TestAuthAndHealth(t *testing.T) {
	r := newRig(t)
	if r.do("POST", "/rpc", rpc("eth_chainId", 1), "X-API-Key", "bad").Code != 401 {
		t.Fatal("bad key")
	}
	if r.do("POST", "/rpc", rpc("eth_chainId", 1), "X-Nothing", "x").Code != 401 {
		t.Fatal("no key")
	}
	if r.do("GET", "/v1/blocks?api_key=k1", "", "X-Other", "1").Code != 200 {
		t.Fatal("query-param key")
	}
	if rec := r.do("GET", "/healthz", ""); rec.Code != 200 || rec.Header().Get("X-Request-Id") == "" {
		t.Fatal("healthz")
	}
	rec := r.do("OPTIONS", "/rpc", "")
	if rec.Code != 204 || rec.Header().Get("Access-Control-Allow-Origin") != "http://web" {
		t.Fatal("cors preflight")
	}
}

func TestRPCProxyCacheAndRateLimit(t *testing.T) {
	r := newRig(t)
	var out struct{ Result string }
	_ = json.Unmarshal(r.do("POST", "/rpc", rpc("eth_chainId", 7)).Body.Bytes(), &out)
	if out.Result != "0x1" {
		t.Fatal("proxy", out)
	}
	before := r.caller.calls
	rec := r.do("POST", "/rpc", rpc("eth_chainId", 8))
	if r.caller.calls != before || !strings.Contains(rec.Body.String(), `"id":8`) {
		t.Fatal("second call must be a cache hit that keeps the caller's id")
	}
	_ = json.Unmarshal(r.do("POST", "/rpc", rpc("eth_blockNumber", 9)).Body.Bytes(), &out)
	// bucket burst is 3 → 4th call within the same instant is limited
	if !strings.Contains(r.do("POST", "/rpc", rpc("eth_blockNumber", 10)).Body.String(), "rate limited") {
		t.Fatal("expected rate limit")
	}
}

func TestRPCErrorsAndBatch(t *testing.T) {
	r := newRig(t)
	*r.now = r.now.Add(time.Hour)
	if !strings.Contains(r.do("POST", "/rpc", rpc("eth_nope", 1)).Body.String(), "-32601") {
		t.Fatal("upstream JSON-RPC error passes through")
	}
	r.caller.err = errors.New("down")
	if !strings.Contains(r.do("POST", "/rpc", rpc("eth_blockNumber", 1)).Body.String(), "-32002") {
		t.Fatal("upstream down")
	}
	r.caller.err, r.caller.raw = nil, "garbage"
	if !strings.Contains(r.do("POST", "/rpc", rpc("eth_blockNumber", 1)).Body.String(), "malformed") {
		t.Fatal("garbage upstream")
	}
	*r.now = r.now.Add(time.Hour)
	r.caller.raw = ""
	r.do("POST", "/rpc", `{"jsonrpc":"2.0","method":"eth_chainId"}`) // no id: proxy + cache fill
	if !strings.Contains(r.do("POST", "/rpc", `{"jsonrpc":"2.0","method":"eth_chainId"}`).Body.String(), `"id":null`) {
		t.Fatal("cache hit for id-less request")
	}
	*r.now = r.now.Add(time.Hour)
	r.caller.raw = `{"jsonrpc":"2.0","result":null,"id":1}`
	r.do("POST", "/rpc", `{"jsonrpc":"2.0","method":"eth_getBlockByHash","params":["0x1",false],"id":1}`)
	// null results are not cached → second call hits upstream again
	n := r.caller.calls
	r.do("POST", "/rpc", `{"jsonrpc":"2.0","method":"eth_getBlockByHash","params":["0x1",false],"id":1}`)
	if r.caller.calls != n+1 {
		t.Fatal("null must not be cached")
	}
	r.caller.raw = ""
	batch := `[` + rpc("eth_chainId", 1) + `,` + rpc("weird\u0000method", 2) + `]`
	if rec := r.do("POST", "/rpc", batch); !strings.HasPrefix(rec.Body.String(), "[") {
		t.Fatal("batch")
	}
	if methodLabel("eth_x") != "eth_x" || methodLabel("evil") != "other" || methodLabel("eth_"+strings.Repeat("a", 50)) != "other" {
		t.Fatal("label cardinality guard")
	}
}

func TestBlocksStatusReadyMetrics(t *testing.T) {
	r := newRig(t)
	if r.do("GET", "/readyz", "").Code != 503 {
		t.Fatal("not ready before first sync")
	}
	if err := r.ix.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.do("GET", "/readyz", "").Code != 200 {
		t.Fatal("ready after sync")
	}
	r.caller.health = []upstream.Status{{State: "open"}}
	if r.do("GET", "/readyz", "").Code != 503 {
		t.Fatal("all upstreams open → not ready")
	}
	if !strings.Contains(r.do("GET", "/v1/blocks?limit=1", "").Body.String(), `"blocks"`) {
		t.Fatal("blocks")
	}
	for _, q := range []string{"limit=0", "limit=x", "limit=501"} {
		if r.do("GET", "/v1/blocks?"+q, "").Code != 400 {
			t.Fatal(q)
		}
	}
	if !strings.Contains(r.do("GET", "/v1/status", "").Body.String(), "upstreams") {
		t.Fatal("status")
	}
	m := r.do("GET", "/metrics", "").Body.String()
	if !strings.Contains(m, "gateway_http_requests_total") || !strings.Contains(m, `route="GET /v1/blocks"`) {
		t.Fatal("metrics:\n" + m)
	}
	if !strings.Contains(r.do("GET", "/nope", "").Body.String(), "") || !strings.Contains(r.do("GET", "/metrics", "").Body.String(), "unmatched") {
		t.Fatal("unmatched route label")
	}
}

func TestSign(t *testing.T) {
	r := newRig(t)
	ok := `{"key_id":"alice","message":"hi"}`
	if rec := r.do("POST", "/v1/sign", ok); rec.Code != 200 || !strings.Contains(rec.Body.String(), "0xs") {
		t.Fatal("sign ok")
	}
	for _, b := range []string{`{`, `{"key_id":"","message":"x"}`, `{"key_id":"a","message":"x","extra":1}`} {
		if r.do("POST", "/v1/sign", b).Code != 400 {
			t.Fatal(b)
		}
	}
	r.signer.err = &signerclient.APIError{Status: 403, Message: "unknown key"}
	if rec := r.do("POST", "/v1/sign", ok); rec.Code != 403 || !strings.Contains(rec.Body.String(), "unknown key") {
		t.Fatal("api error passthrough")
	}
	r.signer.err = errors.New("net")
	if r.do("POST", "/v1/sign", ok).Code != 502 {
		t.Fatal("transport error → 502")
	}
}

func TestWebhooksCRUD(t *testing.T) {
	r := newRig(t)
	if r.do("POST", "/v1/webhooks", `{"url":"https://a.example/x","secret":"short"}`).Code != 400 {
		t.Fatal("short secret")
	}
	if r.do("POST", "/v1/webhooks", `nope`).Code != 400 {
		t.Fatal("bad json")
	}
	if r.do("POST", "/v1/webhooks", `{"url":"http://127.0.0.1/x","secret":"0123456789abcdef"}`).Code != 422 {
		t.Fatal("ssrf guard")
	}
	rec := r.do("POST", "/v1/webhooks", `{"url":"https://a.example/x","secret":"0123456789abcdef"}`)
	if rec.Code != 201 || strings.Contains(rec.Body.String(), "0123456789abcdef") {
		t.Fatal("create must not echo the secret")
	}
	if !strings.Contains(r.do("GET", "/v1/webhooks", "").Body.String(), "wh_0001") {
		t.Fatal("list")
	}
	if r.do("DELETE", "/v1/webhooks/wh_0001", "").Code != 204 || r.do("DELETE", "/v1/webhooks/wh_0001", "").Code != 404 {
		t.Fatal("delete")
	}
}

func TestStreamSSE(t *testing.T) {
	r := newRig(t)
	ts := httptest.NewServer(r.h)
	defer ts.Close()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/v1/stream?api_key=k1", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Scan() // ": connected"
	sc.Scan() // blank separator
	go func() {
		time.Sleep(20 * time.Millisecond)
		_, _ = r.log.Append("chain", "block.new", map[string]int{"n": 1})
	}()
	var lines []string
	for sc.Scan() && len(lines) < 3 {
		lines = append(lines, sc.Text())
	}
	cancel()
	if len(lines) < 3 || !strings.HasPrefix(lines[1], "event: block.new") {
		t.Fatalf("sse frame: %v", lines)
	}
}

func TestStreamEvictedAndNoFlusher(t *testing.T) {
	r := newRig(t)
	ts := httptest.NewServer(r.h)
	defer ts.Close()
	resp, _ := http.Get(ts.URL + "/v1/stream?api_key=k1")
	go func() {
		time.Sleep(20 * time.Millisecond)
		for i := 0; i < 600; i++ {
			_, _ = r.log.Append("c", "t", i)
		}
	}()
	_, _ = io.Copy(io.Discard, resp.Body) // returns once the server closes the stream
	resp.Body.Close()

	// A ResponseWriter without Flush support → 500.
	s := &server{Deps: Deps{Log: r.log}}
	rec := &noFlush{h: http.Header{}}
	s.stream(rec, httptest.NewRequest("GET", "/", nil), "x")
	if rec.code != 500 {
		t.Fatal("expected 500")
	}
}

type noFlush struct {
	h    http.Header
	code int
}

func (n *noFlush) Header() http.Header         { return n.h }
func (n *noFlush) Write(b []byte) (int, error) { return len(b), nil }
func (n *noFlush) WriteHeader(c int)           { n.code = c }

func TestRecoverer(t *testing.T) {
	s := &server{Deps: Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	h := s.recoverer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 500 {
		t.Fatal("panic must become 500")
	}
}

func TestStatusWriterFlush(t *testing.T) {
	sw := &statusWriter{ResponseWriter: httptest.NewRecorder()}
	sw.Flush()
	(&statusWriter{ResponseWriter: &noFlush{h: http.Header{}}}).Flush()
	rec := httptest.NewRecorder()
	(&statusWriter{ResponseWriter: rec}).WriteHeader(202)
	if rec.Code != 202 {
		t.Fatal()
	}
}
