// Package grpcapi implements the PortunusService gRPC interface.
//
// This is the gRPC counterpart of the httpapi package.  Both delegate to
// the same service layer (heartbeat, access, admin) — the only difference
// is the transport protocol:
//
//	httpapi:  HTTP/1.1  + protobuf or JSON  (ESP32 legacy / admin API)
//	grpcapi:  HTTP/2    + gRPC              (ESP32 with gRPC firmware)
//
// The gRPC server runs on a separate port (PORTUNUS_GRPC_ADDR) and can
// co-exist with the HTTP server.  HMAC request authentication is handled
// by a gRPC unary interceptor (see interceptors.go), mirroring the HTTP
// middleware in httpapi.
package grpcapi

import (
	"context"
	"errors"
	"log"

	pb "github.com/BrandonDHaskell/Portunus/server/api/portunus/v1"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Dependencies holds the services needed by the gRPC handler.
type Dependencies struct {
	Logger           *log.Logger
	HeartbeatService *service.HeartbeatService
	AccessService    *service.AccessService
}

// Server implements pb.PortunusServiceServer.
type Server struct {
	pb.UnimplementedPortunusServiceServer

	logger           *log.Logger
	heartbeatService *service.HeartbeatService
	accessService    *service.AccessService
}

// NewServer creates a gRPC server handler that delegates to the service layer.
func NewServer(d Dependencies) *Server {
	return &Server{
		logger:           d.Logger,
		heartbeatService: d.HeartbeatService,
		accessService:    d.AccessService,
	}
}

// ─── Heartbeat ──────────────────────────────────────────────────────────────

func (s *Server) SendHeartbeat(ctx context.Context, req *pb.HeartbeatRequest) (*pb.HeartbeatResponse, error) {
	// Convert protobuf request → domain type.
	domainReq := types.HeartbeatRequest{
		ModuleID:        req.GetModuleId(),
		FirmwareVersion: req.GetFirmwareVersion(),
		UptimeSeconds:   req.GetUptimeS(),
		IP:              req.GetIp(),
		FreeHeapBytes:   req.GetFreeHeapBytes(),
		Sequence:        req.GetSequence(),
	}
	if req.DoorClosed != nil {
		dc := req.GetDoorClosed()
		domainReq.DoorClosed = &dc
	}
	if req.RssiDbm != nil {
		rssi := int(req.GetRssiDbm())
		domainReq.RSSIDbm = &rssi
	}

	// Delegate to the service layer (same code path as HTTP).
	resp, err := s.heartbeatService.Record(ctx, domainReq)
	if err != nil {
		if errors.Is(err, service.ErrInvalidModuleID) {
			return nil, status.Errorf(codes.InvalidArgument, "invalid module_id: %v", err)
		}
		s.logger.Printf("heartbeat gRPC error: %v", err)
		return nil, status.Errorf(codes.Internal, "unexpected server error")
	}

	// Convert domain response → protobuf.
	return &pb.HeartbeatResponse{
		Ok:         resp.OK,
		Known:      resp.Known,
		ModuleId:   resp.ModuleID,
		ServerTime: resp.ServerTime,
	}, nil
}

// ─── Access ─────────────────────────────────────────────────────────────────

func (s *Server) RequestAccess(ctx context.Context, req *pb.AccessRequest) (*pb.AccessResponse, error) {
	// Convert protobuf request → domain type.
	domainReq := types.AccessRequest{
		ModuleID:     req.GetModuleId(),
		CredentialID: req.GetCredentialId(),
		RequestedAt:  req.GetRequestedAt(),
	}
	if req.DoorClosed != nil {
		dc := req.GetDoorClosed()
		domainReq.DoorClosed = &dc
	}

	// Delegate to the service layer.
	resp, err := s.accessService.Decide(ctx, domainReq)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidModuleID):
			return nil, status.Errorf(codes.InvalidArgument, "invalid module_id: %v", err)
		case errors.Is(err, service.ErrInvalidCredentialID):
			return nil, status.Errorf(codes.InvalidArgument, "invalid credential_id: %v", err)
		default:
			s.logger.Printf("access_request gRPC error: %v", err)
			return nil, status.Errorf(codes.Internal, "unexpected server error")
		}
	}

	return &pb.AccessResponse{
		Ok:         resp.OK,
		Known:      resp.Known,
		Granted:    resp.Granted,
		Reason:     resp.Reason,
		ModuleId:   resp.ModuleID,
		ServerTime: resp.ServerTime,
	}, nil
}
