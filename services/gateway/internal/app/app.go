// Package app holds process wiring (config, lifecycle) so cmd/* stay one-liners
// and everything of substance is unit-testable.
package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/chainforge/gateway/internal/cache"
	"github.com/chainforge/gateway/internal/chainsim"
	"github.com/chainforge/gateway/internal/engineclient"
	"github.com/chainforge/gateway/internal/eventlog"
	"github.com/chainforge/gateway/internal/grpcx"
	"github.com/chainforge/gateway/internal/indexer"
	"github.com/chainforge/gateway/internal/intent"
	"github.com/chainforge/gateway/internal/metrics"
	"github.com/chainforge/gateway/internal/ratelimit"
	"github.com/chainforge/gateway/internal/relay"
	"github.com/chainforge/gateway/internal/server"
	"github.com/chainforge/gateway/internal/signerclient"
	"github.com/chainforge/gateway/internal/solana"
	"github.com/chainforge/gateway/internal/upstream"
	"github.com/chainforge/gateway/internal/webhook"
)

// Config is the gateway's 12-factor configuration.
type Config struct {
	Addr         string
	Upstreams    []string
	APIKeys      map[string]string
	SignerURL    string
	SignerSecret string
	RateRPS      float64
	RateBurst    int
	CORSOrigin   string
	Poll         time.Duration
	HooksPrivate bool

	Chain            string   // "evm" (default) or "solana": selects the indexer source and relay flavour
	EngineAddr       string   // unix:///path | host:port (mTLS) | h2c://host:port (dev); empty = engine disabled
	EngineCert       string   // client certificate for mTLS
	EngineKey        string   // its private key
	EngineCA         string   // CA that signed the engine's server certificate
	EngineServerName string   // expected server name in the engine certificate
	RelayURLs        []string // private relays; empty = /v1/broadcast disabled
}

