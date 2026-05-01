package staticprojects

import (
	"encoding/json"

	"gomail/internal/db"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// AuditLogger logs static project actions.
type AuditLogger struct {
	DB *gorm.DB
}

// NewAuditLogger creates a new AuditLogger.
func NewAuditLogger(database *gorm.DB) *AuditLogger {
	return &AuditLogger{DB: database}
}

// Log writes an audit entry.
func (l *AuditLogger) Log(actorID uuid.UUID, eventType string, data any) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	l.DB.Create(&db.AuditLog{
		ActorID: &actorID,
		Type:    eventType,
		Payload: payload,
	})
}

// LogDeploy logs a deploy event.
func (l *AuditLogger) LogDeploy(actorID uuid.UUID, projectID uuid.UUID, projectName string) {
	l.Log(actorID, "static_project.deploy", map[string]any{
		"project_id":   projectID,
		"project_name": projectName,
	})
}

// LogRedeploy logs a redeploy event.
func (l *AuditLogger) LogRedeploy(actorID uuid.UUID, projectID uuid.UUID, projectName string) {
	l.Log(actorID, "static_project.redeploy", map[string]any{
		"project_id":   projectID,
		"project_name": projectName,
	})
}

// LogDelete logs a delete event.
func (l *AuditLogger) LogDelete(actorID uuid.UUID, projectID uuid.UUID, projectName string) {
	l.Log(actorID, "static_project.delete", map[string]any{
		"project_id":   projectID,
		"project_name": projectName,
	})
}

// LogToggleStatus logs a toggle status event.
func (l *AuditLogger) LogToggleStatus(actorID uuid.UUID, projectID uuid.UUID, isActive bool) {
	l.Log(actorID, "static_project.toggle_status", map[string]any{
		"project_id": projectID,
		"is_active":  isActive,
	})
}

// LogDomainAssign logs a domain assign event.
func (l *AuditLogger) LogDomainAssign(actorID uuid.UUID, projectID uuid.UUID, domain string) {
	l.Log(actorID, "static_project.domain_assign", map[string]any{
		"project_id": projectID,
		"domain":     domain,
	})
}

// LogDomainUnassign logs a domain unassign event.
func (l *AuditLogger) LogDomainUnassign(actorID uuid.UUID, projectID uuid.UUID, domain string) {
	l.Log(actorID, "static_project.domain_unassign", map[string]any{
		"project_id": projectID,
		"domain":     domain,
	})
}

// LogActiveSSL logs an SSL activation event.
func (l *AuditLogger) LogActiveSSL(actorID uuid.UUID, projectID uuid.UUID, domain string) {
	l.Log(actorID, "static_project.ssl_active", map[string]any{
		"project_id": projectID,
		"domain":     domain,
	})
}
