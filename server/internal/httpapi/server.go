package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	pb "github.com/BrandonDHaskell/Portunus/server/api/portunus/v1"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

type Dependencies struct {
	Logger           *log.Logger
	Addr             string
	HeartbeatService *service.HeartbeatService
	AccessService    *service.AccessService
}

type Server struct {
	httpServer       *http.Server
	logger           *log.Logger
	mux              *http.ServeMux
	heartbeatService *service.HeartbeatService
	accessService    *service.AccessService
}

func NewServer(d Dependencies) *Server {
	mux := http.NewServeMux()

	s := &Server{
		logger:           d.Logger,
		mux:              mux,
		heartbeatService: d.HeartbeatService,
		accessService:    d.AccessService,
	}

	mux.HandleFunc("POST /v1/heartbeat", s.handleHeartbeat)
	mux.HandleFunc("POST /v1/access_request", s.handleAccessRequest)

	handler := loggingMiddleware(d.Logger, mux)

	s.httpServer = &http.Server{
		Addr:              d.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	return s
}

func (s *Server) Handler() http.Handler { return s.httpServer.Handler }

func (s *Server) Start() error {
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req types.HeartbeatRequest
	protoReq := isProtobuf(r)

	if protoReq {
		var pbReq pb.HeartbeatRequest
		if err := readProto(r, &pbReq); err != nil {
			writeError(w, http.StatusBadRequest, "bad_proto", "invalid protobuf body")
			return
		}
		req = heartbeatRequestFromProto(&pbReq)
	} else {
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_json", "invalid JSON body")
			return
		}
	}

	resp, err := s.heartbeatService.Record(r.Context(), req)
	if err != nil {
		if errors.Is(err, service.ErrInvalidModuleID) {
			writeError(w, http.StatusBadRequest, "invalid_module_id", err.Error())
			return
		}
		s.logger.Printf("heartbeat error: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "unexpected server error")
		return
	}

	if protoReq {
		writeProto(w, http.StatusOK, heartbeatResponseToProto(resp))
	} else {
		writeJSON(w, http.StatusOK, resp)
	}
}

func (s *Server) handleAccessRequest(w http.ResponseWriter, r *http.Request) {
	var req types.AccessRequest
	protoReq := isProtobuf(r)

	if protoReq {
		var pbReq pb.AccessRequest
		if err := readProto(r, &pbReq); err != nil {
			writeError(w, http.StatusBadRequest, "bad_proto", "invalid protobuf body")
			return
		}
		req = accessRequestFromProto(&pbReq)
	} else {
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_json", "invalid JSON body")
			return
		}
	}

	resp, err := s.accessService.Decide(r.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidModuleID):
			writeError(w, http.StatusBadRequest, "invalid_module_id", err.Error())
			return
		case errors.Is(err, service.ErrInvalidCardID):
			writeError(w, http.StatusBadRequest, "invalid_card_id", err.Error())
			return
		case errors.Is(err, service.ErrUnknownModule):
			// Unknown module is blocked from access flow
			if protoReq {
				writeProto(w, http.StatusForbidden, accessResponseToProto(resp))
			} else {
				writeJSON(w, http.StatusForbidden, resp)
			}
			return
		default:
			s.logger.Printf("access_request error: %v", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "unexpected server error")
			return
		}
	}

	if protoReq {
		writeProto(w, http.StatusOK, accessResponseToProto(resp))
	} else {
		writeJSON(w, http.StatusOK, resp)
	}
}
