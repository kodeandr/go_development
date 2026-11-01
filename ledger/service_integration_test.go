//go:build integration

package ledger

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kodeandr/go_development/ledger/internal/cache"
	"github.com/kodeandr/go_development/ledger/internal/db"
	"github.com/redis/go-redis/v9"
)

type fixture struct {
	db         *sql.DB
	dsn        string
	redis      *redis.Client
	cache      *cache.Cache
	prefix     string
	service    *Service
	migrations string
	goose      string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("set TEST_DATABASE_URL to a PostgreSQL URL; integration tests create and remove isolated schemas")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	admin, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	schema := fmt.Sprintf("ledger_test_%d", time.Now().UnixNano())
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
	f := &fixture{dsn: u.String(), prefix: schema + ":"}
	f.db, err = db.Open(ctx, f.dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.db.Close() })
	f.migrations, err = filepath.Abs("migrations")
	if err != nil {
		t.Fatal(err)
	}
	f.goose, err = filepath.Abs("../.bin/goose")
	if err != nil {
		t.Fatal(err)
	}
	if binary := os.Getenv("GOOSE_BIN"); binary != "" {
		f.goose = binary
	}
	f.migrate(t, "up")
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	f.redis = redis.NewClient(&redis.Options{
		Addr: addr, DB: 15, Password: os.Getenv("TEST_REDIS_PASSWORD"),
		DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second,
		ContextTimeoutEnabled: true, MaxRetries: -1,
	})
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		var cursor uint64
		for {
			keys, next, err := f.redis.Scan(cleanupCtx, cursor, f.prefix+"*", 100).Result()
			if err != nil {
				t.Errorf("scan test cache keys: %v", err)
				break
			}
			if len(keys) != 0 {
				if err := f.redis.Del(cleanupCtx, keys...).Err(); err != nil {
					t.Errorf("remove test cache keys: %v", err)
				}
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
		f.redis.Close()
	})
	if err := f.redis.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	f.cache = cache.New(f.redis, f.prefix)
	f.service = newService(f.db, f.cache, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return f
}

func (f *fixture) migrate(t *testing.T, command string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, f.goose, "-dir", f.migrations, "postgres", f.dsn, command).CombinedOutput()
	if err != nil {
		t.Fatalf("goose %s: %v\n%s\nInstall Goose with make tools before integration tests.", command, err, output)
	}
}

func add(t *testing.T, f *fixture, category string, amount Money, date string) Transaction {
	t.Helper()
	expense := Transaction{Category: category, Amount: amount, Date: date, Description: "test purchase"}
	if err := f.service.AddTransaction(context.Background(), &expense); err != nil {
		t.Fatal(err)
	}
	if expense.ID <= 0 {
		t.Fatal("database-generated id was not returned")
	}
	return expense
}

func TestIntegrationBudgetUpsertAndLimits(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	for _, limit := range []Money{5_000, 10_000} {
		if err := f.service.SetBudget(ctx, Budget{Category: "food", Limit: limit}); err != nil {
			t.Fatal(err)
		}
	}
	budgets, err := f.service.ListBudgets(ctx)
	if err != nil || len(budgets) != 1 || budgets[0].Limit != 10_000 {
		t.Fatalf("upsert: %+v, %v", budgets, err)
	}
	add(t, f, "food", 6_010, "2026-01-10")
	last := add(t, f, "food", 3_990, "2026-01-10")
	rejected := Transaction{Category: "food", Amount: 1, Date: "2026-01-10"}
	if err := f.service.AddTransaction(ctx, &rejected); !errors.Is(err, ErrBudgetExceeded) || rejected.ID != 0 {
		t.Fatalf("expected a budget conflict without an id; got %v, id=%d", err, rejected.ID)
	}
	transactions, err := f.service.ListTransactions(ctx)
	if err != nil || len(transactions) != 2 || transactions[0].ID != last.ID {
		t.Fatalf("rejected transaction was inserted or ordering is wrong: %+v, %v", transactions, err)
	}
	add(t, f, "without-budget", 50_000, "2026-01-11")
	var spent Money
	if err := f.db.QueryRow(`SELECT SUM(amount) FROM expenses WHERE category='food'`).Scan(&spent); err != nil || spent != 10_000 {
		t.Fatalf("spent %s, error %v", spent, err)
	}
}

