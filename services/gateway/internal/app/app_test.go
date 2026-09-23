package app

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func valid() map[string]string {
	return map[string]string{"GATEWAY_UPSTREAMS": " http://a , ,http://b", "GATEWAY_API_KEYS": "alice=abcdefgh, bad=x, =zzzzzzzz",
		"SIGNER_SHARED_SECRET": "0123456789abcdef"}
}

func TestLoadConfig(t *testing.T) {
	c, err := LoadConfig(env(valid()))
	if err != nil || len(c.Upstreams) != 2 || c.APIKeys["abcdefgh"] != "alice" || len(c.APIKeys) != 1 || c.Addr != ":8080" || c.Poll != time.Second {
		t.Fatalf("%+v %v", c, err)
	}
	mut := func(k, v string) map[string]string { m := valid(); m[k] = v; return m }
	bad := []map[string]string{
		mut("GATEWAY_UPSTREAMS", ""), mut("GATEWAY_API_KEYS", "x"), mut("SIGNER_SHARED_SECRET", "short"),
		mut("RATE_LIMIT_RPS", "0"), mut("RATE_LIMIT_BURST", "z"), mut("POLL_INTERVAL", "-1s"),
	}
	for i, m := range bad {
		if _, err := LoadConfig(env(m)); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
	m := valid()
	m["WEBHOOK_ALLOW_PRIVATE"] = "true"
	if c, _ := LoadConfig(env(m)); !c.HooksPrivate {
		t.Fatal("flag")
	}
}

func listen(t *testing.T) net.Listener {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

func TestSimAndGatewayEndToEnd(t *testing.T) {
	sim := listen(t)
	simAddr := sim.Addr().String()
	_ = sim.Close()
	ctx, cancel := context.WithCancel(context.Background())
	simDone := make(chan error, 1)
	go func() {
		simDone <- SimMain(ctx, env(map[string]string{"SIM_ADDR": simAddr, "SIM_BLOCK_MS": "5", "SIM_REORG_EVERY": "3"}), quiet)
	}()
	cfg, _ := LoadConfig(env(map[string]string{"GATEWAY_UPSTREAMS": "http://" + simAddr, "GATEWAY_API_KEYS": "alice=abcdefgh",
		"SIGNER_SHARED_SECRET": "0123456789abcdef", "POLL_INTERVAL": "5ms"}))
	gw := listen(t)
	gwDone := make(chan error, 1)
	go func() { gwDone <- Serve(ctx, gw, cfg, quiet) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get("http://" + gw.Addr().String() + "/readyz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("gateway never became ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	req, _ := http.NewRequest("POST", "http://"+gw.Addr().String()+"/rpc", strings.NewReader(`{"jsonrpc":"2.0","method":"eth_chainId","id":1}`))
	req.Header.Set("X-API-Key", "abcdefgh")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), "0x539") {
		t.Fatal(string(b))
	}
	time.Sleep(100 * time.Millisecond) // let the sim mine and force reorgs
	cancel()
	if err := <-gwDone; err != nil {
		t.Fatal(err)
	}
	if err := <-simDone; err != nil {
		t.Fatal(err)
	}
}

func TestServeReturnsListenerError(t *testing.T) {
	ln := listen(t)
	_ = ln.Close() // Serve on a closed listener fails immediately
	cfg, _ := LoadConfig(env(valid()))
	if err := Serve(context.Background(), ln, cfg, quiet); err == nil {
		t.Fatal("want error")
	}
}

func TestMainPaths(t *testing.T) {
	if err := Main(context.Background(), env(nil), quiet); err == nil {
		t.Fatal("config error expected")
	}
	m := valid()
	m["GATEWAY_ADDR"] = "256.256.256.256:1"
	if err := Main(context.Background(), env(m), quiet); err == nil || !strings.Contains(err.Error(), "listen") {
		t.Fatal("listen error expected", err)
	}
	m["GATEWAY_ADDR"] = "127.0.0.1:0"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Main(ctx, env(m), quiet); err != nil {
		t.Fatal(err)
	}
}

func TestSimMainValidation(t *testing.T) {
	for _, m := range []map[string]string{{"SIM_CHAIN_ID": "x"}, {"SIM_BLOCK_MS": "0"}, {"SIM_REORG_EVERY": "-1"}, {"SIM_ADDR": "256.256.256.256:1"}} {
		if err := SimMain(context.Background(), env(m), quiet); err == nil {
			t.Errorf("%v accepted", m)
		}
	}
}

func TestEnv(t *testing.T) {
	t.Setenv("CF_TEST_VAR", "yes")
	if Env()("CF_TEST_VAR") != "yes" || or("", "d") != "d" || or("v", "d") != "v" {
		t.Fatal()
	}
}

func TestServeLogsSyncErrors(t *testing.T) {
	dead := listen(t)
	addr := dead.Addr().String()
	_ = dead.Close()
	cfg, _ := LoadConfig(env(map[string]string{"GATEWAY_UPSTREAMS": "http://" + addr, "GATEWAY_API_KEYS": "alice=abcdefgh",
		"SIGNER_SHARED_SECRET": "0123456789abcdef", "POLL_INTERVAL": "5ms"}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, listen(t), cfg, quiet) }()
	time.Sleep(60 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
