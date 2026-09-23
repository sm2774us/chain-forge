// Command gateway is the ChainForge RPC gateway.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/chainforge/gateway/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := app.Main(ctx, app.Env(), log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}
