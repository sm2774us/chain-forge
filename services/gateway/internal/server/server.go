// Package server wires the gateway's HTTP surface: authenticated, rate-limited
// JSON-RPC proxying with caching and failover, an indexed block API, SSE
// streams, webhooks, custody-signing passthrough, health and Prometheus metrics.
package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/chainforge/gateway/internal/cache"
	"github.com/chainforge/gateway/internal/eventlog"
	"github.com/chainforge/gateway/internal/indexer"
	"github.com/chainforge/gateway/internal/intent"
	"github.com/chainforge/gateway/internal/jsonrpc"
	"github.com/chainforge/gateway/internal/metrics"
	"github.com/chainforge/gateway/internal/ratelimit"
	"github.com/chainforge/gateway/internal/signerclient"
	"github.com/chainforge/gateway/internal/upstream"
	"github.com/chainforge/gateway/internal/webhook"
)

// Caller proxies raw JSON-RPC bodies to a node (upstream.Pool).
type Caller interface {
	Call(ctx context.Context, body []byte) ([]byte, error)
	Health() []upstream.Status
}

// Signer is the custody boundary (signerclient.Client).
type Signer interface {
	Sign(ctx context.Context, r signerclient.SignRequest) (signerclient.SignResponse, error)
	SignTx(ctx context.Context, r signerclient.TxRequest) (signerclient.TxResponse, error)
}

// Intents prepares simulated, priced transactions (intent.Service).
type Intents interface {
	PrepareEVM(ctx context.Context, r intent.EVMRequest) (intent.Prepared, error)
	SimulateSolana(ctx context.Context, txB64 string) (intent.SolanaSim, error)
}

// Broadcaster submits a raw signed transaction through a private relay (relay.Sender).
type Broadcaster interface {
	Send(ctx context.Context, raw string) (string, error)
}

// Deps are the server's collaborators.
type Deps struct {
	Caller      Caller
	Limiter     *ratelimit.Limiter
	Cache       *cache.Cache
	Signer      Signer
	Intents     Intents                // nil = engine not configured (routes answer 501)
	Relays      map[string]Broadcaster // "evm", "solana"
	Index       *indexer.Indexer
	Log         *eventlog.Log
	Hooks       *webhook.Dispatcher
	Metrics     *metrics.Registry
	Logger      *slog.Logger
	APIKeys     map[string]string // key → client name
	AllowOrigin string
}

type server struct {
	Deps
	keys map[[32]byte]string
}

// cacheable methods are immutable per key (hash-addressed) or chain constants.
var cacheable = map[string]bool{"eth_chainId": true, "eth_getBlockByHash": true}

// New returns the root handler.
func New(d Deps) http.Handler {
	s := &server{Deps: d, keys: map[[32]byte]string{}}
	for k, name := range d.APIKeys {
		s.keys[sha256.Sum256([]byte(k))] = name
	}
	d.Metrics.Describe("gateway_http_requests_total", "HTTP requests by route and status")
	d.Metrics.Describe("gateway_rpc_calls_total", "JSON-RPC calls by method and outcome")
	mux := http.NewServeMux()
	handle := func(pattern string, h http.HandlerFunc) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if sw, ok := w.(*statusWriter); ok {
				sw.route = pattern
			}
			h(w, r)
		})
	}
	handle("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	handle("GET /readyz", s.ready)
	handle("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		s.Metrics.Write(w)
	})
	handle("POST /rpc", s.auth(s.rpc))
	handle("GET /v1/status", s.auth(s.status))
	handle("GET /v1/blocks", s.auth(s.blocks))
	handle("GET /v1/stream", s.auth(s.stream))
	handle("POST /v1/sign", s.auth(s.sign))
	handle("POST /v1/simulate", s.auth(s.simulate))
	handle("POST /v1/intent", s.auth(s.intentRoute))
	handle("POST /v1/broadcast", s.auth(s.broadcast))
	handle("GET /v1/webhooks", s.auth(s.listHooks))
	handle("POST /v1/webhooks", s.auth(s.addHook))
	handle("DELETE /v1/webhooks/{id}", s.auth(s.delHook))
	return s.recoverer(s.cors(s.observe(mux)))
}

// ---- middleware -----------------------------------------------------------

type statusWriter struct {
	http.ResponseWriter
	code  int
	route string
}

func (w *statusWriter) WriteHeader(c int) { w.code = c; w.ResponseWriter.WriteHeader(c) }
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *server) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := make([]byte, 8)
		_, _ = rand.Read(id)
		rid := hex.EncodeToString(id)
		w.Header().Set("X-Request-Id", rid)
		sw := &statusWriter{ResponseWriter: w, code: 200}
		start := time.Now()
		next.ServeHTTP(sw, r)
		route := sw.route
		if route == "" {
			route = "unmatched"
		}
		s.Metrics.Inc("gateway_http_requests_total", map[string]string{"route": route, "status": strconv.Itoa(sw.code)})
		s.Logger.Info("http", "id", rid, "route", route, "status", sw.code, "dur_ms", time.Since(start).Milliseconds())
	})
}

