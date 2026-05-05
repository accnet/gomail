package handlers

import (
	"net/http"

	"gomail/internal/db"
	mw "gomail/internal/http/middleware"
	"gomail/pkg/response"

	"github.com/gin-gonic/gin"
)

func (a App) getGeneralSettings(c *gin.Context) {
	response.OK(c, gin.H{
		"saas_domain":      a.Config.SaaSDomain,
		"landing_root":     a.Config.LandingRoot,
		"saas_domain_mode": db.GetSaaSDomainMode(a.DB),
		"mx_target":        a.Config.MXTarget,
		"smtp_hostname":    a.Config.SMTPHostname,
	})
}

func (a App) patchGeneralSettings(c *gin.Context) {
	user := mw.CurrentUser(c)
	if !user.IsAdmin {
		response.Error(c, http.StatusForbidden, "forbidden", "admin required")
		return
	}

	var req struct {
		SaaSDomainMode string `json:"saas_domain_mode"`
	}
	if !bind(c, &req) {
		return
	}

	mode := db.NormalizeSaaSDomainMode(req.SaaSDomainMode)
	if err := db.SetSaaSDomainMode(a.DB, mode); err != nil {
		response.Error(c, http.StatusInternalServerError, "settings_failed", "could not update settings")
		return
	}
	response.OK(c, gin.H{
		"saas_domain":      a.Config.SaaSDomain,
		"landing_root":     a.Config.LandingRoot,
		"saas_domain_mode": mode,
		"mx_target":        a.Config.MXTarget,
		"smtp_hostname":    a.Config.SMTPHostname,
	})
}
