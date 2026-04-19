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
	AdminService     *service.AdminService
	// HMACSecret is the pre-shared key for X-Portunus-Sig verification.
	// Leave empty to disable HMAC enforcement (not recommended for production).
	HMACSecret string
	// AdminAPIKey protects /admin/v1/* routes with Bearer token auth.
	// Leave empty to disable admin auth (not recommended for production).
	AdminAPIKey string
}

type Server struct {
	httpServer       *http.Server
	logger           *log.Logger
	mux              *http.ServeMux
	heartbeatService *service.HeartbeatService
	accessService    *service.AccessService
	adminService     *service.AdminService
}

func NewServer(d Dependencies) *Server {
	mux := http.NewServeMux()

	s := &Server{
		logger:           d.Logger,
		mux:              mux,
		heartbeatService: d.HeartbeatService,
		accessService:    d.AccessService,
		adminService:     d.AdminService,
	}

	// ── Device endpoints (ESP32 modules) ────────────────────────────────
	mux.HandleFunc("POST /v1/heartbeat", s.handleHeartbeat)
	mux.HandleFunc("POST /v1/access_request", s.handleAccessRequest)

	// ── Admin endpoints ─────────────────────────────────────────────────
	if d.AdminService != nil {
		// Modules
		mux.HandleFunc("GET /admin/v1/modules", s.handleAdminListModules)
		mux.HandleFunc("GET /admin/v1/modules/{module_id}", s.handleAdminGetModule)
		mux.HandleFunc("POST /admin/v1/modules", s.handleAdminRegisterModule)
		mux.HandleFunc("POST /admin/v1/modules/{module_id}/revoke", s.handleAdminRevokeModule)
		mux.HandleFunc("DELETE /admin/v1/modules/{module_id}", s.handleAdminDeleteModule)

		// Cards
		mux.HandleFunc("GET /admin/v1/cards", s.handleAdminListCards)
		mux.HandleFunc("POST /admin/v1/cards", s.handleAdminRegisterCard)
		mux.HandleFunc("PATCH /admin/v1/cards/{card_hash}", s.handleAdminUpdateCardStatus)
		mux.HandleFunc("DELETE /admin/v1/cards/{card_hash}", s.handleAdminDeleteCard)

		// Doors
		mux.HandleFunc("GET /admin/v1/doors", s.handleAdminListDoors)
		mux.HandleFunc("POST /admin/v1/doors", s.handleAdminRegisterDoor)
		mux.HandleFunc("DELETE /admin/v1/doors/{door_id}", s.handleAdminDeleteDoor)

		d.Logger.Printf("admin API: ENABLED (%d endpoints registered)", 12)
	}

	// Build middleware chain: logging → admin auth → HMAC auth → mux
	var handler http.Handler = mux

	if d.AdminAPIKey != "" {
		handler = adminAuthMiddleware(d.AdminAPIKey, handler)
		d.Logger.Printf("admin API key auth: ENABLED")
	} else if d.AdminService != nil {
		d.Logger.Printf("admin API key auth: DISABLED (set PORTUNUS_ADMIN_API_KEY to enable — NOT RECOMMENDED)")
	}

	if d.HMACSecret != "" {
		handler = hmacAuthMiddleware(d.Logger, d.HMACSecret, handler)
		d.Logger.Printf("HMAC request signing enforcement: ENABLED")
	} else {
		d.Logger.Printf("HMAC request signing enforcement: DISABLED (set PORTUNUS_HMAC_SECRET to enable)")
	}
	handler = loggingMiddleware(d.Logger, handler)

	s.httpServer = &http.Server{
		Addr:              d.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	return s
}

func (s *Server) Handler() http.Handler { return s.httpServer.Handler }

// Start starts the server in plain HTTP mode.
func (s *Server) Start() error {
	return s.httpServer.ListenAndServe()
}

// StartTLS starts the server with TLS using the provided certificate and key files.
// certFile and keyFile must be PEM-encoded.
func (s *Server) StartTLS(certFile, keyFile string) error {
	return s.httpServer.ListenAndServeTLS(certFile, keyFile)
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
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
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
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
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
		default:
			s.logger.Printf("access_request error: %v", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "unexpected server error")
			return
		}
	}

	if !resp.Known {
		if protoReq {
			writeProto(w, http.StatusForbidden, accessResponseToProto(resp))
		} else {
			writeJSON(w, http.StatusForbidden, resp)
		}
		return
	}

	if protoReq {
		writeProto(w, http.StatusOK, accessResponseToProto(resp))
	} else {
		writeJSON(w, http.StatusOK, resp)
	}
}
