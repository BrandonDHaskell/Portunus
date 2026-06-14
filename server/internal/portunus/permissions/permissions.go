// Package permissions defines the canonical set of permission constants for
// Portunus. These strings are stored in role_permissions and checked at
// request time. New permissions must be added here — they cannot be invented
// at runtime.
package permissions

const (
	// Module management
	ModuleList     = "module.list"
	ModuleGet      = "module.get"
	ModuleRegister = "module.register"
	ModuleRevoke   = "module.revoke"
	ModuleDelete   = "module.delete"

	// Door management
	DoorList     = "door.list"
	DoorRegister = "door.register"
	DoorDelete   = "door.delete"

	// Admin user management
	AdminUserCreate  = "admin_user.create"
	AdminUserList    = "admin_user.list"
	AdminUserEdit    = "admin_user.edit"
	AdminUserDisable = "admin_user.disable"

	// Role management
	RoleList             = "role.list"
	RoleCreate           = "role.create"
	RoleEdit             = "role.edit"
	RoleDelete           = "role.delete"
	RoleAssignPermission = "role.assign_permissions"

	// Member access management
	MemberEnroll  = "member.enroll"
	MemberList    = "member.list"
	MemberView    = "member.view"
	MemberEdit    = "member.edit"
	MemberDisable = "member.disable"
	MemberArchive = "member.archive"

	// Module authorization management
	// _held: actor may only grant/revoke modules their linked member currently holds.
	// _any:  unscoped; seeded to the admin role.
	ModuleAuthGrantHeld  = "module_auth.grant_held"
	ModuleAuthGrantAny   = "module_auth.grant_any"
	ModuleAuthRevokeHeld = "module_auth.revoke_held"
	ModuleAuthRevokeAny  = "module_auth.revoke_any"
	ModuleAuthList       = "module_auth.list"

	// Audit log
	AuditLogList = "audit_log.list"
)

// All returns every defined permission. Used to seed the admin role and to
// render the permission-assignment checkbox grid in the UI.
func All() []string {
	return []string{
		ModuleList, ModuleGet, ModuleRegister, ModuleRevoke, ModuleDelete,
		DoorList, DoorRegister, DoorDelete,
		AdminUserCreate, AdminUserList, AdminUserEdit, AdminUserDisable,
		RoleList, RoleCreate, RoleEdit, RoleDelete, RoleAssignPermission,
		MemberEnroll, MemberList, MemberView, MemberEdit, MemberDisable, MemberArchive,
		ModuleAuthGrantHeld, ModuleAuthGrantAny, ModuleAuthRevokeHeld, ModuleAuthRevokeAny, ModuleAuthList,
		AuditLogList,
	}
}
