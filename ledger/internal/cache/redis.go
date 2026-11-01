package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const ReportTTL = 30 * time.Second

type Cache struct {
	client *redis.Client
	prefix string
}

func Open(ctx context.Context, options *redis.Options, prefix string) (*Cache, error) {
	client := redis.NewClient(options)
	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("Redis is unavailable; check REDIS_ADDR, REDIS_DB and REDIS_PASSWORD: %w", err)
	}
	return New(client, prefix), nil
}

func New(client *redis.Client, prefix string) *Cache {
	return &Cache{client: client, prefix: prefix}
}

func (c *Cache) Close() error { return c.client.Close() }

func (c *Cache) Ping(ctx context.Context) error { return c.client.Ping(ctx).Err() }

func (c *Cache) ReportKey(ctx context.Context, from, to string) (string, error) {
	version, err := c.client.Get(ctx, c.prefix+"report:version").Result()
	if errors.Is(err, redis.Nil) {
		version = "0"
	} else if err != nil {
		return "", err
	}
	return c.prefix + "report:summary:" + from + ":" + to + ":" + version, nil
}

func (c *Cache) Get(ctx context.Context, key string, result any) (bool, error) {
	data, err := c.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(data, result); err != nil {
		return false, fmt.Errorf("decode cached report: %w", err)
	}
	return true, nil
}

func (c *Cache) Set(ctx context.Context, key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.client.Set(ctx, key, data, ReportTTL).Err()
}

// Старые ключи удалит TTL. Версия не даёт запросу, начатому до записи расхода,
// снова положить устаревший отчёт под актуальным ключом.
func (c *Cache) InvalidateReports(ctx context.Context) error {
	return c.client.Incr(ctx, c.prefix+"report:version").Err()
}
