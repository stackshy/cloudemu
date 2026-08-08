// Package pricing turns a discovered cloud resource into an approximate monthly
// USD cost, using per-SKU compute/DB rates, per-region multipliers, and flat
// rates for always-on provisioned resources. Values are representative on-demand
// prices (not a live pricing-API feed) — a preview to catch cost surprises
// early, not a billing-accurate figure.
package pricing

// HoursPerMonth turns an hourly rate into a monthly estimate (~730h).
const HoursPerMonth = 730

const (
	defaultComputeHourly = 0.10 // fallback for an unknown compute SKU
	defaultDBHourly      = 0.10 // fallback for an unknown DB class
	defaultRegionMult    = 1.0  // regions absent from the table
)

// Provider identifiers, matching the resourcediscovery Provider field.
const (
	provAWS   = "aws"
	provGCP   = "gcp"
	provAzure = "azure"
)

// computeHourly entries are approximate on-demand rates gathered from public
// pricing knowledge (not a live feed); accurate to ~2 significant figures.
//
//nolint:gochecknoglobals // static pricing table
var computeHourly = map[string]float64{
	"Standard_B1ms":    0.0208,
	"Standard_B1s":     0.0104,
	"Standard_B2ms":    0.0832,
	"Standard_B2s":     0.0416,
	"Standard_B4ms":    0.166,
	"Standard_B8ms":    0.333,
	"Standard_D16s_v3": 0.768,
	"Standard_D16s_v5": 0.768,
	"Standard_D2_v5":   0.088,
	"Standard_D2as_v5": 0.086,
	"Standard_D2s_v3":  0.096,
	"Standard_D2s_v5":  0.096,
	"Standard_D32s_v5": 1.536,
	"Standard_D4_v5":   0.176,
	"Standard_D4as_v5": 0.172,
	"Standard_D4s_v3":  0.192,
	"Standard_D4s_v5":  0.192,
	"Standard_D8_v5":   0.352,
	"Standard_D8as_v5": 0.344,
	"Standard_D8s_v3":  0.384,
	"Standard_D8s_v5":  0.384,
	"Standard_DS1_v2":  0.073,
	"Standard_DS2_v2":  0.146,
	"Standard_DS3_v2":  0.293,
	"Standard_E16s_v5": 1.008,
	"Standard_E2as_v5": 0.113,
	"Standard_E2s_v5":  0.126,
	"Standard_E32s_v5": 2.016,
	"Standard_E4as_v5": 0.226,
	"Standard_E4s_v5":  0.252,
	"Standard_E8as_v5": 0.452,
	"Standard_E8s_v5":  0.504,
	"Standard_F16s_v2": 0.677,
	"Standard_F2s_v2":  0.0846,
	"Standard_F4s_v2":  0.169,
	"Standard_F8s_v2":  0.338,
	"c2-standard-16":   0.8352,
	"c2-standard-4":    0.2088,
	"c2-standard-8":    0.4176,
	"c5.2xlarge":       0.34,
	"c5.4xlarge":       0.68,
	"c5.large":         0.085,
	"c5.xlarge":        0.17,
	"c6i.2xlarge":      0.34,
	"c6i.4xlarge":      0.68,
	"c6i.large":        0.085,
	"c6i.xlarge":       0.17,
	"e2-medium":        0.033503,
	"e2-micro":         0.008376,
	"e2-small":         0.016751,
	"e2-standard-2":    0.067006,
	"e2-standard-4":    0.134012,
	"e2-standard-8":    0.268024,
	"m5.2xlarge":       0.384,
	"m5.4xlarge":       0.768,
	"m5.large":         0.096,
	"m5.xlarge":        0.192,
	"m6i.2xlarge":      0.384,
	"m6i.4xlarge":      0.768,
	"m6i.large":        0.096,
	"m6i.xlarge":       0.192,
	"n1-standard-1":    0.0475,
	"n1-standard-16":   0.76,
	"n1-standard-2":    0.095,
	"n1-standard-4":    0.19,
	"n1-standard-8":    0.38,
	"n2-standard-16":   0.776944,
	"n2-standard-2":    0.097118,
	"n2-standard-4":    0.194236,
	"n2-standard-8":    0.388472,
	"r5.2xlarge":       0.504,
	"r5.large":         0.126,
	"r5.xlarge":        0.252,
	"r6i.2xlarge":      0.504,
	"r6i.large":        0.126,
	"r6i.xlarge":       0.252,
	"t2.2xlarge":       0.3712,
	"t2.large":         0.0928,
	"t2.medium":        0.0464,
	"t2.micro":         0.0116,
	"t2.nano":          0.0058,
	"t2.small":         0.023,
	"t2.xlarge":        0.1856,
	"t3.2xlarge":       0.3328,
	"t3.large":         0.0832,
	"t3.medium":        0.0416,
	"t3.micro":         0.0104,
	"t3.nano":          0.0052,
	"t3.small":         0.0208,
	"t3.xlarge":        0.1664,
	"t3a.2xlarge":      0.3008,
	"t3a.large":        0.0752,
	"t3a.medium":       0.0376,
	"t3a.micro":        0.0094,
	"t3a.nano":         0.0047,
	"t3a.small":        0.0188,
	"t3a.xlarge":       0.1504,
}

