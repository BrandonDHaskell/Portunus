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

	// Credential management (legacy table; superseded by member_access in PR 4)
	CredentialList         = "credential.list"
	CredentialRegister     = "credential.register"
	CredentialUpdateStatus = "credential.update_status"
	CredentialDelete       = "credential.delete"

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

	// Member access management (used from PR 4 onward)
	MemberProvision   = "member.provision"
	MemberList        = "member.list"
	MemberView        = "member.view"
	MemberAssignRole  = "member.assign_role"
	MemberDisable     = "member.disable"
	MemberArchive     = "member.archive"

	// Module authorization management (used from PR 4 onward)
	ModuleAuthGrant  = "module_auth.grant"
	ModuleAuthRevoke = "module_auth.revoke"
	ModuleAuthList   = "module_auth.list"
)

// All returns every defined permission. Used to seed the admin role and to
// render the permission-assignment checkbox grid in the UI.
func All() []string {
	return []string{
		ModuleList, ModuleGet, ModuleRegister, ModuleRevoke, ModuleDelete,
		CredentialList, CredentialRegister, CredentialUpdateStatus, CredentialDelete,
		DoorList, DoorRegister, DoorDelete,
		AdminUserCreate, AdminUserList, AdminUserEdit, AdminUserDisable,
		RoleList, RoleCreate, RoleEdit, RoleDelete, RoleAssignPermission,
		MemberProvision, MemberList, MemberView, MemberAssignRole, MemberDisable, MemberArchive,
		ModuleAuthGrant, ModuleAuthRevoke, ModuleAuthList,
	}
}
