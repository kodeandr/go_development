//go:build integration

package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kodeandr/go_development/ledger"
	"github.com/redis/go-redis/v9"
)

func TestIntegrationGatewayAPI(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("set TEST_DATABASE_URL to run the PostgreSQL/Redis HTTP integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	schema := fmt.Sprintf("gateway_test_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := admin.ExecContext(cleanupCtx, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Errorf("remove test schema: %v", err)
		}
	})
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme == "" {
		t.Fatal("TEST_DATABASE_URL must be a postgres:// URL")
	}
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	goose, err := filepath.Abs("../../../.bin/goose")
	if err != nil {
		t.Fatal(err)
	}
	if binary := os.Getenv("GOOSE_BIN"); binary != "" {
		goose = binary
	}
	migrations, err := filepath.Abs("../../../ledger/migrations")
	if err != nil {
		t.Fatal(err)
	}
	if output, err := exec.CommandContext(ctx, goose, "-dir", migrations, "postgres", u.String(), "up").CombinedOutput(); err != nil {
		t.Fatalf("goose up: %v\n%s", err, output)
	}
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	t.Setenv("DATABASE_URL", u.String())
	t.Setenv("REDIS_ADDR", addr)
	t.Setenv("REDIS_DB", "15")
	t.Setenv("REDIS_PASSWORD", os.Getenv("TEST_REDIS_PASSWORD"))
	t.Setenv("REDIS_KEY_PREFIX", schema+":")
	client := redis.NewClient(&redis.Options{Addr: addr, DB: 15, Password: os.Getenv("TEST_REDIS_PASSWORD"), MaxRetries: -1})
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		var cursor uint64
		for {
			keys, next, err := client.Scan(cleanupCtx, cursor, schema+":*", 100).Result()
			if err != nil {
				t.Errorf("scan test cache: %v", err)
				break
			}
			if len(keys) > 0 {
				if err := client.Del(cleanupCtx, keys...).Err(); err != nil {
					t.Errorf("remove test cache: %v", err)
				}
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
		client.Close()
	})
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	service, err := ledger.Open(ctx, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close() })
	handler := New(service, service.Ping, logger)
	request := func(t *testing.T, method, path, body string, status int) []byte {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status || w.Header().Get("Content-Type") != "application/json; charset=utf-8" {
			t.Fatalf("%s %s: %d %s, Content-Type=%q; want %d JSON", method, path, w.Code, w.Body, w.Header().Get("Content-Type"), status)
		}
		if !json.Valid(w.Body.Bytes()) {
			t.Fatalf("invalid JSON response: %s", w.Body)
		}
		return w.Body.Bytes()
	}
	t.Run("empty_lists", func(t *testing.T) {
		for _, path := range []string{"/api/budgets", "/api/transactions"} {
			if body := request(t, "GET", path, "", 200); strings.TrimSpace(string(body)) != "[]" {
				t.Fatalf("empty list: %s", body)
			}
		}
	})
	t.Run("budget_upsert_and_get", func(t *testing.T) {
		for _, limit := range []int{50, 100} {
			body := request(t, "POST", "/api/budgets", fmt.Sprintf(`{"category":"food","limit":%d}`, limit), 201)
			var budget BudgetResponse
			if err := json.Unmarshal(body, &budget); err != nil || budget.Limit != ledger.Money(limit*100) {
				t.Fatalf("created budget: %s, %v", body, err)
			}
			if bytes.Contains(body, []byte("limit_amount")) {
				t.Fatal("SQL column name leaked into the public API")
			}
		}
		var budgets []BudgetResponse
		if err := json.Unmarshal(request(t, "GET", "/api/budgets", "", 200), &budgets); err != nil || len(budgets) != 1 || budgets[0].Limit != 10_000 {
			t.Fatalf("budget list: %+v, %v", budgets, err)
		}
	})
	t.Run("bad_budgets", func(t *testing.T) {
		for _, body := range []string{`{`, `{"category":"food","limit":0}`, `{"category":"food","limit":-1}`, `{"category":"food","limit_amount":100}`} {
			request(t, "POST", "/api/budgets", body, 400)
		}
	})
	t.Run("transactions_at_limit", func(t *testing.T) {
		var lastID int64
		for _, amount := range []string{"60.10", "39.90"} {
			body := request(t, "POST", "/api/transactions", fmt.Sprintf(`{"amount":%s,"category":"food","description":"lunch","date":"2026-01-01"}`, amount), 201)
			var tx TransactionResponse
			if err := json.Unmarshal(body, &tx); err != nil || tx.ID <= lastID {
				t.Fatalf("created transaction: %s, %v", body, err)
			}
			lastID = tx.ID
		}
		var list []TransactionResponse
		if err := json.Unmarshal(request(t, "GET", "/api/transactions", "", 200), &list); err != nil || len(list) != 2 || list[0].ID != lastID {
			t.Fatalf("transactions: %+v, %v", list, err)
		}
	})
	t.Run("exceeded", func(t *testing.T) {
		body := request(t, "POST", "/api/transactions", `{"amount":0.01,"category":"food","date":"2026-01-01"}`, 409)
		if strings.TrimSpace(string(body)) != `{"error":"budget exceeded"}` {
			t.Fatalf("unexpected conflict: %s", body)
		}
	})
	t.Run("bad_transactions", func(t *testing.T) {
		for _, body := range []string{
			`{`, `{"amount":0,"category":"food","date":"2026-01-01"}`,
			`{"amount":-1,"category":"food","date":"2026-01-01"}`,
			`{"amount":1,"category":"","date":"2026-01-01"}`,
			`{"amount":1,"category":"food","date":"2026-02-30"}`,
			`{"id":99,"amount":1,"category":"food","date":"2026-01-01"}`,
		} {
			request(t, "POST", "/api/transactions", body, 400)
		}
		transactions, err := service.ListTransactions(ctx)
		if err != nil || len(transactions) != 2 {
			t.Fatalf("rejected transactions were inserted: %+v, %v", transactions, err)
		}
	})
	t.Run("report_cache", func(t *testing.T) {
		path := "/api/reports/summary?from=2026-01-01&to=2026-01-31"
		first := request(t, "GET", path, "", 200)
		second := request(t, "GET", path, "", 200)
		var report ReportSummaryResponse
		if err := json.Unmarshal(first, &report); err != nil || report.Total != 10_000 || len(report.ByCategory) != 1 || !bytes.Equal(first, second) {
			t.Fatalf("report: %s, %v", first, err)
		}
		for _, field := range []string{"cache=miss", "cache=hit", "method=GET", "path=/api/reports/summary", "duration="} {
			if !strings.Contains(logs.String(), field) {
				t.Fatalf("missing %s in log", field)
			}
		}
	})
	t.Run("health", func(t *testing.T) { request(t, http.MethodGet, "/healthz", "", 200) })
}
