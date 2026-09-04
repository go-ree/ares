package auth

import "testing"

func TestBuiltInRoleMatrix(t *testing.T) {
	tests := []struct {
		role      Role
		allowed   []Permission
		forbidden []Permission
	}{
		{
			role:      RoleViewer,
			allowed:   []Permission{PermissionApplicationsRead, PermissionLogsRead},
			forbidden: []Permission{PermissionApplicationsWrite, PermissionReleasesCreate, PermissionSystemSettingsRead},
		},
		{
			role:      RoleDeveloper,
			allowed:   []Permission{PermissionApplicationsWrite, PermissionAppConfigsWrite, PermissionDomainsWrite},
			forbidden: []Permission{PermissionReleasesCreate, PermissionWorkflowsWrite, PermissionSystemSettingsWrite},
		},
		{
			role:      RoleReleaser,
			allowed:   []Permission{PermissionReleasesCreate, PermissionTasksWrite, PermissionLogsRead},
			forbidden: []Permission{PermissionApplicationsWrite, PermissionAppConfigsWrite, PermissionSystemSettingsWrite},
		},
		{
			role:    RoleAdmin,
			allowed: []Permission{PermissionApplicationsWrite, PermissionReleasesCreate, PermissionWorkflowsWrite, PermissionAuditRead},
		},
	}
	for _, test := range tests {
		t.Run(string(test.role), func(t *testing.T) {
			for _, permission := range test.allowed {
				if !RoleHasPermission(test.role, permission) {
					t.Errorf("%s should allow %s", test.role, permission)
				}
			}
			for _, permission := range test.forbidden {
				if RoleHasPermission(test.role, permission) {
					t.Errorf("%s should deny %s", test.role, permission)
				}
			}
		})
	}
}

func TestPermissionsForRoleReturnsIndependentSortedCopy(t *testing.T) {
	first := PermissionsForRole(RoleViewer)
	if len(first) < 2 {
		t.Fatal("viewer permission set is unexpectedly empty")
	}
	for index := 1; index < len(first); index++ {
		if first[index-1] >= first[index] {
			t.Fatalf("permissions are not sorted: %v", first)
		}
	}
	first[0] = PermissionAuditRead
	if RoleHasPermission(RoleViewer, PermissionAuditRead) {
		t.Fatal("caller mutated the role permission catalog")
	}
}