func or(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// LoadConfig reads and validates configuration from getenv.
func LoadConfig(getenv func(string) string) (Config, error) {
	c := Config{Addr: or(getenv("GATEWAY_ADDR"), ":8080"), SignerURL: or(getenv("SIGNER_URL"), "http://localhost:8081"),
		SignerSecret: getenv("SIGNER_SHARED_SECRET"), CORSOrigin: getenv("CORS_ORIGIN"),
		HooksPrivate: getenv("WEBHOOK_ALLOW_PRIVATE") == "true", APIKeys: map[string]string{}}
	for _, u := range strings.Split(getenv("GATEWAY_UPSTREAMS"), ",") {
		if u = strings.TrimSpace(u); u != "" {
			c.Upstreams = append(c.Upstreams, u)
		}
	}
	if len(c.Upstreams) == 0 {
		return c, errors.New("GATEWAY_UPSTREAMS is required")
	}
	for _, kv := range strings.Split(getenv("GATEWAY_API_KEYS"), ",") {
		name, key, ok := strings.Cut(strings.TrimSpace(kv), "=")
		if ok && name != "" && len(key) >= 8 {
			c.APIKeys[key] = name
		}
	}
	if len(c.APIKeys) == 0 {
		return c, errors.New("GATEWAY_API_KEYS must contain name=key pairs (key >= 8 chars)")
	}
	if len(c.SignerSecret) < 16 {
		return c, errors.New("SIGNER_SHARED_SECRET must be >= 16 chars")
	}
	var err error
	if c.RateRPS, err = strconv.ParseFloat(or(getenv("RATE_LIMIT_RPS"), "50"), 64); err != nil || c.RateRPS <= 0 {
		return c, errors.New("RATE_LIMIT_RPS must be a positive number")
	}
	if c.RateBurst, err = strconv.Atoi(or(getenv("RATE_LIMIT_BURST"), "100")); err != nil || c.RateBurst < 1 {
		return c, errors.New("RATE_LIMIT_BURST must be a positive integer")
	}
	if c.Poll, err = time.ParseDuration(or(getenv("POLL_INTERVAL"), "1s")); err != nil || c.Poll <= 0 {
		return c, errors.New("POLL_INTERVAL must be a positive duration")
	}
	if c.Chain = or(getenv("GATEWAY_CHAIN"), "evm"); c.Chain != "evm" && c.Chain != "solana" {
		return c, errors.New(`GATEWAY_CHAIN must be "evm" or "solana"`)
	}
	c.EngineAddr, c.EngineCert, c.EngineKey, c.EngineCA = getenv("ENGINE_ADDR"), getenv("ENGINE_TLS_CERT"), getenv("ENGINE_TLS_KEY"), getenv("ENGINE_TLS_CA")
	c.EngineServerName = or(getenv("ENGINE_SERVER_NAME"), "engine")
	set := 0
	for _, v := range []string{c.EngineCert, c.EngineKey, c.EngineCA} {
		if v != "" {
			set++
		}
	}
	if set != 0 && set != 3 {
		return c, errors.New("ENGINE_TLS_CERT, ENGINE_TLS_KEY and ENGINE_TLS_CA must be set together")
	}
	for _, u := range strings.Split(getenv("RELAY_URLS"), ",") {
		if u = strings.TrimSpace(u); u != "" {
			c.RelayURLs = append(c.RelayURLs, u)
		}
	}
	return c, nil
}

// engineTLS builds the mTLS client configuration (nil when no material is configured).
func engineTLS(c Config) (*tls.Config, error) {
	if c.EngineCert == "" {
		return nil, nil
	}
	pair, err := tls.LoadX509KeyPair(c.EngineCert, c.EngineKey)
	if err != nil {
		return nil, fmt.Errorf("engine client certificate: %w", err)
	}
	ca, err := os.ReadFile(c.EngineCA)
	if err != nil {
		return nil, fmt.Errorf("engine CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, errors.New("engine CA: no certificates found")
	}
	return &tls.Config{Certificates: []tls.Certificate{pair}, RootCAs: pool, ServerName: c.EngineServerName, MinVersion: tls.VersionTLS13}, nil
}

// dialEngine connects lazily to the Rust simulation engine (nil client when disabled).
func dialEngine(c Config) (*grpcx.Client, error) {
	if c.EngineAddr == "" {
		return nil, nil
	}
	tc, err := engineTLS(c)
	if err != nil {
		return nil, err
	}
	return grpcx.Dial(c.EngineAddr, tc)
}

// Serve runs the gateway on ln until ctx is cancelled, then drains.
func Serve(ctx context.Context, ln net.Listener, cfg Config, log *slog.Logger) error {
	pool := upstream.NewPool(cfg.Upstreams, &http.Client{Timeout: 5 * time.Second}, 3, 10*time.Second, nil)
	evlog := eventlog.New(4)
	var src indexer.Source = indexer.RPCSource{C: pool}
	if cfg.Chain == "solana" {
		src = solana.Source{C: pool}
	}
	ix := indexer.New(src, evlog, 256)
	deps := server.Deps{}
	eng, err := dialEngine(cfg)
	if err != nil {
		return err
	}
	if eng != nil || cfg.Chain == "solana" {
		svc := &intent.Service{RPC: pool}
		if eng != nil {
			defer eng.Close()
			svc.Sim = engineclient.New(eng)
		}
		deps.Intents = svc
	}
	if len(cfg.RelayURLs) > 0 {
		rp := upstream.NewPool(cfg.RelayURLs, &http.Client{Timeout: 5 * time.Second}, 3, 10*time.Second, nil)
		send := relay.NewEVM(rp)
		if cfg.Chain == "solana" {
			send = relay.NewSolana(rp)
		}
		deps.Relays = map[string]server.Broadcaster{cfg.Chain: send}
	}
	hooks := webhook.New(webhook.Options{Client: &http.Client{Timeout: 5 * time.Second}, Attempts: 4,
		Backoff: 500 * time.Millisecond, AllowPrivate: cfg.HooksPrivate})
	deps.Caller, deps.Limiter = pool, ratelimit.New(cfg.RateRPS, cfg.RateBurst, nil)
	deps.Cache, deps.Signer = cache.New(4096, 10*time.Minute, nil), &signerclient.Client{BaseURL: cfg.SignerURL, Secret: cfg.SignerSecret, HTTP: &http.Client{Timeout: 5 * time.Second}}
	deps.Index, deps.Log, deps.Hooks, deps.Metrics, deps.Logger, deps.APIKeys, deps.AllowOrigin = ix, evlog, hooks, metrics.New(), log, cfg.APIKeys, cfg.CORSOrigin
	h := server.New(deps)
	bg, stop := context.WithCancel(ctx)
	defer stop()
	go ix.Run(bg, cfg.Poll, func(err error) { log.Warn("index sync", "err", err) })
	go hooks.Run(bg, evlog)
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second} // no WriteTimeout: SSE
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	log.Info("gateway listening", "addr", ln.Addr().String(), "upstreams", len(cfg.Upstreams))
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Close() // SSE streams never finish on their own; close then wait for handlers
	return srv.Shutdown(sctx)
}

// Main is the gateway entrypoint.
func Main(ctx context.Context, getenv func(string) string, log *slog.Logger) error {
	cfg, err := LoadConfig(getenv)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	return Serve(ctx, ln, cfg, log)
}

// SimMain runs the simulated node: mines a block every SIM_BLOCK_MS and forces a
// 2-deep reorg every SIM_REORG_EVERY blocks (0 disables).
func SimMain(ctx context.Context, getenv func(string) string, log *slog.Logger) error {
	id, err := strconv.ParseUint(or(getenv("SIM_CHAIN_ID"), "1337"), 10, 64)
	if err != nil {
		return errors.New("SIM_CHAIN_ID must be an integer")
	}
	ms, err := strconv.Atoi(or(getenv("SIM_BLOCK_MS"), "2000"))
	if err != nil || ms < 1 {
		return errors.New("SIM_BLOCK_MS must be a positive integer")
	}
	every, err := strconv.Atoi(or(getenv("SIM_REORG_EVERY"), "0"))
	if err != nil || every < 0 {
		return errors.New("SIM_REORG_EVERY must be a non-negative integer")
	}
	ln, err := net.Listen("tcp", or(getenv("SIM_ADDR"), ":8545"))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	node := chainsim.New(id)
	srv := &http.Server{Handler: node, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	log.Info("chainsim listening", "addr", ln.Addr().String(), "chain_id", id)
	t := time.NewTicker(time.Duration(ms) * time.Millisecond)
	defer t.Stop()
	for n := 1; ; n++ {
		select {
		case <-ctx.Done():
			return srv.Close()
		case <-t.C:
			if every > 0 && n%every == 0 {
				node.Reorg(2)
				log.Info("forced reorg", "depth", 2)
			} else {
				node.Mine()
			}
		}
	}
}

// Env adapts os.Getenv (kept here so cmd/* need no logic).
func Env() func(string) string { return os.Getenv }
