package main

import (
	"path/filepath"
	"testing"
)

// loadServices runs the read-only half of the generator against the real repo.
func loadServices(t *testing.T) map[string]*Service {
	t.Helper()

	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}

	services, err := collectServices(filepath.Join(root, "services"))
	if err != nil {
		t.Fatalf("collectServices: %v", err)
	}

	if err := attachProviders(root, services); err != nil {
		t.Fatalf("attachProviders: %v", err)
	}

	return services
}

func TestProviderNativeNamesResolve(t *testing.T) {
	services := loadServices(t)

	cases := map[string]map[string]string{
		"storage": {"aws": "S3", "azure": "BlobStorage", "gcp": "GCS"},
		"compute": {"aws": "EC2", "azure": "VirtualMachines", "gcp": "GCE"},
		// monitoring must resolve to the real metrics service, not a consumer
		// like EKS that merely imports monitoring/driver.
		"monitoring": {"aws": "CloudWatch"},
	}

	for svcName, want := range cases {
		svc, ok := services[svcName]
		if !ok {
			t.Fatalf("service %q not found", svcName)
		}

		for prov, native := range want {
			if got := svc.Providers[prov]; got != native {
				t.Errorf("%s[%s] = %q, want %q", svcName, prov, got, native)
			}
		}
	}
}

// TestEmbeddedInterfacesFlatten guards the facade services whose primary
// interface embeds sub-interfaces (SageMaker, AzureAI): a broken flattener
// reports zero operations.
func TestEmbeddedInterfacesFlatten(t *testing.T) {
	services := loadServices(t)

	for _, name := range []string{"sagemaker", "azureai", "storage"} {
		svc, ok := services[name]
		if !ok {
			t.Fatalf("service %q not found", name)
		}

		if len(svc.Operations) == 0 {
			t.Errorf("service %q has 0 operations; embed-flattening likely broken", name)
		}
	}
}

// TestOCIReportsNoImplementedServices is the honesty guarantee: OCI declares
// service fields but its factory constructs none, so the docs must not credit
// it with any service.
func TestOCIReportsNoImplementedServices(t *testing.T) {
	services := loadServices(t)

	for name, svc := range services {
		if native := svc.Providers["oci"]; native != "" {
			t.Errorf("OCI credited with %q for service %q, but oci.New() constructs no services", native, name)
		}
	}
}

// TestFullyImplementedProvidersHaveServices makes sure the constructed-field
// gate did not over-filter AWS/Azure/GCP down to nothing.
func TestFullyImplementedProvidersHaveServices(t *testing.T) {
	services := loadServices(t)

	counts := map[string]int{}
	for _, svc := range services {
		for _, prov := range []string{"aws", "azure", "gcp"} {
			if svc.Providers[prov] != "" {
				counts[prov]++
			}
		}
	}

	for _, prov := range []string{"aws", "azure", "gcp"} {
		if counts[prov] == 0 {
			t.Errorf("provider %q resolved to zero services; construction gate too aggressive", prov)
		}
	}
}
