package model

// --- Auth ---

type RegisterRequest struct {
	Username string `json:"username" binding:"required,min=3,max=50"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6,max=100"`
}

type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"` // seconds
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// --- User ---

type UpdateProfileRequest struct {
	Nickname  *string `json:"nickname,omitempty" binding:"omitnil,min=1,max=100"`
	AvatarURL *string `json:"avatar_url,omitempty" binding:"omitnil,min=1,max=2048"`
}

// --- Friend ---
// (friend request actions use URL path segments: /requests/:id/accept, /requests/:id/reject)
