package eks

import (
	"strconv"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/yamlconv"
	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
)

// Version rules real EKS applies to clusters, nodegroups and add-ons.
const (
	// defaultKubernetesVersion is the newest version in standard support, which
	// EKS uses when CreateCluster omits the version. It matches catalogMaxMinor.
	defaultKubernetesVersion = "1.36"

	// minorStep is how far one UpdateClusterVersion call may move the minor.
	minorStep = 1

	// EKS supports a minor for 14 months of standard and 12 months of
	// extended support. That is six minors at a time, three in each.
	supportedMinorCount = 6
	supportedMinMinor   = catalogMaxMinor - supportedMinorCount + 1

	// rollbackConfig.timeoutMinutes range from the API reference.
	// Updates settle at once here, so the timeout never fires.
	minRollbackTimeoutMinutes = 120
	maxRollbackTimeoutMinutes = 10080

	// rollbackWindow is how long after an in-place upgrade EKS allows a
	// rollback to the previous minor version.
	rollbackWindow = 7 * 24 * time.Hour

	updateTypeVersion  = "VersionUpdate"
	updateTypeRollback = "VersionRollback"
)

// parseMinor reads the minor number of a "1.<minor>" version. Anything else,
// such as "0.1", "1.x" or "1.30.1", is rejected.
func parseMinor(v string) (int, bool) {
	rest, ok := strings.CutPrefix(v, "1.")
	if !ok || rest == "" {
		return 0, false
	}

	for _, r := range rest {
		if r < '0' || r > '9' {
			return 0, false
		}
	}

	n, err := strconv.Atoi(rest)
	if err != nil {
		return 0, false
	}

	return n, true
}

// validateKubernetesVersion checks that v is a "1.<minor>" version EKS
// currently offers in standard or extended support.
func validateKubernetesVersion(v string) error {
	n, ok := parseMinor(v)
	if !ok || n < supportedMinMinor || n > catalogMaxMinor {
		return cerrors.Newf(cerrors.InvalidArgument, "unsupported Kubernetes version %s", v)
	}

	return nil
}

// validateRollbackConfig checks rollbackConfig.timeoutMinutes when it is sent.
func validateRollbackConfig(timeout *int) error {
	if timeout == nil {
		return nil
	}

	if *timeout < minRollbackTimeoutMinutes || *timeout > maxRollbackTimeoutMinutes {
		return cerrors.Newf(cerrors.InvalidArgument,
			"rollbackConfig.timeoutMinutes must be between %d and %d", minRollbackTimeoutMinutes, maxRollbackTimeoutMinutes)
	}

	return nil
}

// unsupportedUpdate is the error EKS returns for a version change it won't do.
func unsupportedUpdate(from, to string) error {
	return cerrors.Newf(cerrors.InvalidArgument,
		"Unsupported Kubernetes minor version update from %s to %s", from, to)
}

// clusterVersionChange decides whether UpdateClusterVersion may move c to
// target. It returns the update type: an upgrade by one minor, or a rollback
// by one minor within rollbackWindow of the upgrade that reached the current
// version. An upgrade needs every nodegroup at the current version, and force
// does not change that. Without force, a rollback is blocked while a
// nodegroup runs a version newer than the target.
func (m *Mock) clusterVersionChange(c *eksdriver.Cluster, target string, force bool) (string, error) {
	cur, curOK := parseMinor(c.Version)
	want, _ := parseMinor(target)

	if !curOK {
		return "", unsupportedUpdate(c.Version, target)
	}

	switch {
	case want == cur+minorStep:
		if err := m.checkNodegroupsMatch(c); err != nil {
			return "", err
		}

		return updateTypeVersion, nil
	case want == cur-minorStep && m.canRollBack(c, target):
		if !force {
			if err := m.checkNodegroupsAtMost(c.Name, want, target); err != nil {
				return "", err
			}
		}

		return updateTypeRollback, nil
	default:
		return "", unsupportedUpdate(c.Version, target)
	}
}

// canRollBack reports whether c reached its version by an in-place upgrade
// from target within rollbackWindow.
func (m *Mock) canRollBack(c *eksdriver.Cluster, target string) bool {
	if c.PreviousVersion != target || c.VersionUpgradedAt.IsZero() {
		return false
	}

	return m.opts.Clock.Now().Sub(c.VersionUpgradedAt) <= rollbackWindow
}

