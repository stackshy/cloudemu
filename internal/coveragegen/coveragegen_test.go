package main

import (
	"path/filepath"
	"reflect"
	"strings"
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

// loadAllServices runs the read-only generator including provider-native
// synthesis, matching what render() serializes.
func loadAllServices(t *testing.T) map[string]*Service {
	t.Helper()

	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}

	services := loadServices(t)

	if err := synthesizeNativeServices(root, services); err != nil {
		t.Fatalf("synthesizeNativeServices: %v", err)
	}

	return services
}

// TestProviderNativeServicesSynthesized is the guarantee behind this whole
// generator step: services implemented only as a wire handler plus a provider
// mock (no services/<name>/driver interface) still surface in coverage, so
// llms.txt readers don't wrongly conclude they are unsupported. The Kubernetes
// trio (EKS/AKS/GKE) is the canonical case.
func TestProviderNativeServicesSynthesized(t *testing.T) {
	services := loadAllServices(t)

	cases := map[string]struct {
		prov, native string
		wantOps      bool
	}{
		"eks":            {"aws", "EKS", true},
		"aks":            {"azure", "AKS", true},
		"gke":            {"gcp", "GKE", true},
		"sts":            {"aws", "STS", true}, // wire-only, ops from nativeWireOperations
		"rds":            {"aws", "RDS", true},
		"redshift":       {"", "", false}, // Redshift is the relationaldb native, not a synth service
		"resourcegroups": {"azure", "Resourcegroups", true},
		"workrequest":    {"oci", "Workrequest", true},
	}

	for name, want := range cases {
		svc, ok := services[name]

		if want.prov == "" {
			if ok {
				t.Errorf("service %q unexpectedly present as its own entry", name)
			}

			continue
		}

		if !ok {
			t.Fatalf("provider-native service %q not synthesized", name)
		}

		if svc.Interface != providerNativeInterface {
			t.Errorf("%s interface = %q, want %q", name, svc.Interface, providerNativeInterface)
		}

		if got := svc.Providers[want.prov]; got != want.native {
			t.Errorf("%s providers[%s] = %q, want %q", name, want.prov, got, want.native)
		}

		if want.wantOps && len(svc.Operations) == 0 {
			t.Errorf("%s has 0 operations; expected the backing mock's surface", name)
		}
	}
}

// TestProviderNativeServicesHaveOperations is the anti-undersell guard: every
// provider-native service must report a non-empty operation surface. A wire-only
// handler (no providers/<prov>/<pkg> mock) gets its operations from
// nativeWireOperations; without an entry it surfaces as "0 operations" and reads
// as a stub even though it works. This turns that regression into a failing test
// so a newly added wire-only service must declare its operations.
func TestProviderNativeServicesHaveOperations(t *testing.T) {
	for name, svc := range loadAllServices(t) {
		if svc.Interface != providerNativeInterface {
			continue
		}

		if len(svc.Operations) == 0 {
			t.Errorf("provider-native service %q has 0 operations; add its wire-served "+
				"operations to nativeWireOperations in wireops.go", name)
		}
	}
}

// TestNativeWireOperationsAreRegistered guards against a stale nativeWireOperations
// entry: every key must name a handler package the corresponding provider's
// server actually registers, so a renamed or removed handler cannot leave dead
// declared operations behind.
func TestNativeWireOperationsAreRegistered(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}

	registered := map[string]map[string]bool{}
	for _, prov := range providerOrder {
		pkgs, regErr := registeredHandlerPkgs(root, prov)
		if regErr != nil {
			t.Fatalf("registeredHandlerPkgs(%s): %v", prov, regErr)
		}

		registered[prov] = pkgs
	}

	for key := range nativeWireOperations {
		prov, pkg, ok := strings.Cut(key, "/")
		if !ok {
			t.Errorf("nativeWireOperations key %q is not \"provider/package\"", key)
			continue
		}

		if !registered[prov][pkg] {
			t.Errorf("nativeWireOperations key %q names no handler registered by server/%s/%s.go", key, prov, prov)
		}
	}
}

