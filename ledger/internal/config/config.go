package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
)

type Config struct {
	DatabaseURL    string
	RedisAddr      string
	RedisDB        int
	RedisPassword  string
	RedisKeyPrefix string
	ListenAddr     string
}

func Load() (Config, error) {
	c := Config{
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		RedisAddr:      env("REDIS_ADDR", "localhost:6379"),
		RedisPassword:  os.Getenv("REDIS_PASSWORD"),
		RedisKeyPrefix: env("REDIS_KEY_PREFIX", "cashcraft:"),
		ListenAddr:     env("LEDGER_ADDR", ":8081"),
	}
	if c.DatabaseURL == "" {
		dsn := url.URL{
			Scheme: "postgres",
			User:   url.UserPassword(env("DB_USER", "postgres"), env("DB_PASS", "postgres")),
			Host:   net.JoinHostPort(env("DB_HOST", "localhost"), env("DB_PORT", "5432")),
			Path:   "/" + env("DB_NAME", "cashapp"),
		}
		query := url.Values{"sslmode": {env("DB_SSLMODE", "disable")}}
		dsn.RawQuery = query.Encode()
		c.DatabaseURL = dsn.String()
	}
	var err error
	c.RedisDB, err = strconv.Atoi(env("REDIS_DB", "0"))
	if err != nil || c.RedisDB < 0 {
		return Config{}, fmt.Errorf("REDIS_DB must be a non-negative integer")
	}
	return c, nil
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
