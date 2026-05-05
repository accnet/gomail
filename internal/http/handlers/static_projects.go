package handlers

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gomail/internal/db"
	mw "gomail/internal/http/middleware"
	"gomail/internal/staticprojects"
	"gomail/internal/teams"
	"gomail/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// StaticProjectsHandler handles static project API endpoints.
type StaticProjectsHandler struct {
	Service *staticprojects.Service
}

// NewStaticProjectsHandler creates a new handler.
func NewStaticProjectsHandler(svc *staticprojects.Service) *StaticProjectsHandler {
	return &StaticProjectsHandler{Service: svc}
}

// List returns all static projects for the current user.
func (h *StaticProjectsHandler) List(c *gin.Context) {
	ctx := teams.FromGin(c)
	oc := staticprojects.OwnerContext{UserID: ctx.UserID, TeamID: ctx.TeamID}
	projects, err := h.Service.List(oc)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "list_failed", "could not list projects")
		return
	}
	response.OK(c, projects)
}

// Get returns a single static project.
func (h *StaticProjectsHandler) Get(c *gin.Context) {
	ctx := teams.FromGin(c)
	projectID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid_id", "invalid project id")
		return
	}
	oc := staticprojects.OwnerContext{UserID: ctx.UserID, TeamID: ctx.TeamID}
	project, err := h.Service.Get(oc, projectID)
	if err != nil {
		if errors.Is(err, staticprojects.ErrNotFound) {
			response.Error(c, http.StatusNotFound, "not_found", "project not found")
			return
		}
		response.Error(c, http.StatusInternalServerError, "get_failed", "could not get project")
		return
	}
	response.OK(c, project)
}

// Thumbnail serves a generated project thumbnail without exposing the data directory.
func (h *StaticProjectsHandler) Thumbnail(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}

	var project db.StaticProject
	if err := h.Service.DB.First(&project, "id = ? AND deleted_at IS NULL", projectID).Error; err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	if project.ThumbnailStatus != "ready" {
		c.Status(http.StatusNotFound)
		return
	}

	thumbnailPath := project.ThumbnailPath
	if thumbnailPath == "" {
		thumbnailPath = filepath.Join(project.RootFolder, "thumbnail.png")
	}
	if thumbnailPath == "" {
		c.Status(http.StatusNotFound)
		return
	}

	rootAbs, err := filepath.Abs(h.Service.Config.StaticSitesRoot)
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	thumbAbs, err := filepath.Abs(thumbnailPath)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	rootPrefix := filepath.Clean(rootAbs) + string(os.PathSeparator)
	if thumbAbs != filepath.Clean(rootAbs) && !strings.HasPrefix(thumbAbs, rootPrefix) {
		c.Status(http.StatusNotFound)
		return
	}
	if _, err := os.Stat(thumbAbs); err != nil {
		c.Status(http.StatusNotFound)
		return
	}

	c.Header("Cache-Control", "public, max-age=300")
	c.File(thumbAbs)
}

// Deploy creates a new static project from an uploaded ZIP.
func (h *StaticProjectsHandler) Deploy(c *gin.Context) {
	user := mw.CurrentUser(c)

	name := c.PostForm("name")
	if name == "" {
		name = "My Website"
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		response.Error(c, http.StatusBadRequest, "missing_file", "file is required")
		return
	}
	defer file.Close()

	project, err := h.Service.DeployStream(c.Request.Context(), user.ID, name, file, header.Filename)
	if err != nil {
		switch {
		case errors.Is(err, staticprojects.ErrQuotaExceeded):
			response.Error(c, http.StatusForbidden, "website_quota_exceeded", "website quota exceeded")
		case errors.Is(err, staticprojects.ErrInvalidArchive):
			response.Error(c, http.StatusBadRequest, "invalid_archive", err.Error())
		case errors.Is(err, staticprojects.ErrPublishRootNotFound):
			response.Error(c, http.StatusBadRequest, "publish_root_not_found", "no index.html found in archive")
		case errors.Is(err, staticprojects.ErrMultiplePublishRoot):
			response.Error(c, http.StatusBadRequest, "multiple_publish_roots", "multiple directories contain index.html")
		case errors.Is(err, staticprojects.ErrForbiddenFileType):
			response.Error(c, http.StatusBadRequest, "forbidden_file_type", err.Error())
		case errors.Is(err, staticprojects.ErrPublishFailed):
			response.Error(c, http.StatusInternalServerError, "publish_failed", "publishing failed")
		default:
			response.Error(c, http.StatusInternalServerError, "deploy_failed", "could not deploy project")
		}
		return
	}
	if h.Service.AuditLogger != nil {
		h.Service.AuditLogger.LogDeploy(user.ID, project.ID, project.Name)
	}
	response.Created(c, project)
}

