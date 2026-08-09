package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	// Set required env vars
	envs := map[string]string{
		"DB_HOST":            "localhost",
		"DB_USER":            "testuser",
		"DB_PASSWORD":        "testpass",
		"DB_NAME":            "testdb",
		"JWT_ACCESS_SECRET":  "access-secret",
		"JWT_REFRESH_SECRET": "refresh-secret",
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

func TestValidate_DefaultSecrets(t *testing.T) {
	cfg := &Config{
		JWT: JWTConfig{
			AccessSecret:  "change-me-access-secret-key",
			RefreshSecret: "change-me-refresh-secret-key",
		},
	}
	ws := cfg.Validate()
	if len(ws) < 2 {
		t.Errorf("expected at least 2 warnings for default secrets, got %d: %v", len(ws), ws)
	}
}

func TestValidate_IdenticalSecrets(t *testing.T) {
	cfg := &Config{
		JWT: JWTConfig{
			AccessSecret:  "same-secret-value-here-32bytes!",
			RefreshSecret: "same-secret-value-here-32bytes!",
		},
	}
	ws := cfg.Validate()
	hasIdentical := false
	for _, w := range ws {
		if strings.Contains(w, "identical") {
			hasIdentical = true
		}
	}
	if !hasIdentical {
		t.Errorf("expected warning about identical secrets, got: %v", ws)
	}
}

func TestValidate_ShortSecret(t *testing.T) {
	cfg := &Config{
		JWT: JWTConfig{
			AccessSecret:  "short",
			RefreshSecret: "some-different-longer-secret",
		},
	}
	ws := cfg.Validate()
	hasShort := false
	for _, w := range ws {
		if strings.Contains(w, "too short") {
			hasShort = true
		}
	}
	if !hasShort {
		t.Errorf("expected warning about short secret, got: %v", ws)
	}
}

func TestValidate_AllGood(t *testing.T) {
	cfg := &Config{
		JWT: JWTConfig{
			AccessSecret:  "a-strong-random-access-secret-key",
			RefreshSecret: "a-different-strong-refresh-secret",
		},
	}
	ws := cfg.Validate()
	if len(ws) > 0 {
		t.Errorf("expected no warnings, got: %v", ws)
	}
}

func TestMask(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"ab", "***"},
		{"abcd", "***"},
		{"abcdefgh", "ab***gh"},
	}
	for _, tt := range tests {
		got := mask(tt.in)
		if got != tt.want {
			t.Errorf("mask(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
