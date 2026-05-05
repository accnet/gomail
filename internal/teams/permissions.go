package teams

import (
	"errors"
	"sort"

	"gomail/internal/db"

	"gorm.io/datatypes"
)

// ── Scopes ─────────────────────────────────────────────────────────────────

const (
	ScopeEmailAccess   = "email:access"
	ScopeEmailManage   = "email:manage"
	ScopeApiKeyRead    = "apikey:read"
	ScopeApiKeyCreate  = "apikey:create"
	ScopeApiKeyManage  = "apikey:manage"
	ScopeWebsiteRead   = "website:read"
	ScopeWebsiteDeploy = "website:deploy"
	ScopeWebsiteManage = "website:manage"
	ScopeDomainManage  = "domain:manage"
	ScopeMemberManage  = "member:manage"
	ScopeTeamDelete    = "team:delete"
)

var allScopes = []string{
	ScopeEmailAccess, ScopeEmailManage,
	ScopeApiKeyRead, ScopeApiKeyCreate, ScopeApiKeyManage,
	ScopeWebsiteRead, ScopeWebsiteDeploy, ScopeWebsiteManage,
	ScopeDomainManage, ScopeMemberManage, ScopeTeamDelete,
}

var defaultOwnerScopes = allScopes

var defaultAdminScopes = []string{
	ScopeEmailAccess, ScopeEmailManage,
	ScopeApiKeyRead, ScopeApiKeyCreate, ScopeApiKeyManage,
	ScopeWebsiteRead, ScopeWebsiteDeploy, ScopeWebsiteManage,
	ScopeDomainManage, ScopeMemberManage,
}

var defaultMemberScopes = []string{
	ScopeEmailAccess,
	ScopeApiKeyRead,
	ScopeWebsiteRead,
}

// ── Helpers ────────────────────────────────────────────────────────────────

func DefaultScopes(role string) []string {
	sort.Strings(defaultOwnerScopes)
	switch role {
	case db.TeamRoleOwner:
		s := make([]string, len(defaultOwnerScopes))
		copy(s, defaultOwnerScopes)
		return s
	case db.TeamRoleAdmin:
		s := make([]string, len(defaultAdminScopes))
		copy(s, defaultAdminScopes)
		return s
	default:
		s := make([]string, len(defaultMemberScopes))
		copy(s, defaultMemberScopes)
		return s
	}
}

var validRoles map[string]bool

func init() {
	validRoles = map[string]bool{
		db.TeamRoleOwner:  true,
		db.TeamRoleAdmin:  true,
		db.TeamRoleMember: true,
	}
}

func ValidateRole(role string, allowOwner bool) error {
	if !validRoles[role] {
		return errors.New("invalid role: must be owner, admin, or member")
	}
	if !allowOwner && role == db.TeamRoleOwner {
		return errors.New("owner role is not allowed in this context")
	}
	return nil
}

func ValidateScopes(scopes []string) error {
	validScope := make(map[string]bool, len(allScopes))
	for _, s := range allScopes {
		validScope[s] = true
	}
	for _, s := range scopes {
		if !validScope[s] {
			return errors.New("invalid scope: " + s)
		}
	}
	return nil
}

func MarshalScopes(scopes []string) datatypes.JSON {
	if scopes == nil {
		scopes = []string{}
	}
	// Using simple JSON string array
	b := []byte("[")
	for i, s := range scopes {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, '"')
		b = append(b, []byte(s)...)
		b = append(b, '"')
	}
	b = append(b, ']')
	return datatypes.JSON(b)
}

func ParseScopes(raw datatypes.JSON) ([]string, error) {
	if len(raw) == 0 {
		return []string{}, nil
	}
	// Simple parser for JSON string array
	s := string(raw)
	if s == "[]" || s == "null" {
		return []string{}, nil
	}
	// Strip brackets
	s = s[1 : len(s)-1]
	if s == "" {
		return []string{}, nil
	}
	var scopes []string
	inQuote := false
	current := ""
	for _, ch := range s {
		if ch == '"' {
			inQuote = !inQuote
			continue
		}
		if inQuote {
			current += string(ch)
		} else if ch == ',' {
			scopes = append(scopes, current)
			current = ""
		}
	}
	if current != "" {
		scopes = append(scopes, current)
	}
	return scopes, nil
}

func MemberHasScope(m db.TeamMember, scope string) bool {
	if m.Role == db.TeamRoleOwner {
		return true // owner always has all scopes
	}
	scopes, err := ParseScopes(m.Permissions)
	if err != nil {
		return false
	}
	for _, s := range scopes {
		if s == scope {
			return true
		}
	}
	return false
}

func MemberHasAnyScope(m db.TeamMember, scopes ...string) bool {
	for _, scope := range scopes {
		if MemberHasScope(m, scope) {
			return true
		}
	}
	return false
}
