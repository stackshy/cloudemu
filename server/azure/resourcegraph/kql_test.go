package resourcegraph

import (
	"reflect"
	"testing"
)

func kqlRows() []row {
	return []row{
		{"name": "vm1", "type": "microsoft.compute/virtualmachines", "resourceGroup": "rga", "location": "eastus",
			"tags": map[string]string{"env": "prod"}, "sku": map[string]any{"tier": "Premium"}},
		{"name": "disk1", "type": "microsoft.compute/disks", "resourceGroup": "rga", "location": "westus",
			"tags": map[string]string{}},
		{"name": "vnet1", "type": "microsoft.network/virtualnetworks", "resourceGroup": "rgb", "location": "eastus",
			"tags": map[string]string{"env": "dev"}},
		{"name": "acr1", "type": "microsoft.containerregistry/registries", "resourceGroup": "RGB", "location": "westus",
			"tags": map[string]string{"env": "prod"}},
	}
}

func names(rows []row) []string {
	out := []string{}
	for _, r := range rows {
		out = append(out, valueString(r["name"]))
	}

	return out
}

func TestKQLWhere(t *testing.T) {
	tests := []struct {
		query string
		want  []string
	}{
		{"Resources", []string{"vm1", "disk1", "vnet1", "acr1"}},
		{"", []string{"vm1", "disk1", "vnet1", "acr1"}},
		{"where resourceGroup == 'rga'", []string{"vm1", "disk1"}},
		{"resources | where resourceGroup == 'rgb'", []string{"vnet1"}},
		{"Resources | where resourceGroup =~ 'rgb'", []string{"vnet1", "acr1"}},
		{"Resources | where resourceGroup != 'rga'", []string{"vnet1", "acr1"}},
		{"Resources | where type == 'microsoft.compute/disks'", []string{"disk1"}},
		{"Resources | where type =~ 'Microsoft.ContainerRegistry/registries'", []string{"acr1"}},
		{"Resources | where type in~ ('Microsoft.Compute/disks', 'microsoft.network/virtualnetworks')", []string{"disk1", "vnet1"}},
		{"Resources | where location !in ('eastus')", []string{"disk1", "acr1"}},
		{"Resources | where name == 'vm1' or name == 'acr1'", []string{"vm1", "acr1"}},
		{"Resources | where resourceGroup =~ 'rgb' and tags['env'] == 'prod'", []string{"acr1"}},
		{"Resources | where tags.env == 'prod' and (location == 'eastus' or name startswith 'acr')", []string{"vm1", "acr1"}},
		{"Resources | where sku.tier == 'Premium'", []string{"vm1"}},
		{"Resources | where type == 'microsoft.compute/disks' | where type == 'microsoft.network/virtualnetworks'", []string{}},
		{"Resources | where name contains 'NET'", []string{"vnet1"}},
		{"Resources | order by name asc | take 2", []string{"acr1", "disk1"}},
		{"Resources | sort by name | limit 1", []string{"vnet1"}},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			q, err := parseKQL(tt.query)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}

			if got := names(q.run(kqlRows())); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestKQLProjectAndCount(t *testing.T) {
	q, err := parseKQL("Resources | where name == 'vm1' | project name, rg = resourceGroup, tags.env")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	want := []row{{"name": "vm1", "rg": "rga", "tags_env": "prod"}}
	if got := q.run(kqlRows()); !reflect.DeepEqual(got, want) {
		t.Fatalf("project = %v, want %v", got, want)
	}

	for query, want := range map[string]row{
		"Resources | where location == 'westus' | count": {"Count": 2},
		"Resources | summarize count()":                   {"count_": 4},
	} {
		q, err := parseKQL(query)
		if err != nil {
			t.Fatalf("parse %q: %v", query, err)
		}

		if got := q.run(kqlRows()); !reflect.DeepEqual(got, []row{want}) {
			t.Fatalf("%q = %v, want %v", query, got, want)
		}
	}
}

func TestKQLTables(t *testing.T) {
	q, err := parseKQL("ResourceContainers | where type == 'x'")
	if err != nil || q.table != tableContainers {
		t.Fatalf("ResourceContainers: table=%v err=%v", q, err)
	}
}

func TestKQLInvalid(t *testing.T) {
	for _, query := range []string{
		"Resources | where",
		"Resources | where name = 'x'",
		"Resources | where name == 'x",
		"Resources | extend x = 1",
		"Resources | summarize count() by type",
		"Resources | limit",
		"Widgets",
		"Resources | where (name == 'x'",
		"Resources | where type in~ 'x'",
	} {
		if _, err := parseKQL(query); err == nil {
			t.Errorf("parseKQL(%q) accepted an invalid query", query)
		}
	}
}
