package subscriptions

// region is one row of the static geo-location table the emulator reports for
// the ListLocations endpoint. Real Azure returns the full public-cloud region
// set; a representative subset is enough for a caller validating a region name
// or enumerating placement options.
type region struct {
	name                string
	displayName         string
	regionalDisplayName string
	geography           string
	geographyGroup      string
	latitude            string
	longitude           string
	physicalLocation    string
	pairedRegion        string
}

// regionTable is the fixed set of geo-locations reported to callers. Values
// mirror the real Azure public-cloud metadata so a client that inspects them
// (geography grouping, paired region) sees plausible data.
//
//nolint:gochecknoglobals // static reference table
var regionTable = []region{
	{"eastus", "East US", "(US) East US", "United States", "US", "37.3719", "-79.8164", "Virginia", "westus"},
	{"eastus2", "East US 2", "(US) East US 2", "United States", "US", "36.6681", "-78.3889", "Virginia", "centralus"},
	{"westus", "West US", "(US) West US", "United States", "US", "37.783", "-122.417", "California", "eastus"},
	{"westus2", "West US 2", "(US) West US 2", "United States", "US", "47.233", "-119.852", "Washington", "westcentralus"},
	{"centralus", "Central US", "(US) Central US", "United States", "US", "41.5908", "-93.6208", "Iowa", "eastus2"},
	{"northeurope", "North Europe", "(Europe) North Europe", "Europe", "EU", "53.3478", "-6.2597", "Ireland", "westeurope"},
	{"westeurope", "West Europe", "(Europe) West Europe", "Europe", "EU", "52.3667", "4.9", "Netherlands", "northeurope"},
	{"uksouth", "UK South", "(Europe) UK South", "United Kingdom", "UK", "50.941", "-0.799", "London", "ukwest"},
	{"southeastasia", "Southeast Asia", "(Asia Pacific) Southeast Asia", "Asia Pacific", "APAC", "1.283", "103.833", "Singapore", "eastasia"},
	{"eastasia", "East Asia", "(Asia Pacific) East Asia", "Asia Pacific", "APAC", "22.267", "114.188", "Hong Kong", "southeastasia"},
	{
		"australiaeast", "Australia East", "(Asia Pacific) Australia East", "Asia Pacific",
		"APAC", "-33.86", "151.2094", "New South Wales", "australiasoutheast",
	},
	{"japaneast", "Japan East", "(Asia Pacific) Japan East", "Asia Pacific", "APAC", "35.68", "139.77", "Tokyo", "japanwest"},
}

// locations renders the geo-location list for subscription id in the ARM shape
// (Location objects with id/name/displayName/regionalDisplayName/metadata).
func locations(subscriptionID string) []map[string]any {
	base := basePath + "/" + subscriptionID + "/locations/"
	out := make([]map[string]any, 0, len(regionTable))

	for i := range regionTable {
		reg := &regionTable[i]
		out = append(out, map[string]any{
			"id":                  base + reg.name,
			"name":                reg.name,
			"type":                "Region",
			"displayName":         reg.displayName,
			"regionalDisplayName": reg.regionalDisplayName,
			"subscriptionId":      subscriptionID,
			"metadata": map[string]any{
				"regionType":       "Physical",
				"regionCategory":   "Recommended",
				"geography":        reg.geography,
				"geographyGroup":   reg.geographyGroup,
				"longitude":        reg.longitude,
				"latitude":         reg.latitude,
				"physicalLocation": reg.physicalLocation,
				"pairedRegion": []map[string]any{{
					"name": reg.pairedRegion,
					"id":   base + reg.pairedRegion,
				}},
			},
		})
	}

	return out
}