func TestIntegrationValidationAndConstraints(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	if err := f.service.SetBudget(ctx, Budget{Category: "food", Limit: 0}); err == nil {
		t.Fatal("zero budget was accepted")
	}
	for _, tx := range []Transaction{
		{Category: "food", Amount: 0, Date: "2026-01-01"},
		{Category: "food", Amount: -100, Date: "2026-01-01"},
		{Category: " ", Amount: 100, Date: "2026-01-01"},
		{Category: "food", Amount: 100, Date: "2026-02-30"},
	} {
		if err := f.service.AddTransaction(ctx, &tx); err == nil {
			t.Fatalf("invalid transaction accepted: %+v", tx)
		}
	}
	transactions, err := f.service.ListTransactions(ctx)
	if err != nil || transactions == nil || len(transactions) != 0 {
		t.Fatalf("invalid rows persisted: %+v, %v", transactions, err)
	}
	for _, query := range []string{
		`INSERT INTO budgets(category, limit_amount) VALUES ('zero', 0)`,
		`INSERT INTO budgets(category, limit_amount) VALUES ('negative', -1)`,
		`INSERT INTO expenses(amount, category, date) VALUES (0, 'food', '2026-01-01')`,
	} {
		if _, err := f.db.Exec(query); err == nil {
			t.Fatalf("database constraint is missing: %s", query)
		}
	}
}

func TestIntegrationConcurrentSpending(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := f.service.SetBudget(ctx, Budget{Category: "food", Limit: 10_000}); err != nil {
		t.Fatal(err)
	}
	var accepted, conflicts atomic.Int32
	var group sync.WaitGroup
	for i := 0; i < 20; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			tx := Transaction{Category: "food", Amount: 1_000, Date: "2026-01-01"}
			err := f.service.AddTransaction(ctx, &tx)
			switch {
			case err == nil:
				accepted.Add(1)
			case errors.Is(err, ErrBudgetExceeded):
				conflicts.Add(1)
			default:
				t.Errorf("concurrent request failed: %v", err)
			}
		}()
	}
	group.Wait()
	if accepted.Load() != 10 || conflicts.Load() != 10 {
		t.Fatalf("accepted=%d, conflicts=%d; want 10 and 10", accepted.Load(), conflicts.Load())
	}
	var count int
	var total Money
	if err := f.db.QueryRow(`SELECT COUNT(*), SUM(amount) FROM expenses`).Scan(&count, &total); err != nil || count != 10 || total != 10_000 {
		t.Fatalf("rows=%d, total=%s, error=%v", count, total, err)
	}
}

func TestIntegrationDataSurvivesNewConnection(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	if err := f.service.SetBudget(ctx, Budget{Category: "food", Limit: 10_000}); err != nil {
		t.Fatal(err)
	}
	expense := add(t, f, "food", 123, "2026-01-01")
	f.db.Close()
	var err error
	f.db, err = db.Open(ctx, f.dsn)
	if err != nil {
		t.Fatal(err)
	}
	f.service = newService(f.db, f.cache, slog.Default())
	budgets, err := f.service.ListBudgets(ctx)
	if err != nil || len(budgets) != 1 {
		t.Fatalf("budgets after reopening: %+v, %v", budgets, err)
	}
	transactions, err := f.service.ListTransactions(ctx)
	if err != nil || len(transactions) != 1 || transactions[0] != expense {
		t.Fatalf("transactions after reopening: %+v, %v", transactions, err)
	}
}

