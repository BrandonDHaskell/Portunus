package types

// ProvisionCredentialRequest is the domain type for device-initiated provisioning.
// CredentialUID carries the raw RFID UID bytes of the new member's card.
// Only the capture path is supported: PEU scan → pending_authorization record;
// an admin approves it via the console.
type ProvisionCredentialRequest struct {
	ModuleID      string `json:"module_id"`
	CredentialUID []byte `json:"credential_uid"` // raw UID (1–10 bytes)
}

// ProvisionStatus represents the outcome of a device-initiated provisioning request.
type ProvisionStatus string

const (
	ProvisionStatusPendingCreated    ProvisionStatus = "pending_created"
	ProvisionStatusDuplicateActive   ProvisionStatus = "duplicate_active"
	ProvisionStatusDuplicateInactive ProvisionStatus = "duplicate_inactive"
	ProvisionStatusDuplicatePending  ProvisionStatus = "duplicate_pending"
	ProvisionStatusUnauthorized      ProvisionStatus = "unauthorized"
)

// ProvisionCredentialResponse is the domain response for device-initiated provisioning.
type ProvisionCredentialResponse struct {
	OK         bool            `json:"ok"`
	Known      bool            `json:"known"`
	MemberUUID string          `json:"member_uuid,omitempty"`
	Status     ProvisionStatus `json:"status,omitempty"`
	Detail     string          `json:"detail,omitempty"`
}
