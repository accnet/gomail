package teams

import (
	"gomail/internal/db"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ── ActiveContext ──────────────────────────────────────────────────────────

type ActiveContext struct {
	UserID   uuid.UUID  `json:"user_id"`
	TeamID   *uuid.UUID `json:"team_id,omitempty"` // nil = personal
	Role     string     `json:"role,omitempty"`
	Scopes   []string   `json:"scopes,omitempty"`
	Personal bool       `json:"personal"`
}

const ginContextKey = "teams_active_context"

// FromGin reads the active context from a gin Context.
// Falls back to the JWT user if middleware hasn't set the active context.
func FromGin(c *gin.Context) ActiveContext {
	if v, ok := c.Get(ginContextKey); ok {
		return v.(ActiveContext)
	}
	// Fallback: try to get user from auth middleware (tests or direct user set)
	if u, ok := c.Get("user"); ok {
		if user, ok2 := u.(db.User); ok2 {
			return ActiveContext{UserID: user.ID, Personal: true}
		}
	}
	return ActiveContext{Personal: true}
}

// SetGin stores the active context in a gin Context.
func SetGin(c *gin.Context, ctx ActiveContext) {
	c.Set(ginContextKey, ctx)
}

// TeamIDFromRequest extracts and validates a team ID from X-Team-Id header.
// Returns nil if header is empty (personal context).
func TeamIDFromRequest(c *gin.Context) (*uuid.UUID, error) {
	raw := c.GetHeader("X-Team-Id")
	if raw == "" {
		return nil, nil // personal context
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, err
	}
	return &id, nil
}
