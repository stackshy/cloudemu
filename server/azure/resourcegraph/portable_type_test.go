package resourcegraph

import "testing"

// portableToAzureType is what stamps the `type` field on every Resource Graph
// row, so a client filtering by the real ARM type must get the exact string
// back. The kubernetes mappings were previously only exercised in the reverse
// (mapAzureType) direction.
func TestPortableToAzureType(t *testing.T) {
	cases := []struct {
		svc, typ, want string
	}{
		{"compute", "Instance", "microsoft.compute/virtualmachines"},
		{"databricks", "Workspace", "microsoft.databricks/workspaces"},
		{"kubernetes", "Cluster", "microsoft.containerservice/managedclusters"},
		{"kubernetes", "NodeGroup", "microsoft.containerservice/managedclusters/agentpools"},
		{"relationaldb", "SqlServer", "microsoft.sql/servers"},
		{"relationaldb", "SqlManagedInstance", "microsoft.sql/managedinstances"},
		{"relationaldb", "MySqlFlexibleServer", "microsoft.dbformysql/flexibleservers"},
		{"relationaldb", "PostgresFlexibleServer", "microsoft.dbforpostgresql/flexibleservers"},
	}

	for _, c := range cases {
		if got := portableToAzureType(c.svc, c.typ); got != c.want {
			t.Errorf("portableToAzureType(%q,%q) = %q, want %q", c.svc, c.typ, got, c.want)
		}
	}
}