// dbHourly entries are approximate on-demand rates gathered from public
// pricing knowledge (not a live feed); accurate to ~2 significant figures.
//
//nolint:gochecknoglobals // static pricing table
var dbHourly = map[string]float64{
	"GP_Gen5_16":        2.96,
	"GP_Gen5_4":         0.74,
	"GP_Gen5_8":         1.48,
	"Standard_B1ms":     0.021,
	"Standard_D2ds_v4":  0.14,
	"Standard_D4ds_v4":  0.28,
	"db-custom-1-3840":  0.085,
	"db-custom-2-7680":  0.17,
	"db-custom-4-15360": 0.34,
	"db-f1-micro":       0.015,
	"db-g1-small":       0.05,
	"db.m5.2xlarge":     0.684,
	"db.m5.large":       0.171,
	"db.m5.xlarge":      0.342,
	"db.r5.2xlarge":     0.96,
	"db.r5.large":       0.24,
	"db.r5.xlarge":      0.48,
	"db.t3.large":       0.136,
	"db.t3.medium":      0.068,
	"db.t3.micro":       0.017,
	"db.t3.small":       0.034,
	"db.t4g.large":      0.129,
	"db.t4g.medium":     0.065,
	"db.t4g.micro":      0.016,
	"db.t4g.small":      0.032,
}

// regionMultiplier entries are approximate on-demand rates gathered from public
// pricing knowledge (not a live feed); accurate to ~2 significant figures.
//
//nolint:gochecknoglobals // static pricing table
var regionMultiplier = map[string]float64{
	"ap-northeast-1":       1.2,
	"ap-south-1":           1.08,
	"ap-southeast-1":       1.16,
	"ap-southeast-2":       1.18,
	"asia-northeast1":      1.2,
	"asia-southeast1":      1.15,
	"australia-southeast1": 1.25,
	"australiaeast":        1.18,
	"ca-central-1":         1.05,
	"centralindia":         1.05,
	"eastus":               1,
	"eastus2":              1,
	"eu-central-1":         1.12,
	"eu-west-1":            1.08,
	"europe-west1":         1.1,
	"europe-west4":         1.1,
	"northeurope":          1.08,
	"sa-east-1":            1.3,
	"southeastasia":        1.15,
	"uksouth":              1.12,
	"us-central1":          1,
	"us-east-1":            1,
	"us-east-2":            1,
	"us-east1":             1,
	"us-west-2":            1,
	"us-west1":             1,
	"westeurope":           1.1,
	"westus2":              1,
}

// flatHourly entries are approximate on-demand rates gathered from public
// pricing knowledge (not a live feed); accurate to ~2 significant figures.
//
//nolint:gochecknoglobals // static pricing table
var flatHourly = map[string]float64{
	"aws:alb":                 0.0225,
	"aws:classic-elb":         0.025,
	"aws:eip-idle":            0.005,
	"aws:eks-control-plane":   0.1,
	"aws:nat-gateway":         0.045,
	"aws:nlb":                 0.0225,
	"azure:aks-control-plane": 0.1,
	"azure:nat-gateway":       0.045,
	"azure:public-ip-idle":    0.005,
	"azure:standard-lb":       0.025,
	"gcp:cloud-nat":           0.044,
	"gcp:forwarding-rule":     0.025,
	"gcp:gke-cluster":         0.1,
	"gcp:static-ip-idle":      0.01,
}

