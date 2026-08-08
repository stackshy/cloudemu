package pricing_test

import (
	"math"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/pricing"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 0.01 }

func TestMonthlyPerSKU(t *testing.T) {
	// A known SKU is priced at its hourly rate × 730 in the base region.
	// t3.micro = 0.0104/hr → ~7.59/mo.
	if got := pricing.Monthly("aws", "compute", "Instance", "t3.micro", "us-east-1", nil); !approx(got, 0.0104*pricing.HoursPerMonth) {
		t.Fatalf("t3.micro us-east-1 = %.4f, want ~%.4f", got, 0.0104*pricing.HoursPerMonth)
	}

	// A larger SKU costs more than a smaller one.
	if pricing.Monthly("aws", "compute", "Instance", "m5.4xlarge", "us-east-1", nil) <=
		pricing.Monthly("aws", "compute", "Instance", "t3.micro", "us-east-1", nil) {
		t.Fatal("m5.4xlarge should cost more than t3.micro")
	}

	// Azure and GCP SKUs are priced too.
	if pricing.Monthly("azure", "compute", "Instance", "Standard_D4s_v5", "eastus", nil) <= 0 {
		t.Fatal("Azure D4s_v5 should be priced")
	}
	if pricing.Monthly("gcp", "compute", "Instance", "n2-standard-4", "us-central1", nil) <= 0 {
		t.Fatal("GCP n2-standard-4 should be priced")
	}
}

func TestMonthlyPerRegion(t *testing.T) {
	base := pricing.Monthly("aws", "compute", "Instance", "m5.large", "us-east-1", nil)
	saoPaulo := pricing.Monthly("aws", "compute", "Instance", "m5.large", "sa-east-1", nil)

	// sa-east-1 has a >1 multiplier, so it must cost more than us-east-1.
	if saoPaulo <= base {
		t.Fatalf("sa-east-1 (%.2f) should exceed us-east-1 (%.2f)", saoPaulo, base)
	}
}

func TestMonthlyUnknownSKUFallsBackNotZero(t *testing.T) {
	if got := pricing.Monthly("aws", "compute", "Instance", "made-up.mega", "us-east-1", nil); got <= 0 {
		t.Fatalf("unknown SKU should use the default rate, got %.4f", got)
	}
}

func TestMonthlyDBAndFlat(t *testing.T) {
	if pricing.Monthly("aws", "relationaldb", "DBInstance", "db.m5.large", "us-east-1", nil) <= 0 {
		t.Fatal("RDS db.m5.large should be priced")
	}
	if pricing.Monthly("aws", "kubernetes", "Cluster", "", "us-east-1", nil) <= 0 {
		t.Fatal("EKS control plane should be priced")
	}
	if pricing.Monthly("aws", "networking", "ElasticIP", "", "us-east-1", nil) <= 0 {
		t.Fatal("idle EIP should be priced")
	}
}

func TestMonthlyVolumeBySize(t *testing.T) {
	// Uses the exact props key the volume walker emits ("diskSizeGB"), so the
	// wired discovery→pricing path is exercised, not a synthetic key.
	// A 100 GB gp3 volume ≈ 0.08 × 100 = $8/mo.
	got := pricing.Monthly("aws", "compute", "Volume", "gp3", "us-east-1", map[string]any{"diskSizeGB": float64(100)})
	if !approx(got, 0.08*100) {
		t.Fatalf("100GB gp3 = %.2f, want ~8.00", got)
	}

	// No size → not estimated.
	if got := pricing.Monthly("aws", "compute", "Volume", "gp3", "us-east-1", nil); got != 0 {
		t.Fatalf("volume without size = %.2f, want 0", got)
	}
}

// Azure managed disks and GCE PDs must price too — the walker's VolumeType maps
// to the provider's disk rate, not a hardcoded AWS prefix.
func TestMonthlyVolumeAzureGCP(t *testing.T) {
	// Azure Premium_LRS 100 GB ≈ 0.135 × 100 = $13.50/mo.
	got := pricing.Monthly("azure", "compute", "Volume", "Premium_LRS", "eastus",
		map[string]any{"diskSizeGB": float64(100)})
	if !approx(got, 0.135*100) {
		t.Fatalf("100GB Premium_LRS = %.2f, want ~13.50", got)
	}

	// GCP pd-ssd 50 GB ≈ 0.17 × 50 = $8.50/mo.
	got = pricing.Monthly("gcp", "compute", "Volume", "pd-ssd", "us-central1",
		map[string]any{"diskSizeGB": float64(50)})
	if !approx(got, 0.17*50) {
		t.Fatalf("50GB pd-ssd = %.2f, want ~8.50", got)
	}
}

// Load balancers and NAT gateways are priced by their flat provisioned rate,
// per provider.
func TestMonthlyLoadBalancerAndNAT(t *testing.T) {
	if got := pricing.Monthly("aws", "loadbalancer", "LoadBalancer", "", "us-east-1", nil); got <= 0 {
		t.Fatal("AWS load balancer should be priced")
	}

	if got := pricing.Monthly("gcp", "networking", "NatGateway", "", "us-central1", nil); got <= 0 {
		t.Fatal("GCP NAT gateway should be priced")
	}
}

func TestMonthlyUsageBasedIsZero(t *testing.T) {
	for _, tc := range [][2]string{{"storage", "Bucket"}, {"database", "Table"}, {"networking", "VPC"}} {
		if got := pricing.Monthly("aws", tc[0], tc[1], "", "us-east-1", nil); got != 0 {
			t.Fatalf("%s/%s = %.4f, want 0 (usage-based)", tc[0], tc[1], got)
		}
	}
}
