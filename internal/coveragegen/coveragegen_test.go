package main

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stackshy/cloudemu/v2/providers/aws"
	"github.com/stackshy/cloudemu/v2/providers/azure"
	"github.com/stackshy/cloudemu/v2/providers/gcp"
	"github.com/stackshy/cloudemu/v2/providers/oci"
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

// TestCreditedServicesAreConstructed is the honesty guarantee: a provider is
// credited with a service only when its factory actually builds one.
//
// The generator decides that by reading the factory's AST. This checks the same
// claim against a real provider value, so a parsing bug cannot credit a slot
// that is merely declared — which is what OCI's nil service fields would
// otherwise do.
func TestCreditedServicesAreConstructed(t *testing.T) {
	services := loadServices(t)

	built := map[string]map[string]bool{
		"aws":   constructed(aws.New()),
		"azure": constructed(azure.New()),
		"gcp":   constructed(gcp.New()),
		"oci":   constructed(oci.New()),
	}

	for name, svc := range services {
		for prov, native := range svc.Providers {
			if native == "" {
				continue
			}

			if !built[prov][native] {
				t.Errorf("%s credited with %q for service %q, but %s.New() leaves that field nil",
					prov, native, name, prov)
			}
		}
	}
}

// constructed returns the provider fields the factory actually populated. A
// typed nil counts as unpopulated: it survives a != nil check and then panics.
func constructed(provider any) map[string]bool {
	v := reflect.ValueOf(provider).Elem()
	out := map[string]bool{}

	for i := range v.NumField() {
		field := v.Field(i)

		switch field.Kind() {
		case reflect.Interface:
			if field.IsNil() || (field.Elem().Kind() == reflect.Ptr && field.Elem().IsNil()) {
				continue
			}
		case reflect.Ptr:
			if field.IsNil() {
				continue
			}
		default:
			continue
		}

		out[v.Type().Field(i).Name] = true
	}

	return out
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
