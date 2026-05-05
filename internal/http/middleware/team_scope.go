package middleware

import (
	"net/http"

	"gomail/internal/teams"

	"github.com/gin-gonic/gin"
)

// RequireTeamScope returns a middleware that checks the active context
// has the required scope. In personal context, it always passes (ownership
// checks are done in handlers). In team context, the user must have the
// specified scope.
func RequireTeamScope(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := teams.FromGin(c)

		// Personal context: always allowed (ownership checks in handler)
		if ctx.Personal {
			c.Next()
			return
		}

		// Team context: check scope
		for _, s := range ctx.Scopes {
			if s == scope {
				c.Next()
				return
			}
		}

		c.JSON(http.StatusForbidden, gin.H{
			"error":          "missing_scope",
			"message":        "you do not have the required permission for this action",
			"required_scope": scope,
		})
		c.Abort()
	}
}
