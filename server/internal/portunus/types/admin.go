package types

// ── Module admin types ──────────────────────────────────────────────────────

// ModuleStatus is the derived lifecycle state of a module, computed from the
// enabled, commissioned_at_ms, and revoked_at_ms columns.
//
//   discovered — seen by the server (auto-created on first heartbeat) but never
//                commissioned by an admin. The device is not trusted.
//   active     — commissioned, enabled, and not revoked. The device is trusted
//                and access decisions are made for its requests.
//   revoked    — explicitly revoked by an admin. The device is no longer trusted
//                regardless of the enabled flag.
type ModuleStatus string

const (
	ModuleStatusDiscovered ModuleStatus = "discovered"
	ModuleStatusActive     ModuleStatus = "active"
	ModuleStatusRevoked    ModuleStatus = "revoked"
)

type RegisterModuleRequest struct {
	ModuleID    string `json:"module_id"`
	DoorID      string `json:"door_id,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

type ModuleInfo struct {
	ModuleID       string       `json:"module_id"`
	DoorID         string       `json:"door_id,omitempty"`
	DisplayName    string       `json:"display_name,omitempty"`
	Status         ModuleStatus `json:"status"`
	Enabled        bool         `json:"enabled"`
	Commissioned   bool         `json:"commissioned"`
	CommissionedAt string       `json:"commissioned_at,omitempty"`
	RevokedAt      string       `json:"revoked_at,omitempty"`
	LastSeenAt     string       `json:"last_seen_at,omitempty"`
	LastIP         string       `json:"last_ip,omitempty"`
	LastFWVersion  string       `json:"last_fw_version,omitempty"`
	LastWiFiRSSI   *int         `json:"last_wifi_rssi,omitempty"`
	CreatedAt      string       `json:"created_at"`
}

type ListModulesResponse struct {
	OK      bool         `json:"ok"`
	Modules []ModuleInfo `json:"modules"`
}

// ── Credential admin types ──────────────────────────────────────────────────

type RegisterCredentialRequest struct {
	CredentialID string `json:"credential_id"` // raw hex UID (e.g. "04:A3:2B:1C")
	Tag          string `json:"tag,omitempty"` // human-readable label
}

type CredentialInfo struct {
	CredentialHash string `json:"credential_hash"` // hex-encoded SHA-256
	Tag            string `json:"tag,omitempty"`
	Status         string `json:"status"` // "active", "disabled", "lost"
	CreatedAt      string `json:"created_at"`
	LastSeenAt     string `json:"last_seen_at,omitempty"`
}

type ListCredentialsResponse struct {
	OK          bool             `json:"ok"`
	Credentials []CredentialInfo `json:"credentials"`
}

// ── Door admin types ────────────────────────────────────────────────────────

type RegisterDoorRequest struct {
	DoorID   string `json:"door_id"`
	Name     string `json:"name"`
	Location string `json:"location,omitempty"`
}

type DoorInfo struct {
	DoorID    string `json:"door_id"`
	Name      string `json:"name"`
	Location  string `json:"location,omitempty"`
	CreatedAt string `json:"created_at"`
}

type ListDoorsResponse struct {
	OK    bool       `json:"ok"`
	Doors []DoorInfo `json:"doors"`
}
