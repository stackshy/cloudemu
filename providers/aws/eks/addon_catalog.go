package eks

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
)

// The add-on catalog is a static copy of what real EKS publishes through
// DescribeAddonVersions. Version strings for cluster versions 1.31 to 1.36
// come from the EKS user guide as of 2026-09-26. Rows for 1.28 to 1.30 keep
// the last builds EKS shipped for those versions.

// Kubernetes minor versions the catalog covers.
const (
	catalogMinMinor = 28
	catalogMaxMinor = 36
)

// Add-on owners, publishers and compute types used in the catalog.
const (
	ownerAWS       = "aws"
	ownerCommunity = "community"
	publisherEKS   = "eks"
	nsKubeSystem   = "kube-system"

	computeEC2     = "ec2"
	computeFargate = "fargate"
	computeAuto    = "auto"
	computeHybrid  = "hybrid"
)

// addonVersionDef is one version of an add-on. clusters lists the cluster
// versions it runs on. defaults lists those where it is the default.
type addonVersionDef struct {
	version  string
	clusters []string
	defaults []string
}

// addonDef is one add-on in the catalog. Versions are listed newest first.
type addonDef struct {
	name         string
	kind         string
	owner        string
	namespace    string
	computeTypes []string
	requiresIAM  bool
	podIdentity  []eksdriver.AddonPodIdentityConfiguration
	schemaProps  map[string]string
	versions     []addonVersionDef
}

// minors returns cluster versions "1.<from>" to "1.<to>".
func minors(from, to int) []string {
	out := make([]string, 0, to-from+1)
	for i := from; i <= to; i++ {
		out = append(out, "1."+strconv.Itoa(i))
	}

	return out
}

// allMinors returns every cluster version in the catalog.
func allMinors() []string { return minors(catalogMinMinor, catalogMaxMinor) }

// defaultOn builds a version that runs on, and is the default for, the given
// cluster versions.
func defaultOn(version string, clusterVersions ...string) addonVersionDef {
	return addonVersionDef{version: version, clusters: clusterVersions, defaults: clusterVersions}
}

// perCluster builds one default version per cluster version, for add-ons
// such as kube-proxy whose build tracks the Kubernetes version.
func perCluster(byMinor map[int]string) []addonVersionDef {
	out := make([]addonVersionDef, 0, len(byMinor))

	for i := catalogMaxMinor; i >= catalogMinMinor; i-- {
		v, ok := byMinor[i]
		if !ok {
			continue
		}

		cv := minors(i, i)
		out = append(out, addonVersionDef{version: v, clusters: cv, defaults: cv})
	}

	return out
}

// latestAndPrevious builds the common two-version shape. Both versions run on
// every cluster version and the latest is the default.
func latestAndPrevious(latest, previous string) []addonVersionDef {
	return []addonVersionDef{
		{version: latest, clusters: allMinors(), defaults: allMinors()},
		{version: previous, clusters: allMinors()},
	}
}

// commonSchemaProps are the settings most add-ons accept.
func commonSchemaProps(extra map[string]string) map[string]string {
	out := map[string]string{
		"affinity":       "object",
		"nodeSelector":   "object",
		"podAnnotations": "object",
		"podLabels":      "object",
		"resources":      "object",
		"tolerations":    "array",
	}

	for k, v := range extra {
		out[k] = v
	}

	return out
}

func podIdentity(serviceAccount, policyArn string) []eksdriver.AddonPodIdentityConfiguration {
	return []eksdriver.AddonPodIdentityConfiguration{
		{ServiceAccount: serviceAccount, RecommendedManagedPolicies: []string{policyArn}},
	}
}

