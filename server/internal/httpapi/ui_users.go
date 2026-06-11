package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/permissions"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
)

// handleUIUsersList serves GET /admin/ui/users.
func (s *Server) handleUIUsersList(w http.ResponseWriter, r *http.Request) {
	d := newUIPageData(r, "users")

	users, err := s.adminUserService.ListUsers(r.Context())
	if err != nil {
		s.logger.Printf("ui list users: %v", err)
		d.Flash = "Failed to load users."
		d.FlashType = "error"
	} else {
		d.Users = users
	}

	render.render(w, "users_list", d)
}

// handleUIUsersNew serves GET /admin/ui/users/new.
func (s *Server) handleUIUsersNew(w http.ResponseWriter, r *http.Request) {
	d := newUIPageData(r, "users")
	d.Form = map[string]string{}

	roles, err := s.roleService.ListRoles(r.Context())
	if err != nil {
		s.logger.Printf("ui new user: list roles: %v", err)
		d.Flash = "Failed to load roles."
		d.FlashType = "error"
	}
	d.Roles = roles

	render.render(w, "users_create", d)
}

// handleUIUsersCreate handles POST /admin/ui/users.
func (s *Server) handleUIUsersCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/admin/ui/users/new?flash=Invalid+form+submission&ft=error", http.StatusSeeOther)
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")
	roleID := r.FormValue("role_id")

	_, err := s.adminUserService.CreateUser(r.Context(), username, password, roleID)
	if err != nil {
		if errors.Is(err, store.ErrUsernameAlreadyExists) {
			d := newUIPageData(r, "users")
			d.Form = map[string]string{"Username": username, "RoleID": roleID}
			d.Flash = "Username already exists."
			d.FlashType = "error"
			if roles, e := s.roleService.ListRoles(r.Context()); e == nil {
				d.Roles = roles
			}
			render.render(w, "users_create", d)
			return
		}
		s.logger.Printf("ui create user: %v", err)
		flashRedirect(w, r, "/admin/ui/users/new", err.Error(), "error")
		return
	}

	s.logger.Printf("admin ui: created user %q with role %q", username, roleID)
	flashRedirect(w, r, "/admin/ui/users", "User "+username+" created successfully.", "success")
}

// handleUIUsersEdit serves GET /admin/ui/users/{uuid}.
func (s *Server) handleUIUsersEdit(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	d := newUIPageData(r, "users")

	user, err := s.adminUserService.GetUser(r.Context(), uuid)
	if err != nil {
		if errors.Is(err, service.ErrAdminUserNotFound) {
			http.NotFound(w, r)
			return
		}
		s.logger.Printf("ui get user: %v", err)
		flashRedirect(w, r, "/admin/ui/users", "Failed to load user.", "error")
		return
	}
	d.User = user

	roles, err := s.roleService.ListRoles(r.Context())
	if err != nil {
		s.logger.Printf("ui edit user: list roles: %v", err)
		d.Flash = "Failed to load roles."
		d.FlashType = "error"
	}
	d.Roles = roles

	// Load linked member info so the template can show status.
	if user.MemberUUID != "" && s.memberAccessService != nil {
		rec, err := s.memberAccessService.GetMember(r.Context(), user.MemberUUID)
		if err == nil {
			info := memberRecordToInfo(rec)
			d.Member = &info
		}
	}

	render.render(w, "users_edit", d)
}

// handleUIUsersSetExpiry handles POST /admin/ui/users/{uuid}/expiry.
func (s *Server) handleUIUsersSetExpiry(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	if err := r.ParseForm(); err != nil {
		flashRedirect(w, r, "/admin/ui/users/"+uuid, "Invalid form submission.", "error")
		return
	}

	raw := r.FormValue("expires_at")
	var expiresAt *time.Time
	if raw != "" {
		t, err := time.Parse("2006-01-02", raw)
		if err != nil {
			flashRedirect(w, r, "/admin/ui/users/"+uuid, "Invalid date format.", "error")
			return
		}
		// Treat the date as end-of-day UTC so the account is usable on the
		// chosen day itself.
		eod := time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, time.UTC)
		expiresAt = &eod
	}

	if err := s.adminUserService.SetExpiry(r.Context(), uuid, expiresAt); err != nil {
		if errors.Is(err, service.ErrAdminUserNotFound) {
			http.NotFound(w, r)
			return
		}
		if errors.Is(err, service.ErrLastAdmin) {
			flashRedirect(w, r, "/admin/ui/users/"+uuid, "Cannot set expiry on the last qualifying admin account.", "error")
			return
		}
		s.logger.Printf("ui set expiry: %v", err)
		flashRedirect(w, r, "/admin/ui/users/"+uuid, "Failed to update expiry.", "error")
		return
	}

	if expiresAt == nil {
		flashRedirect(w, r, "/admin/ui/users/"+uuid, "Account expiry cleared.", "success")
	} else {
		flashRedirect(w, r, "/admin/ui/users/"+uuid, "Account expiry updated.", "success")
	}
}

