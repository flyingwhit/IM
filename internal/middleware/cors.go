package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// CORS returns middleware that allows cross-origin requests from any origin.
//
// This is intentionally permissive — the test client runs on file:// or
// localhost with varying ports. In production, this should be restricted to
// the known frontend origin(s).
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Reflect the request origin instead of hardcoding a specific one.
		// If the Origin header is absent (e.g. same-origin requests), no
		// ACAO header is needed.
		if origin := c.Request.Header.Get("Origin"); origin != "" {
			c.Header("Access-Control-Allow-Origin", origin)
		}

		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Requested-With")
		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Access-Control-Max-Age", "86400") // 24h — browsers cache preflight

		// Preflight requests: respond 204 and abort.
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}
