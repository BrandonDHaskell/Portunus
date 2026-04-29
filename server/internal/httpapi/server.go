package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	pb "github.com/BrandonDHaskell/Portunus/server/api/portunus/v1"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/permissions"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

type Dependencies struct {
	Logger              *log.Logger
	Addr                string
	HeartbeatService    *service.HeartbeatService
	AccessService       *service.AccessService
	ProvisionService    *service.ProvisionService
	AdminService        *service.AdminService
	AuthService         *service.AuthService
	AdminUserService    *service.AdminUserService
	RoleService         *service.RoleService
	MemberAccessService *service.MemberAccessService
	ModuleAuthService   *service.ModuleAuthorizationService
	// HMACSecret is the pre-shared key for X-Portunus-Sig verification.
	// Leave empty to disable HMAC enforcement (not recommended for production).
	HMACSecret string
	// TLSEnabled controls whether the Secure cookie flag and HSTS header are set.
	TLSEnabled bool
}

type Server struct {
	httpServer          *http.Server
	logger              *log.Logger
	mux                 *http.ServeMux
	heartbeatService    *service.HeartbeatService
	accessService       *service.AccessService
	provisionService    *service.ProvisionService
	adminService        *service.AdminService
	authService         *service.AuthService
	adminUserService    *service.AdminUserService
	roleService         *service.RoleService
	memberAccessService *service.MemberAccessService
	moduleAuthService   *service.ModuleAuthorizationService
	tlsEnabled          bool
}

