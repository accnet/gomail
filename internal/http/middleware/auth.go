package middleware

import (
	"net/http"
	"strings"

	"gomail/internal/auth"
	"gomail/internal/db"
	"gomail/pkg/response"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const UserKey = "user"

func Auth(authSvc *auth.Service, database *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		token := strings.TrimPrefix(header, "Bearer ")
		if token == "" || token == header {
			token = c.Query("token")
			if token == "" {
				response.Error(c, http.StatusUnauthorized, "unauthorized", "missing bearer token")
				return
			}
		}
		claims, err := authSvc.ParseAccessToken(token)
		if err != nil {
			response.Error(c, http.StatusUnauthorized, "unauthorized", "invalid token")
			return
		}
		var user db.User
		if err := database.First(&user, "id = ? AND is_active = ?", claims.UserID, true).Error; err != nil {
			response.Error(c, http.StatusUnauthorized, "unauthorized", "inactive or missing user")
			return
		}
		c.Set(UserKey, user)
		c.Next()
	}
}

func CurrentUser(c *gin.Context) db.User {
	user, _ := c.Get(UserKey)
	return user.(db.User)
}

func Admin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !CurrentUser(c).IsAdmin {
			response.Error(c, http.StatusForbidden, "forbidden", "admin required")
			return
		}
		c.Next()
	}
}

func SuperAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !CurrentUser(c).IsSuperAdmin {
			response.Error(c, http.StatusForbidden, "forbidden", "super admin required")
			return
		}
		c.Next()
	}
}
