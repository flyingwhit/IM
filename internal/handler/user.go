package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/ciel/im/internal/middleware"
	"github.com/ciel/im/internal/model"
	"github.com/ciel/im/internal/repository/postgres"
)

// UserHandler handles user profile HTTP endpoints.
type UserHandler struct {
	userRepo *postgres.UserRepo
}

func NewUserHandler(userRepo *postgres.UserRepo) *UserHandler {
	return &UserHandler{userRepo: userRepo}
}

// GetProfile handles GET /api/v1/users/me
func (h *UserHandler) GetProfile(c *gin.Context) {
	userID := c.GetString(middleware.UserIDKey)

	user, err := h.userRepo.FindByID(c.Request.Context(), userID)
	if err != nil {
		c.JSON(errorStatus(err), gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, user)
}

// UpdateProfile handles PUT /api/v1/users/me
func (h *UserHandler) UpdateProfile(c *gin.Context) {
	userID := c.GetString(middleware.UserIDKey)

	var req model.UpdateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	user, err := h.userRepo.FindByID(c.Request.Context(), userID)
	if err != nil {
		c.JSON(errorStatus(err), gin.H{"error": err.Error()})
		return
	}

	if req.Nickname != nil {
		user.Nickname = *req.Nickname
	}
	if req.AvatarURL != nil {
		user.AvatarURL = req.AvatarURL
	}

	if err := h.userRepo.Update(c.Request.Context(), user); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "update failed"})
		return
	}

	c.JSON(http.StatusOK, user)
}
