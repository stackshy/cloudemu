package eks

import (
	"context"
	"encoding/json"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
)

// TestAddonCatalogOneDefaultPerClusterVersion checks the catalog rule that
// every add-on has exactly one default version on each cluster version.
func TestAddonCatalogOneDefaultPerClusterVersion(t *testing.T) {
	for _, d := range addonCatalog() {
		for _, cv := range allMinors() {
			defaults := 0

			for _, v := range d.versions {
				if containsString(v.defaults, cv) {
					if !containsString(v.clusters, cv) {
						t.Errorf("%s %s: default on %s but not compatible", d.name, v.version, cv)
					}

					defaults++
				}
			}

			if defaults != 1 {
				t.Errorf("%s on %s: %d default versions, want 1", d.name, cv, defaults)
			}
		}
	}
}

func TestDescribeAddonVersionsFilters(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	all, err := m.DescribeAddonVersions(ctx, eksdriver.AddonVersionFilter{})
	if err != nil || len(all) != len(addonCatalog()) {
		t.Fatalf("all add-ons: got %d err %v", len(all), err)
	}

	for i := 1; i < len(all); i++ {
		if all[i-1].AddonName >= all[i].AddonName {
			t.Fatalf("add-ons not sorted: %s >= %s", all[i-1].AddonName, all[i].AddonName)
		}
	}

	got, err := m.DescribeAddonVersions(ctx, eksdriver.AddonVersionFilter{AddonName: "coredns", KubernetesVersion: "1.30"})
	if err != nil || len(got) != 1 {
		t.Fatalf("coredns on 1.30: %+v err %v", got, err)
	}

	for _, v := range got[0].AddonVersions {
		for _, c := range v.Compatibilities {
			if c.ClusterVersion != "1.30" {
				t.Fatalf("kubernetesVersion filter leaked %s", c.ClusterVersion)
			}
		}
	}

	if got[0].AddonVersions[0].AddonVersion != "v1.11.4-eksbuild.53" ||
		!got[0].AddonVersions[0].Compatibilities[0].DefaultVersion {
		t.Fatalf("coredns 1.30 default = %+v", got[0].AddonVersions[0])
	}

	empty := []eksdriver.AddonVersionFilter{
		{AddonName: "no-such-addon"},
		{KubernetesVersion: "1.99"},
		{Types: []string{"databases"}},
		{Publishers: []string{"someone"}},
	}

	for _, f := range empty {
		if out, err := m.DescribeAddonVersions(ctx, f); err != nil || len(out) != 0 {
			t.Errorf("filter %+v: got %d add-ons err %v, want none", f, len(out), err)
		}
	}

	community, _ := m.DescribeAddonVersions(ctx, eksdriver.AddonVersionFilter{Owners: []string{"community"}})
	if len(community) != 1 || community[0].AddonName != "metrics-server" {
		t.Fatalf("owners=community: %+v", community)
	}

	storage, _ := m.DescribeAddonVersions(ctx, eksdriver.AddonVersionFilter{Types: []string{"storage"}})
	if len(storage) != 3 {
		t.Fatalf("types=storage: got %d add-ons, want 3", len(storage))
	}
}

func TestDescribeAddonConfiguration(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	cfg, err := m.DescribeAddonConfiguration(ctx, "vpc-cni", "v1.23.1-eksbuild.1")
	if err != nil {
		t.Fatalf("describe: %v", err)
	}

	var schema map[string]any
	if err := json.Unmarshal([]byte(cfg.ConfigurationSchema), &schema); err != nil {
		t.Fatalf("schema is not JSON: %v", err)
	}

	if schema["$schema"] != "http://json-schema.org/draft-06/schema#" {
		t.Fatalf("schema header = %v", schema["$schema"])
	}

	if len(cfg.PodIdentityConfiguration) != 1 || cfg.PodIdentityConfiguration[0].ServiceAccount != "aws-node" {
		t.Fatalf("pod identity = %+v", cfg.PodIdentityConfiguration)
	}

	plain, err := m.DescribeAddonConfiguration(ctx, "coredns", "v1.11.4-eksbuild.53")
	if err != nil || len(plain.PodIdentityConfiguration) != 0 {
		t.Fatalf("coredns: %+v err %v", plain, err)
	}

	for _, tc := range [][2]string{{"vpc-cni", "v0.0.1"}, {"nope", "v1.0.0"}, {"", ""}} {
		if _, err := m.DescribeAddonConfiguration(ctx, tc[0], tc[1]); !cerrors.IsInvalidArgument(err) {
			t.Errorf("%v: want InvalidArgument, got %v", tc, err)
		}
	}
}
