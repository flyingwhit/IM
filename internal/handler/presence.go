package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	redisrepo "github.com/ciel/im/internal/repository/redis"
)

// PresenceHandler handles online status HTTP endpoints.
type PresenceHandler struct {
	presenceRepo *redisrepo.PresenceRepo
}

// NewPresenceHandler creates a PresenceHandler.
func NewPresenceHandler(presenceRepo *redisrepo.PresenceRepo) *PresenceHandler {
	return &PresenceHandler{presenceRepo: presenceRepo}
}

// GetOnlineStatus handles GET /api/v1/users/:id/online.
// It returns whether the specified user is currently online.
func (h *PresenceHandler) GetOnlineStatus(c *gin.Context) {
	userID := c.Param("id")
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing user id"})
		return
	}

	online, err := h.presenceRepo.IsOnline(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check online status"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"user_id": userID,
		"online":  online,
	})
}
