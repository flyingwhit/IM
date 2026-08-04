package config

import (
	"os"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	// Set required env vars
	envs := map[string]string{
		"DB_HOST":             "localhost",
		"DB_USER":             "testuser",
		"DB_PASSWORD":         "testpass",
		"DB_NAME":             "testdb",
		"JWT_ACCESS_SECRET":   "access-secret",
		"JWT_REFRESH_SECRET":  "refresh-secret",
	}
	for k, v := range envs {
		os.Setenv(k, v)
		defer os.Unsetenv(k)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if cfg.DB.DSN() != "postgres://testuser:testpass@localhost:5432/testdb?sslmode=disable" {
		t.Errorf("unexpected DSN: %s", cfg.DB.DSN())
	}

	if cfg.Redis.Addr() != "localhost:6379" {
		t.Errorf("unexpected Redis addr: %s", cfg.Redis.Addr())
	}

	if cfg.JWT.AccessExpiry != 15*time.Minute {
		t.Errorf("expected 15m access expiry, got %v", cfg.JWT.AccessExpiry)
	}

	if cfg.JWT.RefreshExpiry != 168*time.Hour {
		t.Errorf("expected 168h refresh expiry, got %v", cfg.JWT.RefreshExpiry)
	}
}

func TestRequireEnvError(t *testing.T) {
	os.Unsetenv("DB_HOST")
	_, err := Load()
	if err == nil {
		t.Error("expected error for missing required env")
	}
}
