package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/ciel/im/internal/config"
	"github.com/ciel/im/internal/model"
	"github.com/ciel/im/internal/repository/postgres"
	redisrepo "github.com/ciel/im/internal/repository/redis"
)

// AuthService handles authentication business logic.
type AuthService struct {
	userRepo    *postgres.UserRepo
	sessionRepo *redisrepo.SessionRepo
	jwtCfg      config.JWTConfig
}

func NewAuthService(
	userRepo *postgres.UserRepo,
	sessionRepo *redisrepo.SessionRepo,
	jwtCfg config.JWTConfig,
) *AuthService {
	return &AuthService{
		userRepo:    userRepo,
		sessionRepo: sessionRepo,
		jwtCfg:      jwtCfg,
	}
}

// Register creates a new user account.
func (s *AuthService) Register(ctx context.Context, req model.RegisterRequest) (*model.User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	user := &model.User{
		Username:     req.Username,
		Email:        req.Email,
		PasswordHash: string(hash),
	}

	if err := s.userRepo.Create(ctx, user); err != nil {
		return nil, err
	}
	return user, nil
}

// Login authenticates a user and returns token pair.
func (s *AuthService) Login(ctx context.Context, req model.LoginRequest) (*model.TokenResponse, error) {
	user, err := s.userRepo.FindByUsername(ctx, req.Username)
	if err != nil {
		return nil, model.NewAppError(model.ErrUnauthorized, "invalid username or password")
	}

	// Constant-time comparison via bcrypt
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		return nil, model.NewAppError(model.ErrUnauthorized, "invalid username or password")
	}

	return s.generateTokenPair(ctx, user.ID)
}

// Refresh validates a refresh token and issues a new access token.
// It implements token rotation: the old refresh token is invalidated
// and a new one is issued, limiting the window of token theft.
//
// Uses GetAndDelete (Redis GETDEL) to atomically fetch and remove the token.
// This prevents concurrent requests from reusing the same refresh token:
// only the first GETDEL caller sees the value; all others see a miss.
func (s *AuthService) Refresh(ctx context.Context, tokenStr string) (*model.TokenResponse, error) {
	tokenHash := hashToken(tokenStr)

	userID, err := s.sessionRepo.GetAndDelete(ctx, tokenHash)
	if err != nil {
		return nil, model.NewAppError(model.ErrTokenExpired, "refresh token invalid or expired")
	}

	return s.generateTokenPair(ctx, userID)
}

// Logout invalidates a refresh token.
func (s *AuthService) Logout(ctx context.Context, tokenStr string) error {
	tokenHash := hashToken(tokenStr)
	return s.sessionRepo.Delete(ctx, tokenHash)
}

// --- Token generation helpers ---

type accessClaims struct {
	UserID string `json:"user_id"`
	jwt.RegisteredClaims
}

// generateTokenPair creates both access and refresh tokens.
func (s *AuthService) generateTokenPair(ctx context.Context, userID string) (*model.TokenResponse, error) {
	now := time.Now()

	// Access token (JWT)
	accessExpiry := now.Add(s.jwtCfg.AccessExpiry)
	claims := accessClaims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(accessExpiry),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	accessToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(s.jwtCfg.AccessSecret))
	if err != nil {
		return nil, fmt.Errorf("sign access token: %w", err)
	}

	// Refresh token (random opaque string)
	refreshToken, err := generateRandomToken(32)
	if err != nil {
		return nil, fmt.Errorf("generate refresh token: %w", err)
	}

	// Store refresh token hash in Redis
	refreshHash := hashToken(refreshToken)
	if err := s.sessionRepo.Store(ctx, refreshHash, userID, s.jwtCfg.RefreshExpiry); err != nil {
		return nil, fmt.Errorf("store refresh token: %w", err)
	}

	return &model.TokenResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    int64(s.jwtCfg.AccessExpiry.Seconds()),
	}, nil
}

// ValidateAccessToken parses and validates an access token, returning the user ID.
func (s *AuthService) ValidateAccessToken(tokenStr string) (string, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &accessClaims{},
		func(t *jwt.Token) (interface{}, error) {
			// Validate signing algorithm
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return []byte(s.jwtCfg.AccessSecret), nil
		},
	)
	if err != nil {
		return "", model.NewAppError(model.ErrUnauthorized, "invalid or expired access token")
	}

	claims, ok := token.Claims.(*accessClaims)
	if !ok || !token.Valid {
		return "", model.NewAppError(model.ErrUnauthorized, "invalid access token claims")
	}

	return claims.UserID, nil
}

// --- Utility functions ---

// generateRandomToken creates a cryptographically random hex string.
func generateRandomToken(byteLen int) (string, error) {
	b := make([]byte, byteLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// hashToken hashes a token string with SHA-256 for storage.
// We store hashes, not raw tokens — if Redis is compromised, tokens aren't leaked.
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
