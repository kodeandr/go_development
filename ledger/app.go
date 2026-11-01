package ledger

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/kodeandr/go_development/ledger/internal/cache"
	"github.com/kodeandr/go_development/ledger/internal/config"
	"github.com/kodeandr/go_development/ledger/internal/db"
	"github.com/redis/go-redis/v9"
)

// Open создаёт экземпляр Ledger с настройками из окружения.
// Gateway использует этот же пакет напрямую, как в контракте ДЗ №5.
func Open(ctx context.Context, logger *slog.Logger) (*Service, error) {
	if logger == nil {
		logger = slog.Default()
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	database, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	logger.Info("connected to PostgreSQL", "max_open_conns", 10, "max_idle_conns", 5)
	reportCache, err := cache.Open(ctx, &redis.Options{
		Addr: cfg.RedisAddr, DB: cfg.RedisDB, Password: cfg.RedisPassword,
		DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second,
		ContextTimeoutEnabled: true, MaxRetries: -1,
	}, cfg.RedisKeyPrefix)
	if err != nil {
		database.Close()
		return nil, err
	}
	logger.Info("connected to Redis", "addr", cfg.RedisAddr, "db", cfg.RedisDB)
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := database.QueryContext(checkCtx, `SELECT b.category, b.limit_amount, e.id, e.amount, e.description, e.date FROM budgets b CROSS JOIN expenses e LIMIT 0`)
	if err != nil {
		reportCache.Close()
		database.Close()
		return nil, fmt.Errorf("database schema is not ready; run goose up before starting Ledger: %w", err)
	}
	rows.Close()
	return newService(database, reportCache, logger), nil
}

func (s *Service) Close() error {
	return errors.Join(s.cache.Close(), s.db.Close())
}

func (s *Service) Ping(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return err
	}
	return s.cache.Ping(ctx)
}