// Redeploy re-uploads and re-publishes an existing project.
func (h *StaticProjectsHandler) Redeploy(c *gin.Context) {
	user := mw.CurrentUser(c)
	projectID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid_id", "invalid project id")
		return
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		response.Error(c, http.StatusBadRequest, "missing_file", "file is required")
		return
	}
	defer file.Close()

	project, err := h.Service.RedeployStream(c.Request.Context(), user.ID, projectID, file, header.Filename)
	if err != nil {
		switch {
		case errors.Is(err, staticprojects.ErrNotFound):
			response.Error(c, http.StatusNotFound, "not_found", "project not found")
		case errors.Is(err, staticprojects.ErrInvalidArchive):
			response.Error(c, http.StatusBadRequest, "invalid_archive", err.Error())
		case errors.Is(err, staticprojects.ErrPublishRootNotFound):
			response.Error(c, http.StatusBadRequest, "publish_root_not_found", "no index.html found in archive")
		case errors.Is(err, staticprojects.ErrMultiplePublishRoot):
			response.Error(c, http.StatusBadRequest, "multiple_publish_roots", "multiple directories contain index.html")
		case errors.Is(err, staticprojects.ErrForbiddenFileType):
			response.Error(c, http.StatusBadRequest, "forbidden_file_type", err.Error())
		case errors.Is(err, staticprojects.ErrPublishFailed):
			response.Error(c, http.StatusInternalServerError, "publish_failed", "publishing failed")
		default:
			response.Error(c, http.StatusInternalServerError, "redeploy_failed", "could not redeploy project")
		}
		return
	}
	if h.Service.AuditLogger != nil {
		h.Service.AuditLogger.LogRedeploy(user.ID, project.ID, project.Name)
	}
	response.OK(c, project)
}

// ToggleStatus enables or disables a project.
func (h *StaticProjectsHandler) ToggleStatus(c *gin.Context) {
	ctx := teams.FromGin(c)
	projectID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid_id", "invalid project id")
		return
	}

	var req struct {
		IsActive bool `json:"is_active"`
	}
	if !bind(c, &req) {
		return
	}

	oc := staticprojects.OwnerContext{UserID: ctx.UserID, TeamID: ctx.TeamID}
	project, err := h.Service.ToggleStatus(oc, projectID, req.IsActive)
	if err != nil {
		if errors.Is(err, staticprojects.ErrNotFound) {
			response.Error(c, http.StatusNotFound, "not_found", "project not found")
			return
		}
		response.Error(c, http.StatusInternalServerError, "status_failed", "could not update status")
		return
	}
	if h.Service.AuditLogger != nil {
		h.Service.AuditLogger.LogToggleStatus(ctx.UserID, project.ID, project.IsActive)
	}
	response.OK(c, project)
}

// Delete removes a static project.
func (h *StaticProjectsHandler) Delete(c *gin.Context) {
	ctx := teams.FromGin(c)
	projectID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid_id", "invalid project id")
		return
	}

	oc := staticprojects.OwnerContext{UserID: ctx.UserID, TeamID: ctx.TeamID}
	if err := h.Service.Delete(oc, projectID); err != nil {
		if errors.Is(err, staticprojects.ErrNotFound) {
			response.Error(c, http.StatusNotFound, "not_found", "project not found")
			return
		}
		response.Error(c, http.StatusInternalServerError, "delete_failed", "could not delete project")
		return
	}
	if h.Service.AuditLogger != nil {
		h.Service.AuditLogger.LogDelete(ctx.UserID, projectID, "")
	}
	response.OK(c, gin.H{"ok": true})
}

// AvailableDomains returns domains that can be assigned to a project.
func (h *StaticProjectsHandler) AvailableDomains(c *gin.Context) {
	user := mw.CurrentUser(c)
	domains, err := h.Service.AvailableDomains(user.ID)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "domains_failed", "could not list available domains")
		return
	}
	response.OK(c, domains)
}

// AssignDomain assigns a domain to a project.
func (h *StaticProjectsHandler) AssignDomain(c *gin.Context) {
	user := mw.CurrentUser(c)
	projectID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid_id", "invalid project id")
		return
	}

	var req struct {
		DomainID uuid.UUID `json:"domain_id"`
	}
	if !bind(c, &req) {
		return
	}

	project, err := h.Service.AssignDomain(user.ID, projectID, req.DomainID)
	if err != nil {
		switch {
		case errors.Is(err, staticprojects.ErrNotFound):
			response.Error(c, http.StatusNotFound, "not_found", "project not found")
		case errors.Is(err, staticprojects.ErrDomainNotVerified):
			response.Error(c, http.StatusBadRequest, "domain_not_verified", "domain must be verified and owned by you")
		case errors.Is(err, staticprojects.ErrDomainAlreadyBound):
			response.Error(c, http.StatusConflict, "domain_already_bound", "domain is already bound to another project")
		case errors.Is(err, staticprojects.ErrDomainReserved):
			response.Error(c, http.StatusBadRequest, "domain_reserved", "SaaS domain cannot be assigned to a website")
		default:
			response.Error(c, http.StatusInternalServerError, "assign_failed", "could not assign domain")
		}
		return
	}
	if h.Service.AuditLogger != nil {
		h.Service.AuditLogger.LogDomainAssign(user.ID, project.ID, project.AssignedDomain)
	}
	response.OK(c, project)
}