// TestProviderNativeDoesNotClobberPortable makes sure synthesis adds services
// without disturbing the portable ones the earlier stages resolved.
func TestProviderNativeDoesNotClobberPortable(t *testing.T) {
	portable := loadServices(t)
	all := loadAllServices(t)

	if len(all) <= len(portable) {
		t.Fatalf("synthesis added no services: %d portable, %d total", len(portable), len(all))
	}

	for name, svc := range portable {
		got, ok := all[name]
		if !ok {
			t.Errorf("portable service %q lost after synthesis", name)
			continue
		}

		if got.Interface == providerNativeInterface {
			t.Errorf("portable service %q was overwritten by a provider-native entry", name)
		}

		if len(got.Operations) != len(svc.Operations) {
			t.Errorf("portable service %q operation count changed: %d -> %d",
				name, len(svc.Operations), len(got.Operations))
		}
	}
}

func TestProviderNativeNamesResolve(t *testing.T) {
	services := loadServices(t)

	cases := map[string]map[string]string{
		"storage": {"aws": "S3", "azure": "BlobStorage", "gcp": "GCS"},
		"compute": {"aws": "EC2", "azure": "VirtualMachines", "gcp": "GCE"},
		// monitoring must resolve to the real metrics service, not a consumer
		// like EKS that merely imports monitoring/driver.
		"monitoring": {"aws": "CloudWatch"},
		// networking resolves to driver.Networking (implemented by all three),
		// not the AWS-only NetworkInterfaces optional capability (#383).
		"networking": {"aws": "VPC", "azure": "VNet", "gcp": "VPC"},
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

// TestPrimaryInterfaceNotOptionalCapability guards against #383: a service whose
// primary driver interface coexists with a smaller optional capability must
// report the primary. networking is the canonical case — driver.Networking
// (~50+ ops) alongside the 3-method driver.NetworkInterfaces capability.
func TestPrimaryInterfaceNotOptionalCapability(t *testing.T) {
	services := loadServices(t)

	svc, ok := services["networking"]
	if !ok {
		t.Fatal("service \"networking\" not found")
	}

	if svc.Interface != "Networking" {
		t.Errorf("networking primary interface = %q, want %q", svc.Interface, "Networking")
	}

	// The optional NetworkInterfaces capability has 3 methods; the primary
	// carries the full surface, so a correct resolution has many more.
	const minPrimaryOps = 10
	if len(svc.Operations) < minPrimaryOps {
		t.Errorf("networking has %d operations, want >= %d — likely resolved to an optional capability",
			len(svc.Operations), minPrimaryOps)
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

// TestNoDeadWireHandlers guards against a service the provider factory fully
// implements (driver + a populated Provider field, i.e. Service.Providers is
// set) but that server/<cloud>/<cloud>.go never wires up: a Drivers field
// backing it is either absent or declared-but-unread in New(). Such a service
// is reachable through the Go library and the in-process SDK-compat server,
// but never through the standalone wire-protocol server for that cloud — a
// silent capability gap.
func TestNoDeadWireHandlers(t *testing.T) {
	services := loadServices(t)

	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}

	warnings, err := checkRegistrations(root, sortedServices(services))
	if err != nil {
		t.Fatalf("checkRegistrations: %v", err)
	}

	for _, w := range warnings {
		t.Error(w)
	}
}

// renderedCapabilities returns the optional-capability names that
// renderProviderPage actually emits for a service on a provider page, parsed
// from the rendered markdown (the "### <Name>" headers under the "## Optional
// capabilities" section). This exercises the real render path, so a regression
// that un-gates the loop (listing every capability on every page) is caught.
func renderedCapabilities(t *testing.T, outDir, prov string, svc *Service) map[string]bool {
	t.Helper()

	page := renderProviderPage(outDir, prov, svc.Providers[prov], svc)

	out := map[string]bool{}
	inCaps := false

	for _, line := range strings.Split(page, "\n") {
		switch {
		case strings.HasPrefix(line, "## Optional capabilities"):
			inCaps = true
		case strings.HasPrefix(line, "## "):
			inCaps = false
		case inCaps && strings.HasPrefix(line, "### "):
			out[strings.TrimSpace(strings.TrimPrefix(line, "### "))] = true
		}
	}

	return out
}

// TestOptionalCapabilitiesGatedToImplementers is the honesty guarantee for
// optional capabilities (#394, #498): a provider page lists a capability only
// when that provider's mock implements the capability's FULL method set. Before
// this gate, every provider page listed every capability of the service — so
// oci/vcn.md falsely claimed AWS-only TransitGateways/IPAM, gcp/gce.md claimed
// ImageRegistrar/VolumeModifier, etc.
func TestOptionalCapabilitiesGatedToImplementers(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}

	outDir := filepath.Join(root, "docs", "coverage")
	services := loadServices(t)

	for name, svc := range services {
		if len(svc.Capabilities) == 0 {
			continue
		}

		for prov := range svc.Providers {
			rendered := renderedCapabilities(t, outDir, prov, svc)

			// Every rendered capability must be one the provider's mock covers,
			// and every covered capability must render — the page reflects the
			// mock's method set exactly, never more, never less.
			for _, capability := range svc.Capabilities {
				covered := covers(svc.providerMethods[prov], capability.Operations)
				if covered != rendered[capability.Name] {
					t.Errorf("%s/%s capability %q: covered=%v but rendered=%v",
						prov, name, capability.Name, covered, rendered[capability.Name])
				}
			}
		}
	}
}

// TestOptionalCapabilityVanishRemainAnchors pins the exact capabilities that
// must disappear from (and remain on) specific provider pages after the gate,
// so a future change to the resolution or gating logic cannot silently
// reintroduce a false claim or drop a real one.
func TestOptionalCapabilityVanishRemainAnchors(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}

	outDir := filepath.Join(root, "docs", "coverage")
	services := loadServices(t)

	type anchor struct {
		svc, prov, capability string
		want                  bool // true: must render, false: must not
	}

	anchors := []anchor{
		// Vanish: AWS-only network capabilities off the OCI VCN page.
		{"networking", "oci", "TransitGateways", false},
		{"networking", "oci", "IPAM", false},
		{"networking", "oci", "AzureNetworkInterfaces", false},
		// Vanish: AWS-only / Azure-only compute capabilities off the GCP GCE page.
		{"compute", "gcp", "ImageRegistrar", false},
		{"compute", "gcp", "VolumeModifier", false},
		{"compute", "gcp", "SnapshotCopier", false},
		{"compute", "gcp", "AzureVMController", false},
		// Vanish: AWS-only compute capabilities off the Azure VM page.
		{"compute", "azure", "ImageRegistrar", false},
		{"compute", "azure", "VolumeModifier", false},
		{"compute", "azure", "SnapshotCopier", false},
		// Vanish: Azure-only network capability off the AWS VPC page.
		{"networking", "aws", "AzureNetworkInterfaces", false},

		// Remain: AWS network capabilities on the AWS VPC page.
		{"networking", "aws", "IPAM", true},
		{"networking", "aws", "TransitGateways", true},
		// Remain: Azure compute controller on the Azure VM page.
		{"compute", "azure", "AzureVMController", true},
		// Remain: Azure network interfaces on the Azure VNet page.
		{"networking", "azure", "AzureNetworkInterfaces", true},
		// Remain: AWS compute capabilities on the AWS EC2 page.
		{"compute", "aws", "ImageRegistrar", true},
		{"compute", "aws", "VolumeModifier", true},
		// Remain: ConsoleReader is implemented by all three, so it stays on every
		// compute page — the exact case #498's crude "AWS-page-only" filter got
		// wrong, and why the fix must be a per-provider method-set gate.
		{"compute", "aws", "ConsoleReader", true},
		{"compute", "azure", "ConsoleReader", true},
		{"compute", "gcp", "ConsoleReader", true},
	}

	for _, a := range anchors {
		svc, ok := services[a.svc]
		if !ok {
			t.Fatalf("service %q not found", a.svc)
		}

		rendered := renderedCapabilities(t, outDir, a.prov, svc)
		if rendered[a.capability] != a.want {
			t.Errorf("%s/%s capability %q rendered=%v, want %v",
				a.prov, a.svc, a.capability, rendered[a.capability], a.want)
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