// storageGBMonth entries are approximate on-demand rates gathered from public
// pricing knowledge (not a live feed); accurate to ~2 significant figures.
//
//nolint:gochecknoglobals // static pricing table
var storageGBMonth = map[string]float64{
	"aws:ebs-gp2":                     0.1,
	"aws:ebs-gp3":                     0.08,
	"aws:ebs-io2":                     0.125,
	"aws:ebs-sc1":                     0.015,
	"aws:ebs-st1":                     0.045,
	"aws:s3-glacier-instant":          0.004,
	"aws:s3-standard":                 0.023,
	"aws:s3-standard-ia":              0.0125,
	"azure:blob-archive":              0.00099,
	"azure:blob-cool":                 0.01,
	"azure:blob-hot":                  0.018,
	"azure:managed-disk-premium-ssd":  0.135,
	"azure:managed-disk-standard-hdd": 0.045,
	"azure:managed-disk-standard-ssd": 0.075,
	"gcp:cloud-storage-coldline":      0.004,
	"gcp:cloud-storage-nearline":      0.01,
	"gcp:cloud-storage-standard":      0.02,
	"gcp:pd-balanced":                 0.1,
	"gcp:pd-ssd":                      0.17,
	"gcp:pd-standard":                 0.04,
}

// hourly returns the rate for sku, or def if the SKU is unknown.
func hourly(table map[string]float64, sku string, def float64) float64 {
	if r, ok := table[sku]; ok {
		return r
	}

	return def
}

// regionMult returns the region price multiplier, or 1.0 for unknown regions.
func regionMult(region string) float64 {
	if m, ok := regionMultiplier[region]; ok {
		return m
	}

	return defaultRegionMult
}

// controlPlaneKey maps a provider to its managed-Kubernetes control-plane flat
// rate key.
func controlPlaneKey(provider string) string {
	switch provider {
	case provAWS:
		return "aws:eks-control-plane"
	case provGCP:
		return "gcp:gke-cluster"
	case provAzure:
		return "azure:aks-control-plane"
	default:
		return ""
	}
}

// eipKey maps a provider to its idle-public-IP flat rate key.
func eipKey(provider string) string {
	switch provider {
	case provAWS:
		return "aws:eip-idle"
	case provGCP:
		return "gcp:static-ip-idle"
	case provAzure:
		return "azure:public-ip-idle"
	default:
		return ""
	}
}

// sizeFromProps extracts a resource size in GB from the discovery properties,
// tolerating the several key spellings walkers use. Returns 0 when unknown.
func sizeFromProps(props map[string]any) float64 {
	for _, k := range []string{"SizeGiB", "SizeGB", "sizeGb", "size", "Size", "sizeGiB"} {
		if v, ok := props[k]; ok {
			switch n := v.(type) {
			case float64:
				return n
			case int:
				return float64(n)
			case int64:
				return float64(n)
			}
		}
	}

	return 0
}

// volumeMonthly prices an AWS EBS volume by its type (SKU) and size, or 0 when
// the size or tier is unknown.
func volumeMonthly(provider, sku string, props map[string]any) float64 {
	size := sizeFromProps(props)
	if size <= 0 || provider != provAWS {
		return 0
	}

	rate := storageGBMonth["aws:ebs-"+sku]

	return rate * size
}

// Monthly returns an approximate monthly USD cost for one discovered resource.
// Always-on resources (compute, DB instances, Kubernetes control planes, idle
// IPs, sized volumes) are priced; usage-based services and unknown types
// return 0.
func Monthly(provider, service, resourceType, sku, region string, props map[string]any) float64 {
	mult := regionMult(region)

	switch service + "/" + resourceType {
	case "compute/Instance":
		return hourly(computeHourly, sku, defaultComputeHourly) * mult * HoursPerMonth
	case "relationaldb/DBInstance",
		"relationaldb/SqlInstance",
		"relationaldb/SqlManagedInstance",
		"relationaldb/MySqlFlexibleServer",
		"relationaldb/PostgresFlexibleServer":
		return hourly(dbHourly, sku, defaultDBHourly) * mult * HoursPerMonth
	case "kubernetes/Cluster":
		return flatHourly[controlPlaneKey(provider)] * mult * HoursPerMonth
	case "networking/ElasticIP":
		return flatHourly[eipKey(provider)] * mult * HoursPerMonth
	case "compute/Volume":
		return volumeMonthly(provider, sku, props) * mult
	default:
		return 0
	}
}
