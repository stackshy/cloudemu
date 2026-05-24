package cloudasset

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseFilter(t *testing.T) {
	tests := []struct {
		name     string
		expr     string
		wantSvcs []string
		wantType string
		wantRegn string
		wantTags map[string]string
	}{
		{
			name: "empty",
			expr: "",
		},
		{
			name:     "service compute expands to compute+networking",
			expr:     "service:compute.googleapis.com",
			wantSvcs: []string{"compute", "networking"},
		},
		{
			name:     "service storage",
			expr:     "service:storage.googleapis.com",
			wantSvcs: []string{"storage"},
		},
		{
			name:     "assetType pins service and type",
			expr:     "assetType:storage.googleapis.com/Bucket",
			wantSvcs: []string{"storage"},
			wantType: "Bucket",
		},
		{
			name:     "location",
			expr:     "location:us-central1",
			wantRegn: "us-central1",
		},
		{
			name:     "labels prefix",
			expr:     "labels.env:prod",
			wantTags: map[string]string{"env": "prod"},
		},
		{
			name:     "tags prefix",
			expr:     "tags.env:prod",
			wantTags: map[string]string{"env": "prod"},
		},
		{
			name:     "service + label + location",
			expr:     "service:storage.googleapis.com labels.env:prod location:us-east1",
			wantSvcs: []string{"storage"},
			wantRegn: "us-east1",
			wantTags: map[string]string{"env": "prod"},
		},
		{
			name: "unknown key ignored",
			expr: "unknown:value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseFilter(tt.expr)
			assert.Equal(t, tt.wantSvcs, got.Query.Services)
			assert.Equal(t, tt.wantType, got.Query.Type)
			assert.Equal(t, tt.wantRegn, got.Query.Region)
			assert.Equal(t, tt.wantTags, got.Query.Tags)
			assert.False(t, got.ForceEmpty, "non-contradictory filter must not trip ForceEmpty")
		})
	}
}

func TestParseFilter_ForceEmpty(t *testing.T) {
	tests := []struct {
		name string
		expr string
	}{
		{
			name: "two different services",
			expr: "service:compute.googleapis.com service:storage.googleapis.com",
		},
		{
			name: "two different assetTypes",
			expr: "assetType:storage.googleapis.com/Bucket assetType:firestore.googleapis.com/Database",
		},
		{
			name: "conflicting label values for same key",
			expr: "labels.env:prod labels.env:stage",
		},
		{
			name: "label vs tag conflict on same key",
			expr: "labels.env:prod tags.env:stage",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseFilter(tt.expr)
			assert.True(t, got.ForceEmpty, "expected ForceEmpty for %q", tt.expr)
		})
	}
}

func TestMapGCPAssetType(t *testing.T) {
	tests := []struct {
		assetType string
		wantSvc   string
		wantType  string
	}{
		{"compute.googleapis.com/Instance", "compute", "Instance"},
		{"compute.googleapis.com/Network", "networking", "VPC"},
		{"compute.googleapis.com/Subnetwork", "networking", "Subnet"},
		{"compute.googleapis.com/Firewall", "networking", "SecurityGroup"},
		{"storage.googleapis.com/Bucket", "storage", "Bucket"},
		{"firestore.googleapis.com/Database", "database", "Table"},
		{"cloudfunctions.googleapis.com/Function", "serverless", "Function"},
		{"unknown.googleapis.com/Widget", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.assetType, func(t *testing.T) {
			svc, typ := mapGCPAssetType(tt.assetType)
			assert.Equal(t, tt.wantSvc, svc)
			assert.Equal(t, tt.wantType, typ)
		})
	}
}

func TestPortableToGCPAssetType_Roundtrip(t *testing.T) {
	cases := []struct {
		svc  string
		typ  string
		want string
	}{
		{"compute", "Instance", "compute.googleapis.com/Instance"},
		{"networking", "VPC", "compute.googleapis.com/Network"},
		{"networking", "Subnet", "compute.googleapis.com/Subnetwork"},
		{"networking", "SecurityGroup", "compute.googleapis.com/Firewall"},
		{"storage", "Bucket", "storage.googleapis.com/Bucket"},
		{"database", "Table", "firestore.googleapis.com/Database"},
		{"serverless", "Function", "cloudfunctions.googleapis.com/Function"},
	}

	for _, c := range cases {
		got := portableToGCPAssetType(c.svc, c.typ)
		assert.Equal(t, c.want, got)
	}
}
