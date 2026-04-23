package types

// ProvisionCredentialRequest is the domain type for device-initiated provisioning.
// The credential_hash carries the SHA-256 digest computed on-device via mbedTLS;
// the raw UID bytes never leave the device.
type ProvisionCredentialRequest struct {
	OperatorUUID   string `json:"operator_uuid"`
	ModuleID       string `json:"module_id"`
	CredentialHash []byte `json:"credential_hash"` // 32-byte SHA-256, pre-computed on device
	RoleID         string `json:"role_id"`
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
