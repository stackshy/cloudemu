package resourcegraph

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseKQL(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantSvcs  []string
		wantType  string
		wantRegn  string
		wantTags  map[string]string
		wantLimit int
	}{
		{
			name:     "empty query",
			query:    "",
			wantSvcs: nil,
		},
		{
			name:     "only Resources",
			query:    "Resources",
			wantSvcs: nil,
		},
		{
			name:     "type equality",
			query:    "Resources | where type == 'microsoft.compute/virtualmachines'",
			wantSvcs: []string{"compute"},
			wantType: "Instance",
		},
		{
			name:     "type case-insensitive equality",
			query:    "Resources | where type =~ 'Microsoft.Network/VirtualNetworks'",
			wantSvcs: []string{"networking"},
			wantType: "VPC",
		},
		{
			name:     "location",
			query:    "Resources | where location == 'eastus'",
			wantRegn: "eastus",
		},
		{
			name:     "tag bracket",
			query:    "Resources | where tags['env'] == 'prod'",
			wantTags: map[string]string{"env": "prod"},
		},
		{
			name:     "tag dot",
			query:    "Resources | where tags.env == 'prod'",
			wantTags: map[string]string{"env": "prod"},
		},
		{
			name:     "type + tag combined",
			query:    "Resources | where type == 'microsoft.storage/storageaccounts' | where tags['env'] == 'prod'",
			wantSvcs: []string{"storage"},
			wantType: "Bucket",
			wantTags: map[string]string{"env": "prod"},
		},
		{
			name:      "limit clause",
			query:     "Resources | limit 50",
			wantLimit: 50,
		},
		{
			name:      "take clause",
			query:     "Resources | take 10",
			wantLimit: 10,
		},
		{
			name:     "project clause ignored",
			query:    "Resources | where type == 'microsoft.web/sites' | project id, name",
			wantSvcs: []string{"serverless"},
			wantType: "Function",
		},
		{
			name:  "resourceGroup is a no-op",
			query: "Resources | where resourceGroup == 'mygroup'",
		},
		{
			name:  "unknown predicate tolerated",
			query: "Resources | where sku.tier == 'Premium'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseKQL(tt.query)
			assert.Equal(t, tt.wantSvcs, got.Query.Services)
			assert.Equal(t, tt.wantType, got.Query.Type)
			assert.Equal(t, tt.wantRegn, got.Query.Region)
			assert.Equal(t, tt.wantTags, got.Query.Tags)
			assert.Equal(t, tt.wantLimit, got.Limit)
		})
	}
}

func TestMapAzureType(t *testing.T) {
	tests := []struct {
		azureType string
		wantSvc   string
		wantType  string
	}{
		{"microsoft.compute/virtualmachines", "compute", "Instance"},
		{"microsoft.network/virtualnetworks", "networking", "VPC"},
		{"microsoft.network/subnets", "networking", "Subnet"},
		{"microsoft.network/networksecuritygroups", "networking", "SecurityGroup"},
		{"microsoft.storage/storageaccounts", "storage", "Bucket"},
		{"microsoft.documentdb/databaseaccounts", "database", "Table"},
		{"microsoft.web/sites", "serverless", "Function"},
		{"microsoft.unknown/widgets", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.azureType, func(t *testing.T) {
			svc, typ := mapAzureType(tt.azureType)
			assert.Equal(t, tt.wantSvc, svc)
			assert.Equal(t, tt.wantType, typ)
		})
	}
}
