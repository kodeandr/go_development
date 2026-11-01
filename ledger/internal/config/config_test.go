package config

import (
	"net/url"
	"testing"
)

func TestDatabaseURLPriority(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example/db?sslmode=require")
	t.Setenv("DB_HOST", "ignored")
	t.Setenv("REDIS_DB", "3")
	t.Setenv("REDIS_ADDR", "redis:6379")
	t.Setenv("REDIS_PASSWORD", "test-only")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.DatabaseURL != "postgres://example/db?sslmode=require" || c.RedisDB != 3 || c.RedisAddr != "redis:6379" || c.RedisPassword != "test-only" {
		t.Fatal("configuration does not use the supplied environment")
	}
}

func TestDatabaseComponents(t *testing.T) {
	for _, name := range []string{"DATABASE_URL", "DB_HOST", "DB_PORT", "DB_USER", "DB_PASS", "DB_NAME", "DB_SSLMODE", "REDIS_DB"} {
		t.Setenv(name, "")
	}
	c, err := Load()
	if err != nil || c.DatabaseURL != "postgres://postgres:postgres@localhost:5432/cashapp?sslmode=disable" {
		t.Fatalf("default DSN: %s, %v", c.DatabaseURL, err)
	}
	t.Setenv("DB_PASS", "p@ss:/?#word")
	t.Setenv("DB_HOST", "::1")
	t.Setenv("DB_SSLMODE", "require")
	c, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(c.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	password, _ := u.User.Password()
	if password != "p@ss:/?#word" || u.Host != "[::1]:5432" || u.Query().Get("sslmode") != "require" {
		t.Fatal("DSN components were not escaped correctly")
	}
}

func TestInvalidRedisDB(t *testing.T) {
	for _, value := range []string{"abc", "-1", "1.5"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("REDIS_DB", value)
			if _, err := Load(); err == nil {
				t.Fatal("expected configuration error")
			}
		})
	}
}
