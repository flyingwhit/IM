package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// HealthHandler provides a simple health check endpoint.
type HealthHandler struct{}

func NewHealthHandler() *HealthHandler {
	return &HealthHandler{}
}

// Check handles GET /health. Returns 200 if the server is running.
// Phase 2 will add PostgreSQL and Redis connectivity checks.
func (h *HealthHandler) Check(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
