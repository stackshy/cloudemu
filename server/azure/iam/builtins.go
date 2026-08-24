package iam

// Well-known Azure built-in role definition GUIDs. These are stable across
// every tenant and scope in real Azure, so tools and IaC reference them by
// their fixed GUID (e.g. a Terraform azurerm_role_assignment pointing at the
// "Contributor" role). Seeding them lets a RoleAssignment reference a built-in
// role without the caller first having to define a custom role.
const (
	builtInOwnerID       = "8e3af657-a8ff-443c-a75c-2fe8c4bcb635"
	builtInContributorID = "b24988ac-6180-42a0-ab88-20f7382dd24c"
	builtInReaderID      = "acdd72a7-3385-48ef-bd42-f606fba81ae7"

	// builtInCreatedOn is a fixed timestamp for built-in roles. Real Azure
	// reports the (very old) date the role shipped; the exact value does not
	// matter to consumers, but it must be stable so GETs are deterministic.
	builtInCreatedOn = "2017-12-05T00:00:00.0000000Z"

	// builtInAssignableScope is the root scope built-in roles are assignable
	// at — real built-ins carry assignableScopes ["/"], meaning they can be
	// assigned at any scope in the hierarchy.
	builtInAssignableScope = "/"
)

// builtInRoleDefinitions returns the seeded built-in role definitions keyed by
// their fixed GUID. It is a constructor (not a package global) so it satisfies
// the no-globals lint rule and so each Handler owns an independent copy.
//
// The permission sets mirror the real built-in roles closely enough for
// authorization-shaped emulator tests: Owner grants everything, Contributor
// grants everything except managing access (Microsoft.Authorization writes and
// role-assignment/deny-assignment writes), and Reader grants read-only access.
func builtInRoleDefinitions() map[string]roleDefinitionProperties {
	return map[string]roleDefinitionProperties{
		builtInOwnerID: {
			RoleName:    "Owner",
			Description: "Grants full access to manage all resources, including the ability to assign roles in Azure RBAC.",
			Type:        "BuiltInRole",
			Permissions: []permission{{
				Actions:     []string{"*"},
				NotActions:  []string{},
				DataActions: []string{},
			}},
			AssignableScopes: []string{builtInAssignableScope},
			CreatedOn:        builtInCreatedOn,
			UpdatedOn:        builtInCreatedOn,
		},
		builtInContributorID: {
			RoleName:    "Contributor",
			Description: "Grants full access to manage all resources, but does not allow you to assign roles in Azure RBAC.",
			Type:        "BuiltInRole",
			Permissions: []permission{{
				Actions: []string{"*"},
				NotActions: []string{
					"Microsoft.Authorization/*/Delete",
					"Microsoft.Authorization/*/Write",
					"Microsoft.Authorization/elevateAccess/Action",
					"Microsoft.Blueprint/blueprintAssignments/write",
					"Microsoft.Blueprint/blueprintAssignments/delete",
				},
				DataActions: []string{},
			}},
			AssignableScopes: []string{builtInAssignableScope},
			CreatedOn:        builtInCreatedOn,
			UpdatedOn:        builtInCreatedOn,
		},
		builtInReaderID: {
			RoleName:    "Reader",
			Description: "View all resources, but does not allow you to make any changes.",
			Type:        "BuiltInRole",
			Permissions: []permission{{
				Actions:     []string{"*/read"},
				NotActions:  []string{},
				DataActions: []string{},
			}},
			AssignableScopes: []string{builtInAssignableScope},
			CreatedOn:        builtInCreatedOn,
			UpdatedOn:        builtInCreatedOn,
		},
	}
}
