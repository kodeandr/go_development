package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kodeandr/go_development/ledger"
	"github.com/kodeandr/go_development/ledger/httpserver"
)

func main() {
	budgetsFile := flag.String("budgets", "", "load budgets from a JSON file before starting")
	flag.Parse()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil)).With("service", "ledger")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, logger, *budgetsFile); err != nil {
		logger.Error("Ledger stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger, budgetsFile string) error {
	service, err := ledger.Open(ctx, logger)
	if err != nil {
		return err
	}
	defer service.Close()
	if budgetsFile != "" {
		if err := loadFile(ctx, service, budgetsFile); err != nil {
			return err
		}
		logger.Info("budgets loaded", "file", budgetsFile)
	}
	logger.Info("Ledger service started")
	addr := os.Getenv("LEDGER_ADDR")
	if addr == "" {
		addr = ":8081"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		checkCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err := service.Ping(checkCtx); err != nil {
			logger.Error("health check failed", "error", err)
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "dependencies unavailable"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	return httpserver.Run(ctx, addr, mux, logger)
}

func loadFile(ctx context.Context, service *ledger.Service, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open budgets file: %w", err)
	}
	defer file.Close()
	return service.LoadBudgets(ctx, bufio.NewReader(file))
}
