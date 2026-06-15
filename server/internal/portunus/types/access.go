package types

type AccessRequest struct {
	ModuleID     string `json:"module_id"`
	CredentialID string `json:"credential_id"`
	DoorClosed   *bool  `json:"door_closed,omitempty"`
	RequestedAt  string `json:"requested_at,omitempty"` // optional device timestamp (RFC 3339 UTC)
	Nonce        []byte `json:"nonce,omitempty"`        // 16 random bytes for replay protection
}

type AccessResponse struct {
	OK         bool   `json:"ok"`
	Known      bool   `json:"known"`
	Granted    bool   `json:"granted"`
	Reason     string `json:"reason,omitempty"`
	ModuleID   string `json:"module_id"`
	ServerTime string `json:"server_time"`
}
