package types

// ProvisionCredentialRequest is the domain type for device-initiated provisioning.
// CredentialUID carries the scan-2 raw RFID UID bytes (new member card).
// OperatorCredentialUID carries the scan-1 raw RFID UID bytes (operator badge).
// When OperatorCredentialUID is non-empty the server resolves it to an admin user
// and ignores OperatorUUID. OperatorUUID is the legacy fallback for firmware that
// does not send scan-1 UID.
type ProvisionCredentialRequest struct {
	OperatorUUID          string `json:"operator_uuid"`
	OperatorCredentialUID []byte `json:"operator_credential_uid"` // scan-1 raw UID (1–10 bytes)
	ModuleID              string `json:"module_id"`
	CredentialUID         []byte `json:"credential_uid"` // scan-2 raw UID (1–10 bytes)
	RoleID                string `json:"role_id"`
}

// ProvisionStatus represents the outcome of a device-initiated provisioning request.
type ProvisionStatus string

const (
	ProvisionStatusSuccess           ProvisionStatus = "success"
	ProvisionStatusDuplicateActive   ProvisionStatus = "duplicate_active"
	ProvisionStatusDuplicateInactive ProvisionStatus = "duplicate_inactive"
	ProvisionStatusDuplicatePending  ProvisionStatus = "duplicate_pending"
	ProvisionStatusUnauthorized      ProvisionStatus = "unauthorized"
	ProvisionStatusInvalidRole       ProvisionStatus = "invalid_role"
)

// ProvisionCredentialResponse is the domain response for device-initiated provisioning.
type ProvisionCredentialResponse struct {
	OK         bool            `json:"ok"`
	Known      bool            `json:"known"`
	MemberUUID string          `json:"member_uuid,omitempty"`
	Status     ProvisionStatus `json:"status,omitempty"`
	Detail     string          `json:"detail,omitempty"`
}
