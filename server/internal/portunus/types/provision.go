package types

// ProvisionCredentialRequest is the domain type for device-initiated provisioning.
// CredentialUID carries the raw RFID UID bytes from the device; the server applies
// HMAC-SHA256(secret, CredentialUID) before storing in member_access.credential_hash.
type ProvisionCredentialRequest struct {
	OperatorUUID  string `json:"operator_uuid"`
	ModuleID      string `json:"module_id"`
	CredentialUID []byte `json:"credential_uid"` // raw RFID UID bytes (1–10 bytes)
	RoleID        string `json:"role_id"`
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
