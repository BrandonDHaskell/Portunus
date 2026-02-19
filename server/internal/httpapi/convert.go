package httpapi

import (
	pb "github.com/BrandonDHaskell/Portunus/server/api/portunus/v1"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

// ── Heartbeat ────────────────────────────────────────────────────────────────

func heartbeatRequestFromProto(p *pb.HeartbeatRequest) types.HeartbeatRequest {
	req := types.HeartbeatRequest{
		ModuleID:        p.GetModuleId(),
		FirmwareVersion: p.GetFirmwareVersion(),
		UptimeSeconds:   p.GetUptimeS(),
		IP:              p.GetIp(),
		FreeHeapBytes:   p.GetFreeHeapBytes(),
		Sequence:        p.GetSequence(),
	}

	if p.DoorClosed != nil {
		v := p.GetDoorClosed()
		req.DoorClosed = &v
	}
	if p.RssiDbm != nil {
		v := int(p.GetRssiDbm())
		req.RSSIDbm = &v
	}

	return req
}

func heartbeatResponseToProto(r types.HeartbeatResponse) *pb.HeartbeatResponse {
	return &pb.HeartbeatResponse{
		Ok:         r.OK,
		Known:      r.Known,
		ModuleId:   r.ModuleID,
		ServerTime: r.ServerTime,
	}
}

// ── Access ───────────────────────────────────────────────────────────────────

func accessRequestFromProto(p *pb.AccessRequest) types.AccessRequest {
	req := types.AccessRequest{
		ModuleID:    p.GetModuleId(),
		CardID:      p.GetCardId(),
		RequestedAt: p.GetRequestedAt(),
	}

	if p.DoorClosed != nil {
		v := p.GetDoorClosed()
		req.DoorClosed = &v
	}

	return req
}

func accessResponseToProto(r types.AccessResponse) *pb.AccessResponse {
	return &pb.AccessResponse{
		Ok:         r.OK,
		Known:      r.Known,
		Granted:    r.Granted,
		Reason:     r.Reason,
		ModuleId:   r.ModuleID,
		ServerTime: r.ServerTime,
	}
}