// UnassignDomain removes domain binding from a project.
func (h *StaticProjectsHandler) UnassignDomain(c *gin.Context) {
	user := mw.CurrentUser(c)
	projectID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid_id", "invalid project id")
		return
	}

	project, err := h.Service.UnassignDomain(user.ID, projectID)
	if err != nil {
		if errors.Is(err, staticprojects.ErrNotFound) {
			response.Error(c, http.StatusNotFound, "not_found", "project not found")
			return
		}
		response.Error(c, http.StatusInternalServerError, "unassign_failed", "could not unassign domain")
		return
	}
	if h.Service.AuditLogger != nil {
		h.Service.AuditLogger.LogDomainUnassign(user.ID, project.ID, "")
	}
	response.OK(c, project)
}

// CheckDomainIP checks DNS resolution for the assigned domain.
func (h *StaticProjectsHandler) CheckDomainIP(c *gin.Context) {
	user := mw.CurrentUser(c)
	projectID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid_id", "invalid project id")
		return
	}

	ok, msg, err := h.Service.CheckDomainIP(projectID, user.ID)
	if err != nil {
		if errors.Is(err, staticprojects.ErrNotFound) {
			response.Error(c, http.StatusNotFound, "not_found", "project not found")
			return
		}
		response.Error(c, http.StatusInternalServerError, "check_failed", "could not check domain IP")
		return
	}
	response.OK(c, gin.H{"ok": ok, "message": msg})
}

// ActiveSSL activates SSL for the custom domain.
func (h *StaticProjectsHandler) ActiveSSL(c *gin.Context) {
	user := mw.CurrentUser(c)
	projectID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid_id", "invalid project id")
		return
	}

	project, err := h.Service.ActiveSSL(user.ID, projectID)
	if err != nil {
		switch {
		case errors.Is(err, staticprojects.ErrNotFound):
			response.Error(c, http.StatusNotFound, "not_found", "project not found")
		case errors.Is(err, staticprojects.ErrDomainNotAvailable):
			response.Error(c, http.StatusBadRequest, "domain_not_available", "no domain assigned")
		case errors.Is(err, staticprojects.ErrSSLConditionNotMet):
			response.Error(c, http.StatusBadRequest, "ssl_condition_not_met", "DNS check must pass first")
		default:
			response.Error(c, http.StatusInternalServerError, "ssl_failed", "could not activate SSL")
		}
		return
	}
	if h.Service.AuditLogger != nil {
		h.Service.AuditLogger.LogActiveSSL(user.ID, project.ID, project.AssignedDomain)
	}
	response.OK(c, project)
}

// WireStaticProjectRoutes adds static project routes to the router.
func WireStaticProjectRoutes(protected *gin.RouterGroup, handler *StaticProjectsHandler) {
	sp := protected.Group("/static-projects")
	sp.GET("", mw.RequireTeamScope(teams.ScopeWebsiteRead), handler.List)
	sp.GET("/:id", mw.RequireTeamScope(teams.ScopeWebsiteRead), handler.Get)
	sp.POST("/deploy", mw.RequireTeamScope(teams.ScopeWebsiteDeploy), handler.Deploy)
	sp.POST("/:id/redeploy", mw.RequireTeamScope(teams.ScopeWebsiteDeploy), handler.Redeploy)
	sp.PATCH("/:id/status", mw.RequireTeamScope(teams.ScopeWebsiteManage), handler.ToggleStatus)
	sp.DELETE("/:id", mw.RequireTeamScope(teams.ScopeWebsiteManage), handler.Delete)
	sp.GET("/:id/available-domains", mw.RequireTeamScope(teams.ScopeWebsiteManage), handler.AvailableDomains)
	sp.PATCH("/:id/domain", mw.RequireTeamScope(teams.ScopeWebsiteManage), handler.AssignDomain)
	sp.DELETE("/:id/domain", mw.RequireTeamScope(teams.ScopeWebsiteManage), handler.UnassignDomain)
	sp.POST("/:id/domain/check-ip", mw.RequireTeamScope(teams.ScopeWebsiteManage), handler.CheckDomainIP)
	sp.POST("/:id/domain/active-ssl", mw.RequireTeamScope(teams.ScopeWebsiteManage), handler.ActiveSSL)
}