func networkingAddons() []addonDef {
	return []addonDef{
		{
			name: "coredns", kind: "networking", owner: ownerAWS, namespace: nsKubeSystem,
			computeTypes: []string{computeEC2, computeFargate, computeAuto, computeHybrid},
			schemaProps: commonSchemaProps(map[string]string{
				"corefile": "string", "podDisruptionBudget": "object",
				"replicaCount": "integer", "topologySpreadConstraints": "array",
			}),
			versions: []addonVersionDef{
				defaultOn("v1.14.3-eksbuild.16", "1.35", "1.36"),
				defaultOn("v1.13.2-eksbuild.24", "1.34"),
				defaultOn("v1.12.4-eksbuild.31", "1.33"),
				defaultOn("v1.11.4-eksbuild.53", "1.29", "1.30", "1.31", "1.32"),
				defaultOn("v1.10.1-eksbuild.38", "1.28"),
			},
		},
		{
			name: "kube-proxy", kind: "networking", owner: ownerAWS, namespace: nsKubeSystem,
			computeTypes: []string{computeEC2, computeHybrid},
			schemaProps:  commonSchemaProps(map[string]string{"ipvs": "object", "mode": "string"}),
			versions: perCluster(map[int]string{
				36: "v1.36.0-eksbuild.21", 35: "v1.35.3-eksbuild.25", 34: "v1.34.6-eksbuild.25",
				33: "v1.33.10-eksbuild.25", 32: "v1.32.13-eksbuild.28", 31: "v1.31.14-eksbuild.32",
				30: "v1.30.14-eksbuild.20", 29: "v1.29.15-eksbuild.26", 28: "v1.28.15-eksbuild.37",
			}),
		},
		{
			name: "vpc-cni", kind: "networking", owner: ownerAWS, namespace: nsKubeSystem,
			computeTypes: []string{computeEC2}, requiresIAM: true,
			podIdentity: podIdentity("aws-node", "arn:aws:iam::aws:policy/AmazonEKS_CNI_Policy"),
			schemaProps: commonSchemaProps(map[string]string{
				"enableNetworkPolicy": "string", "env": "object", "init": "object", "nodeAgent": "object",
			}),
			versions: latestAndPrevious("v1.23.1-eksbuild.1", "v1.20.4-eksbuild.2"),
		},
	}
}

func storageAddons() []addonDef {
	return []addonDef{
		{
			name: "aws-ebs-csi-driver", kind: "storage", owner: ownerAWS, namespace: nsKubeSystem,
			computeTypes: []string{computeEC2}, requiresIAM: true,
			podIdentity: podIdentity("ebs-csi-controller-sa",
				"arn:aws:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicy"),
			schemaProps: commonSchemaProps(map[string]string{"controller": "object", "node": "object"}),
			versions:    latestAndPrevious("v1.45.0-eksbuild.2", "v1.37.0-eksbuild.1"),
		},
		{
			name: "aws-efs-csi-driver", kind: "storage", owner: ownerAWS, namespace: nsKubeSystem,
			computeTypes: []string{computeEC2, computeAuto}, requiresIAM: true,
			podIdentity: podIdentity("efs-csi-controller-sa",
				"arn:aws:iam::aws:policy/service-role/AmazonEFSCSIDriverPolicy"),
			schemaProps: commonSchemaProps(map[string]string{"controller": "object", "node": "object"}),
			versions:    latestAndPrevious("v2.1.9-eksbuild.1", "v2.1.4-eksbuild.1"),
		},
		{
			name: "snapshot-controller", kind: "storage", owner: ownerAWS, namespace: nsKubeSystem,
			computeTypes: []string{computeEC2, computeFargate, computeAuto, computeHybrid},
			schemaProps:  commonSchemaProps(map[string]string{"replicaCount": "integer"}),
			versions:     latestAndPrevious("v8.2.0-eksbuild.1", "v8.1.0-eksbuild.2"),
		},
	}
}

