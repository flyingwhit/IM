package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ciel/im/internal/service"
)

// UserIDKey is the context key for the authenticated user's ID.
const UserIDKey = "userID"

// AuthRequired returns a middleware that validates the JWT access token.
// On success, it injects the user ID into the Gin context under UserIDKey.
// On failure, it aborts with 401.
func AuthRequired(authService *service.AuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := extractBearerToken(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "missing or malformed Authorization header",
			})
			return
		}

		userID, err := authService.ValidateAccessToken(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "invalid or expired access token",
			})
			return
		}

		c.Set(UserIDKey, userID)
		c.Next()
	}
}

// extractBearerToken pulls the token from "Authorization: Bearer <token>".
func extractBearerToken(c *gin.Context) (string, bool) {
	auth := c.GetHeader("Authorization")
	if auth == "" {
		return "", false
	}

	// Must be "Bearer <token>"
	if !strings.HasPrefix(auth, "Bearer ") {
		return "", false
	}

	token := strings.TrimPrefix(auth, "Bearer ")
	if token == "" {
		return "", false
	}

	return token, true
}
