package pbconvert

import (
	pb "github.com/BrandonDHaskell/Portunus/server/api/portunus/v1"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

// HeartbeatRequestFromProto maps a device heartbeat to the domain type. Both
// transports call it, so a new heartbeat field is mapped once for both.
// Optional proto fields stay nil when the device omitted them.
func HeartbeatRequestFromProto(p *pb.HeartbeatRequest) types.HeartbeatRequest {
	req := types.HeartbeatRequest{
		ModuleID:        p.GetModuleId(),
		FirmwareVersion: p.GetFirmwareVersion(),
		UptimeSeconds:   p.GetUptimeS(),
		IP:              p.GetIp(),
		FreeHeapBytes:   p.GetFreeHeapBytes(),
		Sequence:        p.GetSequence(),
		DroppedEvents:   p.GetDroppedEvents(),
	}
	if p.DoorClosed != nil {
		v := p.GetDoorClosed()
		req.DoorClosed = &v
	}
	if p.RssiDbm != nil {
		v := int(p.GetRssiDbm())
		req.RSSIDbm = &v
	}
	if p.ReaderOk != nil {
		v := p.GetReaderOk()
		req.ReaderOK = &v
	}
	if p.StrikeOk != nil {
		v := p.GetStrikeOk()
		req.StrikeOK = &v
	}
	return req
}
