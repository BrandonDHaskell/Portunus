package types

// ── Member provisioning types ────────────────────────────────────────────────

type ProvisionMemberRequest struct {
	RoleID              string `json:"role_id"`
	CreatedByUUID       string `json:"created_by_uuid,omitempty"`
	ExpiresAt           string `json:"expires_at,omitempty"`            // RFC 3339; omit for no hard deadline
	InactivityLimitDays *int   `json:"inactivity_limit_days,omitempty"` // nil = no inactivity policy
}

type AttachCredentialRequest struct {
	CredentialHashHex string `json:"credential_hash"` // hex-encoded 32-byte SHA-256
}

type AssignRoleRequest struct {
	RoleID string `json:"role_id"`
}

// MemberInfo is the JSON representation of a member_access row.
type MemberInfo struct {
	UUID                string `json:"uuid"`
	RoleID              string `json:"role_id"`
	CredentialHash      string `json:"credential_hash,omitempty"` // hex prefix — full hash not exposed
	Status              string `json:"status"`
	Enabled             bool   `json:"enabled"`
	ExpiresAt           string `json:"expires_at,omitempty"`
	InactivityLimitDays *int   `json:"inactivity_limit_days,omitempty"`
	LastAccessAt        string `json:"last_access_at,omitempty"`
	ProvisioningStatus  string `json:"provisioning_status"`
	CreatedAt           string `json:"created_at"`
	CreatedByUUID       string `json:"created_by_uuid,omitempty"`
	ArchivedAt          string `json:"archived_at,omitempty"`
	ArchivedByUUID      string `json:"archived_by_uuid,omitempty"`
}

type ListMembersResponse struct {
	OK      bool         `json:"ok"`
	Members []MemberInfo `json:"members"`
}

type ListPendingAuthorizationsResponse struct {
	OK      bool         `json:"ok"`
	Members []MemberInfo `json:"members"`
}

// ── Module authorization types ───────────────────────────────────────────────

type GrantAuthorizationRequest struct {
	MemberUUID      string `json:"member_uuid"`
	GrantedByUUID   string `json:"granted_by_uuid,omitempty"`
	ExpiresAt       string `json:"expires_at,omitempty"`       // RFC 3339; omit for no expiry
	TimeRestriction string `json:"time_restriction,omitempty"` // JSON policy; omit for none
}

type ModuleAuthorizationInfo struct {
	AuthorizationID int64  `json:"authorization_id"`
	MemberUUID      string `json:"member_uuid"`
	ModuleID        string `json:"module_id"`
	GrantedAt       string `json:"granted_at"`
	GrantedByUUID   string `json:"granted_by_uuid,omitempty"`
	ExpiresAt       string `json:"expires_at,omitempty"`
	RevokedAt       string `json:"revoked_at,omitempty"`
	RevokedByUUID   string `json:"revoked_by_uuid,omitempty"`
	TimeRestriction string `json:"time_restriction,omitempty"`
}

type ListModuleAuthorizationsResponse struct {
	OK             bool                      `json:"ok"`
	Authorizations []ModuleAuthorizationInfo `json:"authorizations"`
}