func (s *server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.Logger.Error("panic", "value", fmt.Sprint(v), "stack", string(debug.Stack()))
				writeJSON(w, 500, map[string]string{"error": "internal error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.AllowOrigin != "" {
			w.Header().Set("Access-Control-Allow-Origin", s.AllowOrigin)
			w.Header().Set("Access-Control-Allow-Headers", "content-type, x-api-key")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type clientHandler func(w http.ResponseWriter, r *http.Request, client string)

func (s *server) auth(next clientHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-Key")
		if key == "" {
			key = r.URL.Query().Get("api_key") // EventSource cannot set headers
		}
		name, ok := s.keys[sha256.Sum256([]byte(key))]
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid api key"})
			return
		}
		next(w, r, name)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// ---- handlers -------------------------------------------------------------

func (s *server) rpc(w http.ResponseWriter, r *http.Request, client string) {
	jsonrpc.Serve(w, r, func(ctx context.Context, q jsonrpc.Request) jsonrpc.Response {
		resp, outcome := s.proxy(ctx, q, client)
		s.Metrics.Inc("gateway_rpc_calls_total", map[string]string{"method": methodLabel(q.Method), "outcome": outcome})
		return resp
	})
}

// methodLabel bounds metric cardinality against attacker-chosen method names.
func methodLabel(m string) string {
	if len(m) > 4 && m[:4] == "eth_" && len(m) < 40 {
		return m
	}
	return "other"
}

func (s *server) proxy(ctx context.Context, q jsonrpc.Request, client string) (jsonrpc.Response, string) {
	if ok, wait := s.Limiter.Allow(client); !ok {
		return jsonrpc.Fail(q.ID, jsonrpc.CodeRateLimited, fmt.Sprintf("rate limited; retry in %dms", wait.Milliseconds())), "ratelimited"
	}
	key := q.Method + string(q.Params)
	if cacheable[q.Method] {
		if hit, ok := s.Cache.Get(key); ok {
			return jsonrpc.Response{JSONRPC: "2.0", Result: hit, ID: idOrNull(q.ID)}, "cached"
		}
	}
	body, _ := json.Marshal(q)
	raw, err := s.Caller.Call(ctx, body)
	if err != nil {
		return jsonrpc.Fail(q.ID, jsonrpc.CodeUpstream, "upstream unavailable"), "upstream_error"
	}
	var resp jsonrpc.Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return jsonrpc.Fail(q.ID, jsonrpc.CodeUpstream, "malformed upstream response"), "upstream_error"
	}
	resp.ID = idOrNull(q.ID)
	if resp.Error != nil {
		return resp, "error"
	}
	if cacheable[q.Method] && string(resp.Result) != "null" {
		s.Cache.Set(key, resp.Result)
	}
	return resp, "ok"
}

func idOrNull(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}

func (s *server) ready(w http.ResponseWriter, _ *http.Request) {
	healthy := 0
	for _, e := range s.Caller.Health() {
		if e.State != "open" {
			healthy++
		}
	}
	if _, ok := s.Index.Head(); !ok || healthy == 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ready": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ready": true})
}

func (s *server) status(w http.ResponseWriter, _ *http.Request, _ string) {
	head, _ := s.Index.Head()
	writeJSON(w, 200, map[string]any{
		"head": head, "upstreams": s.Caller.Health(), "cache_entries": s.Cache.Len(),
		"rpc_ok":      s.Metrics.Value("gateway_rpc_calls_total", map[string]string{"method": "eth_blockNumber", "outcome": "ok"}),
		"webhook_cnt": len(s.Hooks.List()),
	})
}

func (s *server) blocks(w http.ResponseWriter, r *http.Request, _ string) {
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 500 {
			writeJSON(w, 400, map[string]string{"error": "limit must be 1..500"})
			return
		}
		limit = n
	}
	writeJSON(w, 200, map[string]any{"blocks": s.Index.Blocks(limit)})
}

func (s *server) stream(w http.ResponseWriter, r *http.Request, _ string) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, 500, map[string]string{"error": "streaming unsupported"})
		return
	}
	ch, cancel := s.Log.Subscribe(256)
	defer cancel()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	fmt.Fprint(w, ": connected\n\n")
	fl.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, open := <-ch:
			if !open {
				return
			}
			b, _ := json.Marshal(ev)
			fmt.Fprintf(w, "id: %d-%d\nevent: %s\ndata: %s\n\n", ev.Partition, ev.Offset, ev.Type, b)
			fl.Flush()
		}
	}
}

func (s *server) sign(w http.ResponseWriter, r *http.Request, _ string) {
	var req signerclient.SignRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || req.KeyID == "" || req.Message == "" {
		writeJSON(w, 400, map[string]string{"error": "body must be {key_id, message}"})
		return
	}
	out, err := s.Signer.Sign(r.Context(), req)
	if err != nil {
		signerFail(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (s *server) listHooks(w http.ResponseWriter, _ *http.Request, _ string) {
	writeJSON(w, 200, map[string]any{"webhooks": s.Hooks.List()})
}

func (s *server) addHook(w http.ResponseWriter, r *http.Request, _ string) {
	var in struct{ URL, Secret string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil || len(in.Secret) < 16 {
		writeJSON(w, 400, map[string]string{"error": "body must be {url, secret (>=16 chars)}"})
		return
	}
	reg, err := s.Hooks.Register(in.URL, in.Secret)
	if err != nil {
		writeJSON(w, 422, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 201, reg)
}

func (s *server) delHook(w http.ResponseWriter, r *http.Request, _ string) {
	if !s.Hooks.Remove(r.PathValue("id")) {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
