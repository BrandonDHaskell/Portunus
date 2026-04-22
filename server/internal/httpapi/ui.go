package httpapi

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"strings"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/permissions"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
)

//go:embed templates static
var uiFS embed.FS

// PermGroup is used to render the permission checkbox grid grouped by category.
type PermGroup struct {
	Label string
	Perms []string
}

// UIPageData is the common data struct passed to every UI template.
type UIPageData struct {
	Session    *service.AdminSession
	ActivePage string
	Flash      string
	FlashType  string // "success", "error", "warn"

	// Page-specific fields — only the relevant ones are set per page.
	UserCount  int
	RoleCount  int
	Users      []service.AdminUserInfo
	Roles      []service.RoleInfo
	User       *service.AdminUserInfo
	Role       *service.RoleInfo
	PermGroups []PermGroup
	Form       map[string]string
	MustChange bool
}

// HasPerm returns true if the given permission is in Role.Permissions.
// Used from role_edit template: {{if $.HasPerm "module.list"}}.
func (d *UIPageData) HasPerm(p string) bool {
	if d.Role == nil {
		return false
	}
	for _, rp := range d.Role.Permissions {
		if rp == p {
			return true
		}
	}
	return false
}

// templateFuncs adds helpers available in all templates.
var templateFuncs = template.FuncMap{
	"derefInt": func(p *int) int {
		if p == nil {
			return 0
		}
		return *p
	},
	"not": func(b bool) bool { return !b },
}

// uiRenderer renders a named page template against the base layout.
type uiRenderer struct{}

func (uiRenderer) render(w http.ResponseWriter, page string, data *UIPageData) {
	t, err := template.New("base").
		Funcs(templateFuncs).
		ParseFS(uiFS, "templates/base.html", "templates/"+page+".html")
	if err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "base", data); err != nil {
		// Headers already sent — log only.
		return
	}
}

// newUIPageData builds a UIPageData pre-filled with session and optional flash.
func newUIPageData(r *http.Request, activePage string) *UIPageData {
	d := &UIPageData{
		Session:    sessionFromContext(r.Context()),
		ActivePage: activePage,
		Form:       map[string]string{},
	}
	// Read flash from query params (?flash=msg&ft=success).
	if f := r.URL.Query().Get("flash"); f != "" {
		d.Flash = f
		d.FlashType = r.URL.Query().Get("ft")
		if d.FlashType == "" {
			d.FlashType = "success"
		}
	}
	return d
}

// permGroups builds the grouped permission list from the constants package.
func permGroups() []PermGroup {
	groups := []struct {
		label  string
		prefix string
	}{
		{"Module Management", "module."},
		{"Credential Management", "credential."},
		{"Door Management", "door."},
		{"Admin User Management", "admin_user."},
		{"Role Management", "role."},
		{"Member Management", "member."},
		{"Module Authorizations", "module_auth."},
	}

	all := permissions.All()
	var result []PermGroup
	claimed := make(map[string]bool, len(all))

	for _, g := range groups {
		var perms []string
		for _, p := range all {
			if strings.HasPrefix(p, g.prefix) {
				perms = append(perms, p)
				claimed[p] = true
			}
		}
		if len(perms) > 0 {
			result = append(result, PermGroup{Label: g.label, Perms: perms})
		}
	}

	// Catch anything that doesn't match a prefix.
	var other []string
	for _, p := range all {
		if !claimed[p] {
			other = append(other, p)
		}
	}
	if len(other) > 0 {
		result = append(result, PermGroup{Label: "Other", Perms: other})
	}
	return result
}

// staticHandler serves embedded static files under /admin/ui/static/.
func staticHandler() http.Handler {
	sub, _ := fs.Sub(uiFS, "static")
	return http.FileServer(http.FS(sub))
}

// requireUISession is the UI-layer session guard: redirects to login instead of
// returning JSON 401.
func requireUISession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if sessionFromContext(r.Context()) == nil {
			http.Redirect(w, r, "/admin/ui/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

// requireUIPermission guards a UI route with a session + permission check.
// If must_change_pw is set the user is redirected to the change-password page
// instead of their requested destination.
func requireUIPermission(perm string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess := sessionFromContext(r.Context())
		if sess == nil {
			http.Redirect(w, r, "/admin/ui/login", http.StatusSeeOther)
			return
		}
		if sess.MustChangePW {
			http.Redirect(w, r, "/admin/ui/change-password", http.StatusSeeOther)
			return
		}
		if !sess.HasPermission(perm) {
			http.Redirect(w, r, "/admin/ui/?flash=Insufficient+permissions&ft=error", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

// flashRedirect redirects with a flash message and type as query params.
func flashRedirect(w http.ResponseWriter, r *http.Request, dest, msg, flashType string) {
	u := dest + "?flash=" + urlEncode(msg) + "&ft=" + flashType
	http.Redirect(w, r, u, http.StatusSeeOther)
}

func urlEncode(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			b.WriteRune(c)
		case c == ' ':
			b.WriteByte('+')
		default:
			b.WriteString("%" + hexByte(byte(c)))
		}
	}
	return b.String()
}

const hexChars = "0123456789ABCDEF"

func hexByte(b byte) string {
	return string([]byte{hexChars[b>>4], hexChars[b&0xf]})
}