// handleUIUsersSetMemberLink handles POST /admin/ui/users/{uuid}/member.
func (s *Server) handleUIUsersSetMemberLink(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	if err := r.ParseForm(); err != nil {
		flashRedirect(w, r, "/admin/ui/users/"+uuid, "Invalid form submission.", "error")
		return
	}

	raw := r.FormValue("member_uuid")
	var memberUUID *string
	if raw != "" {
		memberUUID = &raw
	}

	if err := s.adminUserService.SetMemberLink(r.Context(), uuid, memberUUID); err != nil {
		if errors.Is(err, service.ErrAdminUserNotFound) {
			http.NotFound(w, r)
			return
		}
		if errors.Is(err, store.ErrMemberAlreadyLinked) {
			flashRedirect(w, r, "/admin/ui/users/"+uuid, "That member is already linked to another admin account.", "error")
			return
		}
		s.logger.Printf("ui set member link: %v", err)
		flashRedirect(w, r, "/admin/ui/users/"+uuid, "Failed to update member link.", "error")
		return
	}

	if memberUUID == nil {
		flashRedirect(w, r, "/admin/ui/users/"+uuid, "Member link cleared.", "success")
	} else {
		flashRedirect(w, r, "/admin/ui/users/"+uuid, "Member linked.", "success")
	}
}

// handleUIUsersAssignRole handles POST /admin/ui/users/{uuid}/role.
func (s *Server) handleUIUsersAssignRole(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	if err := r.ParseForm(); err != nil {
		flashRedirect(w, r, "/admin/ui/users/"+uuid, "Invalid form submission.", "error")
		return
	}

	roleID := r.FormValue("role_id")
	if err := s.adminUserService.AssignRole(r.Context(), uuid, roleID); err != nil {
		if errors.Is(err, service.ErrAdminUserNotFound) {
			http.NotFound(w, r)
			return
		}
		if errors.Is(err, service.ErrLastAdmin) {
			flashRedirect(w, r, "/admin/ui/users/"+uuid, "Cannot move the last admin user off the admin role.", "error")
			return
		}
		s.logger.Printf("ui assign role: %v", err)
		flashRedirect(w, r, "/admin/ui/users/"+uuid, err.Error(), "error")
		return
	}

	s.logger.Printf("admin ui: assigned role %q to user %s", roleID, uuid)
	flashRedirect(w, r, "/admin/ui/users/"+uuid, "Role updated.", "success")
}

// handleUIUsersDisable handles POST /admin/ui/users/{uuid}/disable.
func (s *Server) handleUIUsersDisable(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	sess := sessionFromContext(r.Context())

	if err := s.adminUserService.SetEnabled(r.Context(), uuid, sess.AdminUUID, false); err != nil {
		if errors.Is(err, service.ErrCannotSelfDisable) {
			flashRedirect(w, r, "/admin/ui/users", "You cannot disable your own account.", "error")
			return
		}
		if errors.Is(err, service.ErrAdminUserNotFound) {
			http.NotFound(w, r)
			return
		}
		if errors.Is(err, service.ErrLastAdmin) {
			flashRedirect(w, r, "/admin/ui/users", "Cannot disable the last enabled admin user.", "error")
			return
		}
		s.logger.Printf("ui disable user: %v", err)
		flashRedirect(w, r, "/admin/ui/users", "Failed to disable user.", "error")
		return
	}

	s.logger.Printf("admin ui: disabled user %s", uuid)
	flashRedirect(w, r, "/admin/ui/users", "Account disabled.", "success")
}

// handleUIUsersEnable handles POST /admin/ui/users/{uuid}/enable.
func (s *Server) handleUIUsersEnable(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")

	if err := s.adminUserService.SetEnabled(r.Context(), uuid, "", true); err != nil {
		if errors.Is(err, service.ErrAdminUserNotFound) {
			http.NotFound(w, r)
			return
		}
		s.logger.Printf("ui enable user: %v", err)
		flashRedirect(w, r, "/admin/ui/users", "Failed to enable user.", "error")
		return
	}

	s.logger.Printf("admin ui: enabled user %s", uuid)
	flashRedirect(w, r, "/admin/ui/users", "Account enabled.", "success")
}

// uiUserRoutes registers all /admin/ui/users/* routes on the given mux.
func (s *Server) uiUserRoutes(mux *http.ServeMux) {
	perm := requireUIPermission
	mux.HandleFunc("GET /admin/ui/users",
		perm(permissions.AdminUserList, s.handleUIUsersList))
	mux.HandleFunc("GET /admin/ui/users/new",
		perm(permissions.AdminUserCreate, s.handleUIUsersNew))
	mux.HandleFunc("POST /admin/ui/users",
		perm(permissions.AdminUserCreate, s.handleUIUsersCreate))
	mux.HandleFunc("GET /admin/ui/users/{uuid}",
		perm(permissions.AdminUserList, s.handleUIUsersEdit))
	mux.HandleFunc("POST /admin/ui/users/{uuid}/role",
		perm(permissions.AdminUserEdit, s.handleUIUsersAssignRole))
	mux.HandleFunc("POST /admin/ui/users/{uuid}/disable",
		perm(permissions.AdminUserDisable, s.handleUIUsersDisable))
	mux.HandleFunc("POST /admin/ui/users/{uuid}/enable",
		perm(permissions.AdminUserDisable, s.handleUIUsersEnable))
	mux.HandleFunc("POST /admin/ui/users/{uuid}/expiry",
		perm(permissions.AdminUserEdit, s.handleUIUsersSetExpiry))
	mux.HandleFunc("POST /admin/ui/users/{uuid}/member",
		perm(permissions.AdminUserEdit, s.handleUIUsersSetMemberLink))
}
