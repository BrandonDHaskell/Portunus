package httpapi

import (
	"context"
	"net/http"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
)

const sessionCookieName = "portunus_session"

type contextKey string

const sessionKey contextKey = "admin_session"

func sessionFromContext(ctx context.Context) *service.AdminSession {
	s, _ := ctx.Value(sessionKey).(*service.AdminSession)
	return s
}

func withSession(ctx context.Context, sess *service.AdminSession) context.Context {
	return context.WithValue(ctx, sessionKey, sess)
}

// sessionMiddleware resolves the session cookie on every request. If a valid
// session is found it is injected into the request context. Requests with no
// cookie or an invalid/expired session pass through with no session in context.
func sessionMiddleware(authSvc *service.AuthService, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err == nil && cookie.Value != "" {
			sess, err := authSvc.ResolveSession(r.Context(), cookie.Value)
			if err == nil {
				r = r.WithContext(withSession(r.Context(), sess))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// requireSession wraps a handler and rejects requests with no valid session.
func requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if sessionFromContext(r.Context()) == nil {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "valid session required")
			return
		}
		next(w, r)
	}
}

// requirePermission wraps a handler and enforces:
//  1. A valid session is present.
//  2. The session carries the required permission.
//  3. The session does not have must_change_pw set (except for the change-password
//     endpoint, which uses requireSession directly).
func requirePermission(perm string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess := sessionFromContext(r.Context())
		if sess == nil {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "valid session required")
			return
		}
		if sess.MustChangePW {
			writeError(w, http.StatusForbidden, "password_change_required",
				"you must change your password before performing other actions")
			return
		}
		if !sess.HasPermission(perm) {
			writeError(w, http.StatusForbidden, "forbidden", "insufficient permissions")
			return
		}
		next(w, r)
	}
}

// hstsMiddleware adds Strict-Transport-Security to every response. Only wire
// this in when the server is running with TLS.
func hstsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		next.ServeHTTP(w, r)
	})
}

// setSessionCookie writes the session cookie with HttpOnly + SameSite=Strict.
// The Secure flag is only set when tls is true.
func setSessionCookie(w http.ResponseWriter, sessionID string, tls bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionID,
		Path:     "/admin/",
		HttpOnly: true,
		Secure:   tls,
		SameSite: http.SameSiteStrictMode,
	})
}

// clearSessionCookie expires the session cookie.
func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/admin/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