// checkNodegroupsAtMost rejects a rollback while a managed nodegroup of the
// cluster runs a minor newer than the target. Nodes can't be newer than the
// control plane.
func (m *Mock) checkNodegroupsAtMost(clusterName string, minor int, target string) error {
	//nolint:gocritic // Store.All copies values out anyway; the per-iter copy here is no extra cost.
	for _, ng := range m.nodegroups.All() {
		if ng.ClusterName != clusterName {
			continue
		}

		if v, ok := parseMinor(ng.Version); ok && v > minor {
			return invalidStateErrf(
				"Nodegroup %s runs Kubernetes version %s, which is newer than the rollback target %s. "+
					"Roll back the nodegroup first or use force.", ng.NodegroupName, ng.Version, target)
		}
	}

	return nil
}

// checkNodegroupsMatch rejects an upgrade while a managed nodegroup runs a
// version other than the cluster's current one.
func (m *Mock) checkNodegroupsMatch(c *eksdriver.Cluster) error {
	//nolint:gocritic // Store.All copies values out anyway; the per-iter copy here is no extra cost.
	for _, ng := range m.nodegroups.All() {
		if ng.ClusterName == c.Name && ng.Version != c.Version {
			return cerrors.Newf(cerrors.FailedPrecondition,
				"Nodegroup %s runs Kubernetes version %s. All nodegroups must be at the cluster version %s "+
					"before the cluster can be updated.", ng.NodegroupName, ng.Version, c.Version)
		}
	}

	return nil
}

// resolveNodegroupVersion returns the version a nodegroup gets. An empty
// version means the cluster version. A set version must not be newer than it.
func resolveNodegroupVersion(cluster *eksdriver.Cluster, version string) (string, error) {
	if version == "" {
		return cluster.Version, nil
	}

	want, ok := parseMinor(version)
	if !ok {
		return "", cerrors.Newf(cerrors.InvalidArgument, "unsupported Kubernetes version %s", version)
	}

	if cur, curOK := parseMinor(cluster.Version); curOK && want > cur {
		return "", cerrors.Newf(cerrors.InvalidArgument,
			"Nodegroup Kubernetes version %s cannot be newer than cluster %s version %s",
			version, cluster.Name, cluster.Version)
	}

	return version, nil
}

// resolveAddonVersion picks the add-on version for a cluster version. An
// empty request gives the catalog default. A set version must be one the
// catalog lists for that cluster version.
func resolveAddonVersion(addonName, clusterVersion, requested string) (string, error) {
	d, ok := findAddon(addonName)
	if !ok {
		return "", addonNotSupported(addonName, clusterVersion)
	}

	var def string

	for _, v := range d.versions {
		if !containsString(v.clusters, clusterVersion) {
			continue
		}

		if requested != "" && v.version == requested {
			return requested, nil
		}

		if def == "" && containsString(v.defaults, clusterVersion) {
			def = v.version
		}
	}

	if def == "" {
		return "", addonNotSupported(addonName, clusterVersion)
	}

	if requested != "" {
		return "", cerrors.New(cerrors.InvalidArgument, "Addon version specified is not supported")
	}

	return def, nil
}

func addonNotSupported(addonName, clusterVersion string) error {
	return cerrors.Newf(cerrors.InvalidArgument,
		"Addon %s specified is not supported in %s kubernetes version", addonName, clusterVersion)
}

// findAddon looks up an add-on in the catalog by name.
func findAddon(name string) (*addonDef, bool) {
	catalog := addonCatalog()

	for i := range catalog {
		if catalog[i].name == name {
			return &catalog[i], true
		}
	}

	return nil, false
}

// validateConfigurationValues checks that an add-on configuration is a JSON
// or YAML object. JSON is valid YAML, so one parser covers both.
func validateConfigurationValues(s string) error {
	if strings.TrimSpace(s) == "" {
		return nil
	}

	tree, err := yamlconv.Decode([]byte(s), nil)
	if err != nil {
		return cerrors.Newf(cerrors.InvalidArgument,
			"ConfigurationValue provided in request is not supported: %v", err)
	}

	if _, ok := tree.(map[string]any); !ok {
		return cerrors.New(cerrors.InvalidArgument,
			"ConfigurationValue provided in request is not supported: the value must be a JSON or YAML object")
	}

	return nil
}
