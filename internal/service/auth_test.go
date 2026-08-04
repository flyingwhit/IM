package service

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ciel/im/internal/config"
)

// TestValidateAccessToken tests token generation and validation end-to-end.
func TestValidateAccessToken(t *testing.T) {
	svc := &AuthService{
		jwtCfg: config.JWTConfig{
			AccessSecret:  "test-access-secret",
			RefreshSecret: "test-refresh-secret",
			AccessExpiry:  15 * time.Minute,
			RefreshExpiry: 7 * 24 * time.Hour,
		},
	}

	// Generate a token using the same signing method as generateTokenPair
	claims := accessClaims{
		UserID: "test-user-id",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, err := token.SignedString([]byte(svc.jwtCfg.AccessSecret))
	if err != nil {
		t.Fatalf("failed to sign test token: %v", err)
	}

	// Validate
	userID, err := svc.ValidateAccessToken(tokenStr)
	if err != nil {
		t.Fatalf("expected valid token, got error: %v", err)
	}
	if userID != "test-user-id" {
		t.Errorf("expected userID 'test-user-id', got '%s'", userID)
	}
}

func TestValidateAccessToken_Expired(t *testing.T) {
	svc := &AuthService{
		jwtCfg: config.JWTConfig{
			AccessSecret: "test-access-secret",
		},
	}

	// Generate an expired token
	claims := accessClaims{
		UserID: "test-user-id",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, _ := token.SignedString([]byte(svc.jwtCfg.AccessSecret))

	_, err := svc.ValidateAccessToken(tokenStr)
	if err == nil {
		t.Error("expected error for expired token")
	}
}

func TestValidateAccessToken_WrongSecret(t *testing.T) {
	svc := &AuthService{
		jwtCfg: config.JWTConfig{
			AccessSecret: "test-access-secret",
		},
	}

	// Sign with wrong secret
	claims := accessClaims{
		UserID: "test-user-id",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, _ := token.SignedString([]byte("wrong-secret"))

	_, err := svc.ValidateAccessToken(tokenStr)
	if err == nil {
		t.Error("expected error for token signed with wrong secret")
	}
}

func TestHashToken(t *testing.T) {
	token := "my-test-refresh-token"
	h1 := hashToken(token)
	h2 := hashToken(token)

	if h1 != h2 {
		t.Error("hash should be deterministic")
	}
	if h1 == token {
		t.Error("hash should not equal the original token")
	}
}

func TestGenerateRandomToken(t *testing.T) {
	t1, err := generateRandomToken(32)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(t1) != 64 { // 32 bytes = 64 hex chars
		t.Errorf("expected 64 chars, got %d", len(t1))
	}

	t2, _ := generateRandomToken(32)
	if t1 == t2 {
		t.Error("two random tokens should differ")
	}
}