func observabilityAddons() []addonDef {
	return []addonDef{
		{
			name: "adot", kind: "observability", owner: ownerAWS, namespace: "opentelemetry-operator-system",
			computeTypes: []string{computeEC2, computeFargate, computeAuto, computeHybrid},
			schemaProps:  commonSchemaProps(map[string]string{"collector": "object", "manager": "object"}),
			versions:     latestAndPrevious("v0.117.0-eksbuild.1", "v0.109.0-eksbuild.2"),
		},
		{
			name: "amazon-cloudwatch-observability", kind: "observability", owner: ownerAWS,
			namespace: "amazon-cloudwatch", computeTypes: []string{computeEC2, computeAuto, computeHybrid},
			requiresIAM: true,
			podIdentity: podIdentity("cloudwatch-agent", "arn:aws:iam::aws:policy/CloudWatchAgentServerPolicy"),
			schemaProps: commonSchemaProps(map[string]string{"agent": "object", "containerLogs": "object"}),
			versions:    latestAndPrevious("v4.1.0-eksbuild.1", "v2.6.0-eksbuild.1"),
		},
		{
			name: "eks-node-monitoring-agent", kind: "observability", owner: ownerAWS, namespace: nsKubeSystem,
			computeTypes: []string{computeEC2, computeHybrid},
			schemaProps:  commonSchemaProps(nil),
			versions:     latestAndPrevious("v1.3.0-eksbuild.2", "v1.0.1-eksbuild.2"),
		},
		{
			name: "metrics-server", kind: "observability", owner: ownerCommunity, namespace: nsKubeSystem,
			computeTypes: []string{computeEC2, computeFargate, computeAuto, computeHybrid},
			schemaProps:  commonSchemaProps(map[string]string{"args": "array", "replicas": "integer"}),
			versions:     latestAndPrevious("v0.8.0-eksbuild.2", "v0.7.2-eksbuild.1"),
		},
	}
}

func securityAddons() []addonDef {
	return []addonDef{
		{
			name: "aws-guardduty-agent", kind: "security", owner: ownerAWS, namespace: "amazon-guardduty",
			computeTypes: []string{computeEC2, computeAuto},
			schemaProps:  commonSchemaProps(nil),
			versions:     latestAndPrevious("v1.10.0-eksbuild.2", "v1.8.1-eksbuild.2"),
		},
		{
			name: "eks-pod-identity-agent", kind: "security", owner: ownerAWS, namespace: nsKubeSystem,
			computeTypes: []string{computeEC2, computeHybrid},
			schemaProps:  commonSchemaProps(map[string]string{"agent": "object"}),
			versions:     latestAndPrevious("v1.3.8-eksbuild.2", "v1.3.4-eksbuild.1"),
		},
	}
}

// addonCatalog returns every add-on sorted by name.
func addonCatalog() []addonDef {
	groups := [][]addonDef{networkingAddons(), storageAddons(), observabilityAddons(), securityAddons()}

	size := 0
	for _, g := range groups {
		size += len(g)
	}

	out := make([]addonDef, 0, size)
	for _, g := range groups {
		out = append(out, g...)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })

	return out
}

// matchesAny reports whether v is in list. An empty list matches all.
func matchesAny(list []string, v string) bool {
	return len(list) == 0 || containsString(list, v)
}

// toAddonInfo renders an add-on for DescribeAddonVersions. A non-empty
// clusterVersion keeps only versions and compatibilities for it. It returns
// false when no version is left.
func (d *addonDef) toAddonInfo(clusterVersion string) (eksdriver.AddonInfo, bool) {
	info := eksdriver.AddonInfo{
		AddonName: d.name, Type: d.kind, Owner: d.owner, Publisher: publisherEKS, DefaultNamespace: d.namespace,
	}

	for _, v := range d.versions {
		var compat []eksdriver.AddonCompatibility

		for _, cv := range v.clusters {
			if clusterVersion != "" && cv != clusterVersion {
				continue
			}

			compat = append(compat, eksdriver.AddonCompatibility{
				ClusterVersion:   cv,
				PlatformVersions: []string{"*"},
				DefaultVersion:   containsString(v.defaults, cv),
			})
		}

		if len(compat) == 0 {
			continue
		}

		info.AddonVersions = append(info.AddonVersions, eksdriver.AddonVersionInfo{
			AddonVersion:           v.version,
			Architecture:           []string{"amd64", "arm64"},
			ComputeTypes:           copyStrings(d.computeTypes),
			Compatibilities:        compat,
			RequiresIamPermissions: d.requiresIAM,
		})
	}

	return info, len(info.AddonVersions) > 0
}

