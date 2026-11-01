package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/kodeandr/go_development/gateway/internal/api"
	"github.com/kodeandr/go_development/ledger"
	"github.com/kodeandr/go_development/ledger/httpserver"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil)).With("service", "gateway")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, logger); err != nil {
		logger.Error("Gateway stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	service, err := ledger.Open(ctx, logger)
	if err != nil {
		return err
	}
	defer service.Close()
	addr := os.Getenv("GATEWAY_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	return httpserver.Run(ctx, addr, api.New(service, service.Ping, logger), logger)
}
