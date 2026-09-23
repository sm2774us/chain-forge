//go:build interop

package engineclient_test

import (
	"context"
	"errors"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/chainforge/gateway/internal/engineclient"
	"github.com/chainforge/gateway/internal/grpcx"
)

// Real Go client ↔ real Rust engine over a Unix socket. Run with:
//
//	ENGINE_BIN=target/release/engine go test -tags interop ./internal/engineclient
func startEngine(tb testing.TB) *engineclient.Client {
	bin := os.Getenv("ENGINE_BIN")
	if bin == "" {
		tb.Skip("ENGINE_BIN not set")
	}
	sock := filepath.Join(tb.TempDir(), "engine.sock")
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "ENGINE_LISTEN=unix://"+sock)
	if err := cmd.Start(); err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	for i := 0; i < 200; i++ {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	conn, err := grpcx.Dial("unix://"+sock, nil)
	if err != nil {
		tb.Fatal(err)
	}
	return engineclient.New(conn)
}

// BenchmarkSimulateOverUnixSocket measures full Go→gRPC→revm→Go round trips:
//
//	ENGINE_BIN=target/release/engine go test -tags interop -bench Simulate ./internal/engineclient
func BenchmarkSimulateOverUnixSocket(b *testing.B) {
	c := startEngine(b)
	alice, bob := engineclient.Address{0xaa}, engineclient.Address{0xbb}
	req := engineclient.SimRequest{ChainID: 1, From: alice, To: &bob, Value: big.NewInt(1000), GasLimit: 100_000,
		State: []engineclient.Account{{Address: alice, Balance: big.NewInt(10_000)}}}
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.Simulate(ctx, req); err != nil {
			b.Fatal(err)
		}
	}
}

func TestGoClientAgainstRustEngine(t *testing.T) {
	c := startEngine(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if v, err := c.Health(ctx); err != nil || v == "" {
		t.Fatalf("health: %q %v", v, err)
	}
	alice, bob := engineclient.Address{0xaa}, engineclient.Address{0xbb}
	res, err := c.Simulate(ctx, engineclient.SimRequest{
		ChainID: 1, From: alice, To: &bob, Value: big.NewInt(1000), GasLimit: 100_000,
		State: []engineclient.Account{{Address: alice, Balance: big.NewInt(10_000)}},
	})
	if err != nil || res.Outcome != engineclient.Success || res.GasUsed != 21000 || len(res.BalanceChanges) != 2 {
		t.Fatalf("simulate: %+v %v", res, err)
	}
	t.Logf("engine simulated a transfer in %dµs", res.ElapsedUS)

	// engine-side validation surfaces as a typed gRPC status
	_, err = c.Simulate(ctx, engineclient.SimRequest{ChainID: 1, From: alice, GasLimit: 999_999_999})
	var st *grpcx.Status
	if !errors.As(err, &st) || st.Code != grpcx.ResourceExhausted {
		t.Fatalf("gas ceiling: %v", err)
	}
	_, err = c.Simulate(ctx, engineclient.SimRequest{ChainID: 1, From: alice, To: &bob, Value: big.NewInt(5), GasLimit: 100_000})
	if !errors.As(err, &st) || st.Code != grpcx.FailedPrecondition {
		t.Fatalf("insufficient funds: %v", err)
	}
}
