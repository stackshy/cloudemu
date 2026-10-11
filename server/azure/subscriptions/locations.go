package subscriptions

import "strings"

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
//nolint:gochecknoglobals,goconst // static reference table
var regionTable = []region{
	{"eastus", "East US", "(US) East US", "United States", "US", "37.3719", "-79.8164", "Virginia", "westus"},
	{"eastus2", "East US 2", "(US) East US 2", "United States", "US", "36.6681", "-78.3889", "Virginia", "centralus"},
	{"westus", "West US", "(US) West US", "United States", "US", "37.783", "-122.417", "California", "eastus"},
	{"westus2", "West US 2", "(US) West US 2", "United States", "US", "47.233", "-119.852", "Washington", "westcentralus"},
	{"centralus", "Central US", "(US) Central US", "United States", "US", "41.5908", "-93.6208", "Iowa", "eastus2"},
	{"northeurope", "North Europe", "(Europe) North Europe", "Europe", "EU", "53.3478", "-6.2597", "Ireland", "westeurope"},
	{"westeurope", "West Europe", "(Europe) West Europe", "Europe", "EU", "52.3667", "4.9", "Netherlands", "northeurope"},
	{"uksouth", "UK South", "(Europe) UK South", "United Kingdom", "UK", "50.941", "-0.799", "London", "ukwest"},
	{
		"southeastasia", "Southeast Asia", "(Asia Pacific) Southeast Asia",
		"Asia Pacific", "APAC", "1.283", "103.833", "Singapore", "eastasia",
	},
	{"eastasia", "East Asia", "(Asia Pacific) East Asia", "Asia Pacific", "APAC", "22.267", "114.188", "Hong Kong", "southeastasia"},
	{
		"australiaeast", "Australia East", "(Asia Pacific) Australia East", "Asia Pacific",
		"APAC", "-33.86", "151.2094", "New South Wales", "australiasoutheast",
	},
	{"japaneast", "Japan East", "(Asia Pacific) Japan East", "Asia Pacific", "APAC", "35.68", "139.77", "Tokyo", "japanwest"},
	{"westcentralus", "West Central US", "(US) West Central US", "United States", "US", "40.89", "-110.234", "Wyoming", "westus2"},
	{"southcentralus", "South Central US", "(US) South Central US", "United States", "US", "29.4167", "-98.5", "Texas", "northcentralus"},
	{
		"northcentralus", "North Central US", "(US) North Central US",
		"United States", "US", "41.8819", "-87.6278", "Illinois", "southcentralus",
	},
	{"westus3", "West US 3", "(US) West US 3", "United States", "US", "33.448376", "-112.074036", "Arizona", "eastus"},
	{"canadacentral", "Canada Central", "(Canada) Canada Central", "Canada", "CA", "43.653", "-79.383", "Toronto", "canadaeast"},
	{"canadaeast", "Canada East", "(Canada) Canada East", "Canada", "CA", "46.817", "-71.217", "Quebec", "canadacentral"},
	{
		"brazilsouth", "Brazil South", "(South America) Brazil South",
		"Brazil", "BR", "-23.55", "-46.633", "Sao Paulo State", "southcentralus",
	},
	{"ukwest", "UK West", "(Europe) UK West", "United Kingdom", "UK", "53.427", "-3.084", "Cardiff", "uksouth"},
	{"francecentral", "France Central", "(Europe) France Central", "France", "FR", "46.3772", "2.373", "Paris", "francesouth"},
	{
		"germanywestcentral", "Germany West Central", "(Europe) Germany West Central",
		"Germany", "DE", "50.1109", "8.6821", "Frankfurt", "germanynorth",
	},
	{
		"switzerlandnorth", "Switzerland North", "(Europe) Switzerland North",
		"Switzerland", "CH", "47.451542", "8.564572", "Zurich", "switzerlandwest",
	},
	{"norwayeast", "Norway East", "(Europe) Norway East", "Norway", "NO", "59.913868", "10.752245", "Oslo", "norwaywest"},
	{"swedencentral", "Sweden Central", "(Europe) Sweden Central", "Sweden", "SE", "60.67488", "17.14127", "Gavle", "swedensouth"},
	{"italynorth", "Italy North", "(Europe) Italy North", "Italy", "IT", "45.46427", "9.18951", "Milan", "swedencentral"},
	{"polandcentral", "Poland Central", "(Europe) Poland Central", "Poland", "PL", "52.23", "21.01", "Warsaw", "northeurope"},
	{"spaincentral", "Spain Central", "(Europe) Spain Central", "Spain", "ES", "40.4168", "-3.7038", "Madrid", "northeurope"},
	{
		"australiasoutheast", "Australia Southeast", "(Asia Pacific) Australia Southeast",
		"Asia Pacific", "APAC", "-37.8136", "144.9631", "Victoria", "australiaeast",
	},
	{
		"australiacentral", "Australia Central", "(Asia Pacific) Australia Central",
		"Asia Pacific", "APAC", "-35.3075", "149.1244", "Canberra", "australiacentral2",
	},
	{"japanwest", "Japan West", "(Asia Pacific) Japan West", "Asia Pacific", "APAC", "34.6939", "135.5022", "Osaka", "japaneast"},
	{"koreacentral", "Korea Central", "(Asia Pacific) Korea Central", "Asia Pacific", "APAC", "37.5665", "126.9780", "Seoul", "koreasouth"},
	{"koreasouth", "Korea South", "(Asia Pacific) Korea South", "Asia Pacific", "APAC", "35.1796", "129.0756", "Busan", "koreacentral"},
	{"centralindia", "Central India", "(Asia Pacific) Central India", "India", "IN", "18.5822", "73.9197", "Pune", "southindia"},
	{"southindia", "South India", "(Asia Pacific) South India", "India", "IN", "12.9822", "80.1636", "Chennai", "centralindia"},
	{"westindia", "West India", "(Asia Pacific) West India", "India", "IN", "19.088", "72.868", "Mumbai", "southindia"},
	{"uaenorth", "UAE North", "(Middle East) UAE North", "UAE", "AE", "25.266666", "55.316666", "Dubai", "uaecentral"},
	{
		"southafricanorth", "South Africa North", "(Africa) South Africa North",
		"South Africa", "ZA", "-25.731340", "28.218370", "Johannesburg", "southafricawest",
	},
	{"qatarcentral", "Qatar Central", "(Middle East) Qatar Central", "Qatar", "QA", "25.3548", "51.1839", "Doha", "westeurope"},
	{"israelcentral", "Israel Central", "(Middle East) Israel Central", "Israel", "IL", "31.0461", "34.8516", "Israel", "northeurope"},
	{"mexicocentral", "Mexico Central", "(Mexico) Mexico Central", "Mexico", "MX", "20.5888", "-100.3899", "Queretaro", "southcentralus"},
	{
		"newzealandnorth", "New Zealand North", "(Asia Pacific) New Zealand North",
		"Asia Pacific", "APAC", "-36.8485", "174.7633", "Auckland", "australiaeast",
	},
	{
		"brazilsoutheast", "Brazil Southeast", "(South America) Brazil Southeast",
		"South America", "BR", "-22.9028", "-43.2075", "Rio", "brazilsouth",
	},
	{"chilecentral", "Chile Central", "(South America) Chile Central", "South America", "CL", "-33.4489", "-70.6693", "Santiago", ""},
	{"indonesiacentral", "Indonesia Central", "(Asia Pacific) Indonesia Central", "Asia Pacific", "ID", "-6.2088", "106.8456", "Jakarta", ""},
	{"malaysiawest", "Malaysia West", "(Asia Pacific) Malaysia West", "Asia Pacific", "MY", "3.139", "101.6869", "Kuala Lumpur", ""},
	{"jioindiawest", "Jio India West", "(Asia Pacific) Jio India West", "Asia Pacific", "IN", "22.4707", "70.0577", "Jamnagar", ""},
	{"belgiumcentral", "Belgium Central", "(Europe) Belgium Central", "Europe", "BE", "50.8503", "4.3517", "Brussels", ""},
	{"austriaeast", "Austria East", "(Europe) Austria East", "Europe", "AT", "48.2082", "16.3738", "Vienna", ""},
	{"denmarkeast", "Denmark East", "(Europe) Denmark East", "Europe", "DK", "55.6761", "12.5683", "Copenhagen", ""},
	{"taiwannorth", "Taiwan North", "(Asia Pacific) Taiwan North", "Asia Pacific", "TW", "25.033", "121.5654", "Taipei", ""},
	{
		"francesouth", "France South", "(Europe) France South",
		"Europe", "EU", "43.8345", "2.1972", "Marseille", "francecentral",
	},
	{
		"germanynorth", "Germany North", "(Europe) Germany North",
		"Europe", "EU", "53.073635", "8.806422", "Berlin", "germanywestcentral",
	},
	{
		"swedensouth", "Sweden South", "(Europe) Sweden South",
		"Europe", "EU", "55.6059", "13.0007", "Malmo", "swedencentral",
	},
	{
		"norwaywest", "Norway West", "(Europe) Norway West",
		"Europe", "EU", "58.969975", "5.733107", "Norway", "norwayeast",
	},
	{
		"switzerlandwest", "Switzerland West", "(Europe) Switzerland West",
		"Europe", "EU", "46.204391", "6.143158", "Geneva", "switzerlandnorth",
	},
	{
		"uaecentral", "UAE Central", "(Middle East) UAE Central",
		"Middle East", "ME", "24.466667", "54.366669", "Abu Dhabi", "uaenorth",
	},
	{
		"southafricawest", "South Africa West", "(Africa) South Africa West",
		"South Africa", "ZA", "-34.075691", "18.843266", "Cape Town", "southafricanorth",
	},
	{
		"australiacentral2", "Australia Central 2", "(Asia Pacific) Australia Central 2",
		"Asia Pacific", "APAC", "-35.3075", "149.1244", "Canberra", "australiacentral",
	},
	{
		"eastus2euap", "East US 2 EUAP", "(US) East US 2 EUAP",
		"United States", "US", "36.6681", "-78.3889", "", "centraluseuap",
	},
	{
		"centraluseuap", "Central US EUAP", "(US) Central US EUAP",
		"United States", "US", "41.5908", "-93.6208", "", "eastus2euap",
	},
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
				"pairedRegion":     pairedRegions(base, reg.pairedRegion),
			},
		})
	}

	return out
}

// IsRegion reports whether name is a known Azure region. The comparison is
// case-insensitive and ignores spaces, so "West US 2" matches "westus2".
// pairedRegions is a region's pair list; regions without a pair report an
// empty list, as ARM does.
func pairedRegions(base, paired string) []map[string]any {
	if paired == "" {
		return []map[string]any{}
	}

	return []map[string]any{{"name": paired, "id": base + paired}}
}

// CanonicalLocation returns the region's programmatic name ("West US 2" ->
// "westus2"), or name unchanged when it is not a known region.
func CanonicalLocation(name string) string {
	want := NormalizeLocation(name)

	for i := range regionTable {
		if regionTable[i].name == want {
			return want
		}
	}

	return name
}

func IsRegion(name string) bool {
	want := NormalizeLocation(name)

	for i := range regionTable {
		if regionTable[i].name == want {
			return true
		}
	}

	return false
}

// NormalizeLocation lowercases a location and removes spaces, the form ARM
// compares regions in.
func NormalizeLocation(name string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), " ", ""))
}
