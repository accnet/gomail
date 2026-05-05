package middleware

import (
	"net/http"

	"gomail/internal/teams"
	"gomail/pkg/response"

	"github.com/gin-gonic/gin"
)

// RequireTeamContext validates the X-Team-Id header and sets the
// teams.ActiveContext in the gin context. Must run after mw.Auth().
func RequireTeamContext(teamSvc *teams.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		user := CurrentUser(c)

		teamID, err := teams.TeamIDFromRequest(c)
		if err != nil {
			response.Error(c, http.StatusBadRequest, "invalid_team_id", "invalid team id format")
			c.Abort()
			return
		}

		// No team header → personal context
		if teamID == nil {
			teams.SetGin(c, teams.ActiveContext{
				UserID:   user.ID,
				Personal: true,
			})
			c.Next()
			return
		}

		// Team context: verify membership
		member, err := teamSvc.GetMember(c.Request.Context(), *teamID, user.ID)
		if err != nil {
			response.Error(c, http.StatusForbidden, "forbidden", "not a member of this team")
			c.Abort()
			return
		}

		scopes, _ := teams.ParseScopes(member.Permissions)
		// Owner always gets all scopes
		if member.Role == "owner" {
			scopes = teams.DefaultScopes("owner")
		}

		teams.SetGin(c, teams.ActiveContext{
			UserID: user.ID,
			TeamID: teamID,
			Role:   member.Role,
			Scopes: scopes,
		})
		c.Next()
	}
}
