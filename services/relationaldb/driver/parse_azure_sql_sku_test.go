package driver

import "testing"

func TestParseAzureSQLSKU(t *testing.T) {
	tests := []struct {
		name         string
		sku          string
		wantTier     string
		wantFamily   string
		wantCapacity int
	}{
		{name: "vcore general purpose", sku: "GP_Gen5_2", wantTier: "GeneralPurpose", wantFamily: "Gen5", wantCapacity: 2},
		{name: "vcore general purpose larger", sku: "GP_Gen5_8", wantTier: "GeneralPurpose", wantFamily: "Gen5", wantCapacity: 8},
		{name: "vcore serverless", sku: "GP_S_Gen5_2", wantTier: "GeneralPurpose", wantFamily: "Gen5", wantCapacity: 2},
		{name: "vcore business critical", sku: "BC_Gen5_4", wantTier: "BusinessCritical", wantFamily: "Gen5", wantCapacity: 4},
		{name: "vcore hyperscale", sku: "HS_Gen5_2", wantTier: "Hyperscale", wantFamily: "Gen5", wantCapacity: 2},
		{name: "vcore fsv2 family", sku: "GP_Fsv2_8", wantTier: "GeneralPurpose", wantFamily: "Fsv2", wantCapacity: 8},
		{name: "elastic pool sku no capacity", sku: "GP_Gen5", wantTier: "GeneralPurpose"},
		{name: "dtu standard S0", sku: "S0", wantTier: "Standard"},
		{name: "dtu standard S3", sku: "S3", wantTier: "Standard"},
		{name: "dtu premium P2", sku: "P2", wantTier: "Premium"},
		{name: "dtu basic", sku: "Basic", wantTier: "Basic"},
		{name: "empty", sku: ""},
		{name: "unrecognized pool sku", sku: "StandardPool"},
		{name: "premium without digits", sku: "Premium"},
		{name: "bare vcore prefix", sku: "GP", wantTier: "GeneralPurpose"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tier, family, capacity := ParseAzureSQLSKU(tc.sku)

			if tier != tc.wantTier {
				t.Errorf("tier = %q, want %q", tier, tc.wantTier)
			}

			if family != tc.wantFamily {
				t.Errorf("family = %q, want %q", family, tc.wantFamily)
			}

			if capacity != tc.wantCapacity {
				t.Errorf("capacity = %d, want %d", capacity, tc.wantCapacity)
			}
		})
	}
}
