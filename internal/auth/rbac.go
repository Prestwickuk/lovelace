package auth

import "github.com/Prestwickuk/lovelace/internal/domain"

// Permission defines a fine-grained administrative capability.
type Permission string

const (
	PermPropertyRead       Permission = "property:read"
	PermPropertyWrite      Permission = "property:write"
	PermInventoryRead      Permission = "inventory:read"
	PermInventoryWrite     Permission = "inventory:write"
	PermRatesRead          Permission = "rates:read"
	PermRatesWrite         Permission = "rates:write"
	PermBlocksRead         Permission = "blocks:read"
	PermBlocksWrite        Permission = "blocks:write"
	PermReservationsRead   Permission = "reservations:read"
	PermReservationsWrite  Permission = "reservations:write"
	PermReleasesRead       Permission = "releases:read"
	PermReleasesWrite      Permission = "releases:write"
	PermChannelsRead       Permission = "channels:read"
	PermChannelsWrite      Permission = "channels:write"
	PermUsersManage        Permission = "users:manage"
	PermAuditRead          Permission = "audit:read"
	PermOpsRead            Permission = "ops:read"
	PermHousekeepingWrite  Permission = "housekeeping:write"
	PermMaintenanceRead    Permission = "maintenance:read"
	PermMaintenanceReport  Permission = "maintenance:report"
	PermMaintenanceWrite   Permission = "maintenance:write"
	PermMaintenanceInspect Permission = "maintenance:inspect"
	PermMaintenanceManage  Permission = "maintenance:manage"
	PermGuestsRead         Permission = "guests:read"
	PermGuestsWrite        Permission = "guests:write"
	PermGuestsNotes        Permission = "guests:notes"
	PermGuestsMerge        Permission = "guests:merge"
	PermGuestsExport       Permission = "guests:export"
	PermGuestsAnonymize    Permission = "guests:anonymize"
	PermCommsRead          Permission = "comms:read"
	PermCommsWrite         Permission = "comms:write"
	PermCommsReconcile     Permission = "comms:reconcile"
	PermCommsTemplates     Permission = "comms:templates"
	PermReportsRead        Permission = "reports:read"
	PermReportsExport      Permission = "reports:export"
)

var rolePermissions = map[domain.Role]map[Permission]bool{
	domain.RoleOwnerAdmin: {
		PermPropertyRead:       true,
		PermPropertyWrite:      true,
		PermInventoryRead:      true,
		PermInventoryWrite:     true,
		PermRatesRead:          true,
		PermRatesWrite:         true,
		PermBlocksRead:         true,
		PermBlocksWrite:        true,
		PermReservationsRead:   true,
		PermReservationsWrite:  true,
		PermReleasesRead:       true,
		PermReleasesWrite:      true,
		PermChannelsRead:       true,
		PermChannelsWrite:      true,
		PermUsersManage:        true,
		PermAuditRead:          true,
		PermOpsRead:            true,
		PermHousekeepingWrite:  true,
		PermMaintenanceRead:    true,
		PermMaintenanceReport:  true,
		PermMaintenanceWrite:   true,
		PermMaintenanceInspect: true,
		PermMaintenanceManage:  true,
		PermGuestsRead:         true,
		PermGuestsWrite:        true,
		PermGuestsNotes:        true,
		PermGuestsMerge:        true,
		PermGuestsExport:       true,
		PermGuestsAnonymize:    true,
		PermCommsRead:          true,
		PermCommsWrite:         true,
		PermCommsReconcile:     true,
		PermCommsTemplates:     true,
		PermReportsRead:        true,
		PermReportsExport:      true,
	},
	domain.RoleManager: {
		PermPropertyRead:       true,
		PermInventoryRead:      true,
		PermInventoryWrite:     true,
		PermRatesRead:          true,
		PermRatesWrite:         true,
		PermBlocksRead:         true,
		PermBlocksWrite:        true,
		PermReservationsRead:   true,
		PermReservationsWrite:  true,
		PermReleasesRead:       true,
		PermReleasesWrite:      true,
		PermChannelsRead:       true,
		PermChannelsWrite:      true,
		PermAuditRead:          true,
		PermOpsRead:            true,
		PermHousekeepingWrite:  true,
		PermMaintenanceRead:    true,
		PermMaintenanceReport:  true,
		PermMaintenanceWrite:   true,
		PermMaintenanceInspect: true,
		PermMaintenanceManage:  true,
		PermGuestsRead:         true,
		PermGuestsWrite:        true,
		PermGuestsNotes:        true,
		PermGuestsMerge:        true,
		PermGuestsExport:       true,
		PermGuestsAnonymize:    true,
		PermCommsRead:          true,
		PermCommsWrite:         true,
		PermCommsReconcile:     true,
		PermCommsTemplates:     true,
		PermReportsRead:        true,
		PermReportsExport:      true,
	},
	domain.RoleStaff: {
		PermPropertyRead:      true,
		PermInventoryRead:     true,
		PermRatesRead:         true,
		PermBlocksRead:        true,
		PermBlocksWrite:       true,
		PermReservationsRead:  true,
		PermReservationsWrite: true,
		PermReleasesRead:      true,
		PermOpsRead:           true,
		PermHousekeepingWrite: true,
		PermMaintenanceRead:   true,
		PermMaintenanceReport: true,
		PermMaintenanceWrite:  true,
		PermGuestsRead:        true,
		PermGuestsWrite:       true,
		PermGuestsNotes:       true,
		PermCommsRead:         true,
		PermCommsWrite:        true,
	},
	domain.RoleMaintenanceStaff: {
		PermPropertyRead:      true,
		PermInventoryRead:     true,
		PermBlocksRead:        true,
		PermOpsRead:           true,
		PermMaintenanceRead:   true,
		PermMaintenanceReport: true,
		PermMaintenanceWrite:  true,
	},
	domain.RoleHousekeepingStaff: {
		PermPropertyRead:      true,
		PermInventoryRead:     true,
		PermOpsRead:           true,
		PermHousekeepingWrite: true,
		PermMaintenanceRead:   true,
		PermMaintenanceReport: true,
	},
	domain.RoleViewer: {
		PermPropertyRead:     true,
		PermInventoryRead:    true,
		PermRatesRead:        true,
		PermBlocksRead:       true,
		PermReservationsRead: true,
		PermReleasesRead:     true,
		PermOpsRead:          true,
		PermMaintenanceRead:  true,
		PermGuestsRead:       true,
		PermReportsRead:      true,
	},
}

// HasPermission returns true when role grants perm (deny-by-default).
func HasPermission(role domain.Role, perm Permission) bool {
	perms, ok := rolePermissions[role]
	if !ok {
		return false
	}
	return perms[perm]
}