func TestIntegrationReportCache(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	var logs bytes.Buffer
	f.service.log = slog.New(slog.NewTextHandler(&logs, nil))
	add(t, f, "food", 1_000, "2026-01-01")
	add(t, f, "food", 100, "2026-01-31")
	add(t, f, "travel", 500, "2026-01-15")
	add(t, f, "outside", 99_900, "2025-12-31")
	add(t, f, "outside", 99_900, "2026-02-01")
	first, err := f.service.GetReportSummary(ctx, "2026-01-01", "2026-01-31")
	if err != nil || first.Total != 1_600 || len(first.ByCategory) != 2 || first.ByCategory[0].Category != "food" {
		t.Fatalf("report: %+v, %v", first, err)
	}
	key, err := f.cache.ReportKey(ctx, first.From, first.To)
	if err != nil {
		t.Fatal(err)
	}
	ttl, err := f.redis.TTL(ctx, key).Result()
	if err != nil || ttl <= 0 || ttl > cache.ReportTTL {
		t.Fatalf("report TTL: %v, %v", ttl, err)
	}
	// Закрытая БД доказывает, что второй запрос читает Redis, а не пересчитывает отчёт.
	f.db.Close()
	second, err := f.service.GetReportSummary(ctx, first.From, first.To)
	if err != nil || second.Total != first.Total {
		t.Fatalf("cache hit with closed database: %+v, %v", second, err)
	}
	f.db, err = db.Open(ctx, f.dsn)
	if err != nil {
		t.Fatal(err)
	}
	f.service.db = f.db
	if !strings.Contains(logs.String(), "cache=hit") || !strings.Contains(logs.String(), "cache=miss") {
		t.Fatalf("cache hit/miss were not logged: %s", logs.String())
	}
	if _, err := f.db.Exec(`INSERT INTO expenses(amount, category, date) VALUES (2.00, 'food', '2026-01-15')`); err != nil {
		t.Fatal(err)
	}
	if err := f.redis.PExpire(ctx, key, time.Millisecond).Err(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	afterExpiry, err := f.service.GetReportSummary(ctx, first.From, first.To)
	if err != nil || afterExpiry.Total != 1_800 {
		t.Fatalf("report was not recomputed after expiry: %+v, %v", afterExpiry, err)
	}
	add(t, f, "food", 100, "2026-01-15")
	afterWrite, err := f.service.GetReportSummary(ctx, first.From, first.To)
	if err != nil || afterWrite.Total != 1_900 {
		t.Fatalf("report was not invalidated after a transaction: %+v, %v", afterWrite, err)
	}
	newKey, err := f.cache.ReportKey(ctx, first.From, first.To)
	if err != nil || newKey == key {
		t.Fatalf("report key version did not change: %s, %v", newKey, err)
	}
	if err := f.redis.Set(ctx, newKey, "not JSON", cache.ReportTTL).Err(); err != nil {
		t.Fatal(err)
	}
	recovered, err := f.service.GetReportSummary(ctx, first.From, first.To)
	if err != nil || recovered.Total != 1_900 {
		t.Fatalf("corrupted cache must fall back to PostgreSQL: %+v, %v", recovered, err)
	}
}

func TestIntegrationRedisFailureDoesNotLoseTransactions(t *testing.T) {
	f := setup(t)
	brokenClient := redis.NewClient(&redis.Options{
		Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond,
		ContextTimeoutEnabled: true, MaxRetries: -1,
	})
	defer brokenClient.Close()
	f.service.cache = cache.New(brokenClient, f.prefix)
	add(t, f, "food", 250, "2026-01-01")
	report, err := f.service.GetReportSummary(context.Background(), "2026-01-01", "2026-01-01")
	if err != nil || report.Total != 250 {
		t.Fatalf("Redis failure broke the PostgreSQL workflow: %+v, %v", report, err)
	}
}

func TestIntegrationMigrationsUpAndDown(t *testing.T) {
	f := setup(t)
	f.migrate(t, "up")
	for i := 0; i < 2; i++ {
		f.migrate(t, "down")
	}
	var budgets, expenses sql.NullString
	if err := f.db.QueryRow(`SELECT to_regclass('budgets')::text, to_regclass('expenses')::text`).Scan(&budgets, &expenses); err != nil || budgets.Valid || expenses.Valid {
		t.Fatalf("down did not remove tables: %v, %v, %v", budgets, expenses, err)
	}
	f.migrate(t, "up")
}

func TestIntegrationLoadBudgets(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	input := `[{"category":"food","limit":100},{"category":"travel","limit":50},{"category":"food","limit":200}]`
	if err := f.service.LoadBudgets(ctx, strings.NewReader(input)); err != nil {
		t.Fatal(err)
	}
	budgets, err := f.service.ListBudgets(ctx)
	if err != nil || len(budgets) != 2 || budgets[0].Category != "food" || budgets[0].Limit != 20_000 {
		t.Fatalf("imported budgets: %+v, %v", budgets, err)
	}
	if err := f.service.LoadBudgets(ctx, strings.NewReader(`[{"category":"new","limit":5},{"category":"invalid","limit":0}]`)); err == nil {
		t.Fatal("invalid batch was accepted")
	}
	budgets, err = f.service.ListBudgets(ctx)
	if err != nil || len(budgets) != 2 {
		t.Fatalf("invalid batch partially persisted: %+v, %v", budgets, err)
	}
}

func TestIntegrationOpenAndPool(t *testing.T) {
	f := setup(t)
	t.Setenv("DATABASE_URL", f.dsn)
	t.Setenv("REDIS_ADDR", f.redis.Options().Addr)
	t.Setenv("REDIS_DB", "15")
	t.Setenv("REDIS_PASSWORD", f.redis.Options().Password)
	t.Setenv("REDIS_KEY_PREFIX", f.prefix)
	service, err := Open(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if err := service.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := service.db.Stats().MaxOpenConnections; got != 10 {
		t.Fatalf("max open connections = %d, want 10", got)
	}
}
