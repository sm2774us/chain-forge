package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadConfigEngineRelayAndChain(t *testing.T) {
	m := valid()
	m["GATEWAY_CHAIN"] = "solana"
	m["ENGINE_ADDR"] = "unix:///run/engine.sock"
	m["RELAY_URLS"] = " http://r1 , ,http://r2"
	c, err := LoadConfig(env(m))
	if err != nil || c.Chain != "solana" || c.EngineAddr != "unix:///run/engine.sock" || len(c.RelayURLs) != 2 || c.EngineServerName != "engine" {
		t.Fatalf("%+v %v", c, err)
	}
	c, _ = LoadConfig(env(valid()))
	if c.Chain != "evm" || c.EngineAddr != "" || len(c.RelayURLs) != 0 {
		t.Fatalf("defaults: %+v", c)
	}
	for _, kv := range [][2]string{{"GATEWAY_CHAIN", "bitcoin"}, {"ENGINE_TLS_CERT", "c.pem"}, {"ENGINE_TLS_CA", "ca.pem"}} {
		m := valid()
		m[kv[0]] = kv[1]
		if _, err := LoadConfig(env(m)); err == nil {
			t.Errorf("%v accepted", kv)
		}
	}
	m = valid()
	m["ENGINE_TLS_CERT"], m["ENGINE_TLS_KEY"], m["ENGINE_TLS_CA"], m["ENGINE_SERVER_NAME"] = "c", "k", "ca", "eng.internal"
	if c, err := LoadConfig(env(m)); err != nil || c.EngineServerName != "eng.internal" {
		t.Fatalf("full tls: %+v %v", c, err)
	}
}

func writePEM(t *testing.T, path, typ string, der []byte) {
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}

// clientPKI writes ca.pem, client.pem and client.key into dir.
func clientPKI(t *testing.T, dir string) (cert, key, ca string) {
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caT := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ca"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, _ := x509.CreateCertificate(rand.Reader, caT, caT, &caKey.PublicKey, caKey)
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	lt := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "gateway"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, _ := x509.CreateCertificate(rand.Reader, lt, caT, &k.PublicKey, caKey)
	kb, _ := x509.MarshalECPrivateKey(k)
	cert, key, ca = filepath.Join(dir, "client.pem"), filepath.Join(dir, "client.key"), filepath.Join(dir, "ca.pem")
	writePEM(t, cert, "CERTIFICATE", der)
	writePEM(t, key, "EC PRIVATE KEY", kb)
	writePEM(t, ca, "CERTIFICATE", caDER)
	return
}

func TestEngineTLSAndDial(t *testing.T) {
	dir := t.TempDir()
	cert, key, ca := clientPKI(t, dir)
	if tc, err := engineTLS(Config{}); tc != nil || err != nil {
		t.Fatalf("no material: %v %v", tc, err)
	}
	tc, err := engineTLS(Config{EngineCert: cert, EngineKey: key, EngineCA: ca, EngineServerName: "engine"})
	if err != nil || tc.ServerName != "engine" || len(tc.Certificates) != 1 || tc.MinVersion == 0 {
		t.Fatalf("%+v %v", tc, err)
	}
	junk := filepath.Join(dir, "junk.pem")
	os.WriteFile(junk, []byte("not pem"), 0o600)
	for name, c := range map[string]Config{
		"bad pair": {EngineCert: junk, EngineKey: junk, EngineCA: ca},
		"no ca":    {EngineCert: cert, EngineKey: key, EngineCA: filepath.Join(dir, "missing")},
		"junk ca":  {EngineCert: cert, EngineKey: key, EngineCA: junk},
	} {
		if _, err := engineTLS(c); err == nil {
			t.Errorf("%s accepted", name)
		}
	}

	if cl, err := dialEngine(Config{}); cl != nil || err != nil {
		t.Fatalf("disabled: %v %v", cl, err)
	}
	for _, addr := range []string{"unix://" + filepath.Join(dir, "e.sock"), "h2c://127.0.0.1:1"} {
		if cl, err := dialEngine(Config{EngineAddr: addr}); cl == nil || err != nil {
			t.Fatalf("%s: %v", addr, err)
		}
	}
	if cl, err := dialEngine(Config{EngineAddr: "engine:50051", EngineCert: cert, EngineKey: key, EngineCA: ca}); cl == nil || err != nil {
		t.Fatalf("mtls: %v", err)
	}
	if _, err := dialEngine(Config{EngineAddr: "engine:50051", EngineCert: junk, EngineKey: junk, EngineCA: ca}); err == nil {
		t.Fatal("bad tls must fail")
	}
	if _, err := dialEngine(Config{EngineAddr: "engine:50051"}); err == nil {
		t.Fatal("TCP without mTLS must be refused")
	}
}

