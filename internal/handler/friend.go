package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/ciel/im/internal/middleware"
	"github.com/ciel/im/internal/service"
)

// FriendHandler handles friend-related HTTP endpoints.
type FriendHandler struct {
	friendService *service.FriendService
}

func NewFriendHandler(friendService *service.FriendService) *FriendHandler {
	return &FriendHandler{friendService: friendService}
}

// SendRequest handles POST /api/v1/friends/requests
func (h *FriendHandler) SendRequest(c *gin.Context) {
	userID := c.GetString(middleware.UserIDKey)

	var req struct {
		TargetID string `json:"target_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	f, err := h.friendService.SendRequest(c.Request.Context(), userID, req.TargetID)
	if err != nil {
		c.JSON(errorStatus(err), gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, f)
}

// ListFriends handles GET /api/v1/friends?offset=0&limit=50.
// Returns accepted friends with pagination.
func (h *FriendHandler) ListFriends(c *gin.Context) {
	userID := c.GetString(middleware.UserIDKey)
	offset, limit := parsePagination(c)

	friends, err := h.friendService.ListFriends(c.Request.Context(), userID, offset, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"friends": friends, "offset": offset, "limit": limit})
}

// ListPendingRequests handles GET /api/v1/friends/requests?offset=0&limit=50.
// Returns incoming pending friend requests with pagination.
func (h *FriendHandler) ListPendingRequests(c *gin.Context) {
	userID := c.GetString(middleware.UserIDKey)
	offset, limit := parsePagination(c)

	requests, err := h.friendService.ListPendingRequests(c.Request.Context(), userID, offset, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"requests": requests, "offset": offset, "limit": limit})
}

// parsePagination extracts offset and limit from query parameters.
// Defaults: offset=0, limit=50. Max limit: 100.
func parsePagination(c *gin.Context) (int, int) {
	offset := 0
	limit := 50

	if o := c.Query("offset"); o != "" {
		if n, err := strconv.Atoi(o); err == nil && n >= 0 {
			offset = n
		}
	}
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	return offset, limit
}

// AcceptRequest handles PUT /api/v1/friends/requests/:id/accept
func (h *FriendHandler) AcceptRequest(c *gin.Context) {
	userID := c.GetString(middleware.UserIDKey)
	requestID := c.Param("id")

	f, err := h.friendService.AcceptRequest(c.Request.Context(), requestID, userID)
	if err != nil {
		c.JSON(errorStatus(err), gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, f)
}

// RejectRequest handles PUT /api/v1/friends/requests/:id/reject
func (h *FriendHandler) RejectRequest(c *gin.Context) {
	userID := c.GetString(middleware.UserIDKey)
	requestID := c.Param("id")

	if err := h.friendService.RejectRequest(c.Request.Context(), requestID, userID); err != nil {
		c.JSON(errorStatus(err), gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "request rejected"})
}

// RemoveFriend handles DELETE /api/v1/friends/:id
func (h *FriendHandler) RemoveFriend(c *gin.Context) {
	userID := c.GetString(middleware.UserIDKey)
	friendshipID := c.Param("id")

	if err := h.friendService.RemoveFriend(c.Request.Context(), friendshipID, userID); err != nil {
		c.JSON(errorStatus(err), gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "friend removed"})
}