func NewServer(d Dependencies) *Server {
	mux := http.NewServeMux()

	s := &Server{
		logger:              d.Logger,
		mux:                 mux,
		heartbeatService:    d.HeartbeatService,
		accessService:       d.AccessService,
		provisionService:    d.ProvisionService,
		adminService:        d.AdminService,
		authService:         d.AuthService,
		adminUserService:    d.AdminUserService,
		roleService:         d.RoleService,
		memberAccessService: d.MemberAccessService,
		moduleAuthService:   d.ModuleAuthService,
		tlsEnabled:          d.TLSEnabled,
	}

	// ── Device endpoints (ESP32 modules) ────────────────────────────────
	mux.HandleFunc("POST /v1/heartbeat", s.handleHeartbeat)
	mux.HandleFunc("POST /v1/access_request", s.handleAccessRequest)
	if d.ProvisionService != nil {
		mux.HandleFunc("POST /v1/provision_credential", s.handleProvisionCredential)
	}

	// ── Admin auth endpoints (no permission guard — open to any caller) ─
	if d.AuthService != nil {
		mux.HandleFunc("POST /admin/v1/login", s.handleAdminLogin)
		mux.HandleFunc("POST /admin/v1/logout", requireSession(s.handleAdminLogout))
		mux.HandleFunc("POST /admin/v1/change-password", requireSession(s.handleAdminChangePassword))
		mux.HandleFunc("GET /admin/v1/health", requireSession(s.handleAdminHealth))
	}

	// ── Admin endpoints (session + permission required) ─────────────────
	if d.AdminService != nil {
		// Modules
		mux.HandleFunc("GET /admin/v1/modules",
			requirePermission(permissions.ModuleList, s.handleAdminListModules))
		mux.HandleFunc("GET /admin/v1/modules/{module_id}",
			requirePermission(permissions.ModuleGet, s.handleAdminGetModule))
		mux.HandleFunc("POST /admin/v1/modules",
			requirePermission(permissions.ModuleRegister, s.handleAdminRegisterModule))
		mux.HandleFunc("POST /admin/v1/modules/{module_id}/revoke",
			requirePermission(permissions.ModuleRevoke, s.handleAdminRevokeModule))
		mux.HandleFunc("DELETE /admin/v1/modules/{module_id}",
			requirePermission(permissions.ModuleDelete, s.handleAdminDeleteModule))

		// Credentials
		mux.HandleFunc("GET /admin/v1/credentials",
			requirePermission(permissions.CredentialList, s.handleAdminListCredentials))
		mux.HandleFunc("POST /admin/v1/credentials",
			requirePermission(permissions.CredentialRegister, s.handleAdminRegisterCredential))
		mux.HandleFunc("PATCH /admin/v1/credentials/{credential_hash}",
			requirePermission(permissions.CredentialUpdateStatus, s.handleAdminUpdateCredentialStatus))
		mux.HandleFunc("DELETE /admin/v1/credentials/{credential_hash}",
			requirePermission(permissions.CredentialDelete, s.handleAdminDeleteCredential))

		// Doors
		mux.HandleFunc("GET /admin/v1/doors",
			requirePermission(permissions.DoorList, s.handleAdminListDoors))
		mux.HandleFunc("POST /admin/v1/doors",
			requirePermission(permissions.DoorRegister, s.handleAdminRegisterDoor))
		mux.HandleFunc("DELETE /admin/v1/doors/{door_id}",
			requirePermission(permissions.DoorDelete, s.handleAdminDeleteDoor))

		d.Logger.Printf("admin API: ENABLED")
	}

	// ── Member access endpoints (session + permission required) ──────────
	if d.MemberAccessService != nil {
		mux.HandleFunc("GET /admin/v1/members",
			requirePermission(permissions.MemberList, s.handleAdminListMembers))
		mux.HandleFunc("GET /admin/v1/members/pending",
			requirePermission(permissions.MemberList, s.handleAdminListPendingAuthorizations))
		mux.HandleFunc("GET /admin/v1/members/{member_uuid}",
			requirePermission(permissions.MemberView, s.handleAdminGetMember))
		mux.HandleFunc("POST /admin/v1/members",
			requirePermission(permissions.MemberProvision, s.handleAdminProvisionMember))
		mux.HandleFunc("POST /admin/v1/members/{member_uuid}/credential",
			requirePermission(permissions.MemberProvision, s.handleAdminAttachCredential))
		mux.HandleFunc("PUT /admin/v1/members/{member_uuid}/role",
			requirePermission(permissions.MemberAssignRole, s.handleAdminAssignRole))
		mux.HandleFunc("POST /admin/v1/members/{member_uuid}/disable",
			requirePermission(permissions.MemberDisable, s.handleAdminDisableMember))
		mux.HandleFunc("POST /admin/v1/members/{member_uuid}/archive",
			requirePermission(permissions.MemberArchive, s.handleAdminArchiveMember))
	}

	// ── Module authorization endpoints (session + permission required) ────
	if d.ModuleAuthService != nil {
		mux.HandleFunc("GET /admin/v1/modules/{module_id}/authorizations",
			requirePermission(permissions.ModuleAuthList, s.handleAdminListAuthorizationsByModule))
		mux.HandleFunc("POST /admin/v1/modules/{module_id}/authorizations",
			requirePermission(permissions.ModuleAuthGrant, s.handleAdminGrantModuleAuthorization))
		mux.HandleFunc("DELETE /admin/v1/modules/{module_id}/authorizations/{member_uuid}",
			requirePermission(permissions.ModuleAuthRevoke, s.handleAdminRevokeModuleAuthorization))
		mux.HandleFunc("GET /admin/v1/members/{member_uuid}/authorizations",
			requirePermission(permissions.ModuleAuthList, s.handleAdminListAuthorizationsByMember))
	}

	// ── Admin UI (HTML) ────────────────────────────────────────────────────
	if d.AuthService != nil {
		// Auth routes (no permission required — handled per-route).
		mux.HandleFunc("GET /admin/ui/login", s.handleUILogin)
		mux.HandleFunc("POST /admin/ui/login", s.handleUILogin)
		mux.HandleFunc("POST /admin/ui/logout", requireUISession(s.handleUILogout))
		mux.HandleFunc("GET /admin/ui/change-password", requireUISession(s.handleUIChangePassword))
		mux.HandleFunc("POST /admin/ui/change-password", requireUISession(s.handleUIChangePassword))
		mux.HandleFunc("GET /admin/ui/", s.handleUIDashboard)

		// Static assets (no auth required).
		mux.Handle("GET /admin/ui/static/", http.StripPrefix("/admin/ui/static/", staticHandler()))
	}
	if d.AdminUserService != nil {
		s.uiUserRoutes(mux)
	}
	if d.RoleService != nil {
		s.uiRoleRoutes(mux)
	}
	if d.MemberAccessService != nil {
		s.uiMemberRoutes(mux)
	}

	// Build middleware chain: logging → HSTS (if TLS) → session → HMAC → mux
	var handler http.Handler = mux

	if d.AuthService != nil {
		handler = sessionMiddleware(d.AuthService, handler)
	}

	if d.TLSEnabled {
		handler = hstsMiddleware(handler)
		d.Logger.Printf("HSTS: ENABLED")
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
		case errors.Is(err, service.ErrInvalidCredentialID):
			writeError(w, http.StatusBadRequest, "invalid_credential_id", err.Error())
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

func (s *Server) handleProvisionCredential(w http.ResponseWriter, r *http.Request) {
	var req types.ProvisionCredentialRequest
	protoReq := isProtobuf(r)

	if protoReq {
		var pbReq pb.ProvisionCredentialRequest
		if err := readProto(r, &pbReq); err != nil {
			writeError(w, http.StatusBadRequest, "bad_proto", "invalid protobuf body")
			return
		}
		req = provisionRequestFromProto(&pbReq)
	} else {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_json", "invalid JSON body")
			return
		}
	}

	resp, err := s.provisionService.Provision(r.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidModuleID):
			writeError(w, http.StatusBadRequest, "invalid_module_id", err.Error())
			return
		case errors.Is(err, service.ErrProvisionCredentialHashRequired):
			writeError(w, http.StatusBadRequest, "invalid_credential_hash", err.Error())
			return
		default:
			s.logger.Printf("provision_credential error: %v", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "unexpected server error")
			return
		}
	}

	if !resp.Known {
		writeError(w, http.StatusForbidden, "unknown_module", "module is not registered")
		return
	}

	if protoReq {
		writeProto(w, http.StatusOK, provisionResponseToProto(resp))
	} else {
		writeJSON(w, http.StatusOK, resp)
	}
}
