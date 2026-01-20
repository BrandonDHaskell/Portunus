package types

type HeartbeatRequest struct {
	ModuleID        string `json:"module_id"`
	FirmwareVersion string `json:"firmware_version,omitempty"`
	UptimeSeconds   uint64 `json:"uptime_s,omitempty"`
	DoorClosed      *bool  `json:"door_closed,omitempty"`
	RSSIDbm         *int   `json:"rssi_dbm,omitempty"`
	IP              string `json:"ip,omitempty"`
}

type HeartbeatResponse struct {
	OK         bool   `json:"ok"`
	Known      bool   `json:"known"`
	ModuleID   string `json:"module_id"`
	ServerTime string `json:"server_time"`
}