// fakeEngine speaks the gRPC wire with the shared golden response.
func fakeEngine(t *testing.T) string {
	raw, err := os.ReadFile("../../../../contracts/wire_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var g struct {
		Resp string `json:"simulate_response_hex"`
	}
	json.Unmarshal(raw, &g)
	msg, _ := hex.DecodeString(g.Resp)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Trailer", "Grpc-Status")
		f := make([]byte, 5+len(msg))
		binary.BigEndian.PutUint32(f[1:5], uint32(len(msg)))
		copy(f[5:], msg)
		w.Write(f)
		w.Header().Set("Grpc-Status", "0")
	}))
	var p http.Protocols
	p.SetUnencryptedHTTP2(true)
	srv.Config.Protocols = &p
	srv.Start()
	t.Cleanup(srv.Close)
	return "h2c://" + strings.TrimPrefix(srv.URL, "http://")
}

func waitReady(t *testing.T, addr string) {
	deadline := time.Now().Add(5 * time.Second)
	for {
		if resp, err := http.Get("http://" + addr + "/readyz"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("gateway never became ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func post(t *testing.T, addr, path, body string) (int, map[string]any) {
	req, _ := http.NewRequest("POST", "http://"+addr+path, strings.NewReader(body))
	req.Header.Set("X-API-Key", "abcdefgh")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

// The whole pipeline over real sockets: chain node → gateway → gRPC engine, plus private-relay broadcast.
func TestIntentPipelineEndToEnd(t *testing.T) {
	sim := listen(t)
	simAddr := sim.Addr().String()
	_ = sim.Close()
	ctx, cancel := context.WithCancel(context.Background())
	simDone := make(chan error, 1)
	go func() {
		simDone <- SimMain(ctx, env(map[string]string{"SIM_ADDR": simAddr, "SIM_BLOCK_MS": "5"}), quiet)
	}()
	cfg, err := LoadConfig(env(map[string]string{
		"GATEWAY_UPSTREAMS": "http://" + simAddr, "GATEWAY_API_KEYS": "alice=abcdefgh", "SIGNER_SHARED_SECRET": "0123456789abcdef",
		"POLL_INTERVAL": "5ms", "ENGINE_ADDR": fakeEngine(t), "RELAY_URLS": "http://" + simAddr,
	}))
	if err != nil {
		t.Fatal(err)
	}
	gw := listen(t)
	gwDone := make(chan error, 1)
	go func() { gwDone <- Serve(ctx, gw, cfg, quiet) }()
	addr := gw.Addr().String()
	waitReady(t, addr)

	code, m := post(t, addr, "/v1/intent", `{"chain_id":1337,"from":"0x1111111111111111111111111111111111111111","to":"0x2222222222222222222222222222222222222222","value":"1000"}`)
	tx, _ := m["tx"].(map[string]any)
	sm, _ := m["simulation"].(map[string]any)
	if code != 200 || sm["outcome"] != "success" || sm["gas_used"] != float64(21000) || tx["gas_limit"] != float64(25200) || tx["nonce"] != float64(0) {
		t.Fatalf("intent: %d %v", code, m)
	}
	if code, m = post(t, addr, "/v1/broadcast", `{"raw_tx":"0x0102"}`); code != 200 || !strings.HasPrefix(m["tx_id"].(string), "0x") {
		t.Fatalf("broadcast: %d %v", code, m)
	}
	cancel()
	if err := <-gwDone; err != nil {
		t.Fatal(err)
	}
	if err := <-simDone; err != nil {
		t.Fatal(err)
	}
}

func TestServeSolanaModeAndBadEngineTLS(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cfg, _ := LoadConfig(env(map[string]string{
		"GATEWAY_UPSTREAMS": "http://127.0.0.1:1", "GATEWAY_API_KEYS": "alice=abcdefgh", "SIGNER_SHARED_SECRET": "0123456789abcdef",
		"POLL_INTERVAL": "5ms", "GATEWAY_CHAIN": "solana", "RELAY_URLS": "http://127.0.0.1:1",
	}))
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, listen(t), cfg, quiet) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	cfg.EngineAddr, cfg.EngineCert, cfg.EngineKey, cfg.EngineCA = "engine:50051", "/missing", "/missing", "/missing"
	if err := Serve(context.Background(), listen(t), cfg, quiet); err == nil {
		t.Fatal("unusable engine mTLS material must stop startup")
	}
}