// DescribeAddonVersions lists catalog add-ons and their versions. An unknown
// add-on name or cluster version gives an empty list, as in real EKS.
//
//nolint:gocritic // filter matches the driver interface signature.
func (*Mock) DescribeAddonVersions(_ context.Context, filter eksdriver.AddonVersionFilter) ([]eksdriver.AddonInfo, error) {
	out := make([]eksdriver.AddonInfo, 0)

	catalog := addonCatalog()

	for i := range catalog {
		d := &catalog[i]
		if filter.AddonName != "" && d.name != filter.AddonName {
			continue
		}

		if !matchesAny(filter.Types, d.kind) || !matchesAny(filter.Owners, d.owner) ||
			!matchesAny(filter.Publishers, publisherEKS) {
			continue
		}

		if info, ok := d.toAddonInfo(filter.KubernetesVersion); ok {
			out = append(out, info)
		}
	}

	return out, nil
}

// configurationSchema renders the JSON schema DescribeAddonConfiguration
// returns for an add-on, in the draft-06 shape real EKS uses.
func (d *addonDef) configurationSchema() string {
	props := make(map[string]any, len(d.schemaProps))
	for name, typ := range d.schemaProps {
		props[name] = map[string]any{"type": typ}
	}

	title := d.name
	schema := map[string]any{
		"$ref":    "#/definitions/" + title,
		"$schema": "http://json-schema.org/draft-06/schema#",
		"definitions": map[string]any{
			title: map[string]any{
				"additionalProperties": false,
				"properties":           props,
				"title":                title,
				"type":                 "object",
			},
		},
	}

	b, err := json.Marshal(schema)
	if err != nil {
		return "{}"
	}

	return string(b)
}

// DescribeAddonConfiguration returns the configuration schema and pod
// identity settings of one add-on version.
func (*Mock) DescribeAddonConfiguration(
	_ context.Context, addonName, addonVersion string,
) (*eksdriver.AddonConfiguration, error) {
	if addonName == "" || addonVersion == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "addonName and addonVersion are required.")
	}

	d, v, ok := findAddonVersion(addonName, addonVersion)
	if ok {
		out := &eksdriver.AddonConfiguration{
			AddonName:                d.name,
			AddonVersion:             v.version,
			ConfigurationSchema:      d.configurationSchema(),
			PodIdentityConfiguration: make([]eksdriver.AddonPodIdentityConfiguration, 0, len(d.podIdentity)),
		}

		for _, p := range d.podIdentity {
			p.RecommendedManagedPolicies = copyStrings(p.RecommendedManagedPolicies)
			out.PodIdentityConfiguration = append(out.PodIdentityConfiguration, p)
		}

		return out, nil
	}

	return nil, cerrors.Newf(cerrors.InvalidArgument,
		"Addon %s with version %s is not supported.", addonName, addonVersion)
}

// findAddonVersion looks up one version of a catalog add-on.
func findAddonVersion(addonName, addonVersion string) (*addonDef, *addonVersionDef, bool) {
	catalog := addonCatalog()

	for i := range catalog {
		if catalog[i].name != addonName {
			continue
		}

		for j := range catalog[i].versions {
			if catalog[i].versions[j].version == addonVersion {
				return &catalog[i], &catalog[i].versions[j], true
			}
		}
	}

	return nil, nil, false
}
