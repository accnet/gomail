package staticprojects

import (
	"gomail/internal/db"

	"github.com/google/uuid"
)

// UIState represents the computed UI state for a project.
type UIState string

const (
	UIStateDeploying UIState = "deploying"
	UIStateLive      UIState = "live"
	UIStateFailed    UIState = "failed"
	UIStateDisabled  UIState = "disabled"
)

// ProjectResponse is the API response for a static project.
type ProjectResponse struct {
	db.StaticProject
	UIState      UIState `json:"ui_state"`
	WebsitesUsed int     `json:"websites_used"`
	MaxWebsites  int     `json:"max_websites"`
}

// ComputeUIState computes the UI state from the project's raw fields.
func ComputeUIState(p *db.StaticProject) UIState {
	if !p.IsActive {
		return UIStateDisabled
	}
	switch p.Status {
	case "draft", "deploying":
		return UIStateDeploying
	case "published":
		return UIStateLive
	case "publish_failed":
		return UIStateFailed
	default:
		return UIStateFailed
	}
}

// toProjectResponse builds a ProjectResponse from a project and quota info.
func toProjectResponse(project *db.StaticProject, used, max int) *ProjectResponse {
	return &ProjectResponse{
		StaticProject: *project,
		UIState:       ComputeUIState(project),
		WebsitesUsed:  used,
		MaxWebsites:   max,
	}
}

// reloadAndRespond reloads the project from DB and builds a response with quota.
func (s *Service) reloadAndRespond(projectID, userID uuid.UUID) (*ProjectResponse, error) {
	var project db.StaticProject
	if err := s.DB.First(&project, "id = ?", projectID).Error; err != nil {
		return nil, err
	}
	used, max, _ := s.QuotaInfo(userID)
	return toProjectResponse(&project, used, max), nil
}