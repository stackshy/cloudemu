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

// TestParseKQL_ForceEmpty pins the AND-semantics fix: contradictory
// chained where-clauses must surface ForceEmpty so the handler can return
// zero rows. Real KQL would also yield zero — a resource cannot
// simultaneously have two distinct types or two different tag values for
// the same key.
func TestParseKQL_ForceEmpty(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{
			name:  "two different type filters",
			query: "Resources | where type == 'microsoft.compute/virtualmachines' | where type == 'microsoft.storage/storageaccounts'",
		},
		{
			name:  "same type filter twice (still flagged — second clause is redundant by design)",
			query: "Resources | where type == 'microsoft.compute/virtualmachines' | where type == 'microsoft.compute/virtualmachines'",
		},
		{
			name:  "conflicting tag values for same key",
			query: "Resources | where tags['env'] == 'prod' | where tags['env'] == 'stage'",
		},
		{
			name:  "tag dot syntax conflicts with bracket syntax on same key",
			query: "Resources | where tags['env'] == 'prod' | where tags.env == 'stage'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseKQL(tt.query)
			assert.True(t, got.ForceEmpty, "expected ForceEmpty for contradictory query: %q", tt.query)
		})
	}
}

// TestParseKQL_NoFalsePositiveForceEmpty makes sure agreeing/compatible
// chained clauses do NOT trip the contradiction sentinel.
func TestParseKQL_NoFalsePositiveForceEmpty(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{
			name:  "type + unrelated tag",
			query: "Resources | where type == 'microsoft.compute/virtualmachines' | where tags['env'] == 'prod'",
		},
		{
			name:  "two tag filters on different keys",
			query: "Resources | where tags['env'] == 'prod' | where tags['team'] == 'data'",
		},
		{
			name:  "same tag key+value repeated",
			query: "Resources | where tags['env'] == 'prod' | where tags['env'] == 'prod'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseKQL(tt.query)
			assert.False(t, got.ForceEmpty, "ForceEmpty must not trip for compatible query: %q", tt.query)
		})
	}
}
