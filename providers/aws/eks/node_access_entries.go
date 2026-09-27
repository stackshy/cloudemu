package eks

import (
	"strings"

	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
)

// windowsAMIPrefix starts every Windows managed nodegroup AMI type, such as
// WINDOWS_CORE_2022_x86_64.
const windowsAMIPrefix = "WINDOWS_"

// nodegroupEntryType is the access entry type EKS gives a managed nodegroup's
// node role: EC2_WINDOWS for Windows AMIs, EC2_LINUX for the rest
// (Amazon Linux and Bottlerocket).
func nodegroupEntryType(amiType string) string {
	if strings.HasPrefix(amiType, windowsAMIPrefix) {
		return eksdriver.AccessEntryTypeEC2Windows
	}

	return eksdriver.AccessEntryTypeEC2Linux
}

// addNodeEntryLocked creates the access entry EKS makes for the IAM role of
// a managed nodegroup or a Fargate profile. It does nothing when the cluster
// mode has no access entry API, when the role can't be an entry principal,
// or when the role already has an entry (a principal holds only one).
// Callers hold m.mu.
func (m *Mock) addNodeEntryLocked(c *eksdriver.Cluster, roleArn, entryType string) {
	if !apiAuthMode(c.AccessConfig.AuthenticationMode) {
		return
	}

	p, err := parsePrincipal(roleArn)
	if err != nil || p.kind != kindRole || p.account != m.opts.AccountID {
		return
	}

	key := accessEntryKey(c.Name, roleArn)
	if _, ok := m.accessEntries.Get(key); ok {
		return
	}

	now := m.opts.Clock.Now().UTC()
	m.accessEntries.Set(key, eksdriver.AccessEntry{
		ClusterName:  c.Name,
		PrincipalArn: roleArn,
		ARN:          m.accessEntryARN(c, p),
		Type:         entryType,
		Username:     defaultUsername(entryType, p),
		CreatedAt:    now,
		ModifiedAt:   now,
		AutoCreated:  true,
	})
}

// removeNodeEntryLocked drops the entry EKS made for roleArn once no managed
// nodegroup or Fargate profile of the cluster uses the role. Entries the
// caller created are left alone. Callers hold m.mu and have already removed
// the nodegroup or profile from its store.
func (m *Mock) removeNodeEntryLocked(clusterName, roleArn string) {
	key := accessEntryKey(clusterName, roleArn)

	e, ok := m.accessEntries.Get(key)
	if !ok || !e.AutoCreated || m.roleInUseLocked(clusterName, roleArn) {
		return
	}

	m.accessEntries.Delete(key)
}

func (m *Mock) roleInUseLocked(clusterName, roleArn string) bool {
	//nolint:gocritic // Store.All copies values out anyway; the per-iter copy here is no extra cost.
	for _, ng := range m.nodegroups.All() {
		if ng.ClusterName == clusterName && ng.NodeRole == roleArn {
			return true
		}
	}

	//nolint:gocritic // Store.All copies values out anyway; the per-iter copy here is no extra cost.
	for _, fp := range m.fargateProfiles.All() {
		if fp.ClusterName == clusterName && fp.PodExecutionRole == roleArn {
			return true
		}
	}

	return false
}

// backfillAccessEntriesLocked adds the entries EKS creates when a cluster
// first gets the access entry API: the creator admin entry, and the node
// entries of its managed nodegroups and Fargate profiles. Callers hold m.mu.
func (m *Mock) backfillAccessEntriesLocked(c *eksdriver.Cluster) {
	m.bootstrapCreatorEntryLocked(c)

	//nolint:gocritic // Store.All copies values out anyway; the per-iter copy here is no extra cost.
	for _, ng := range m.nodegroups.All() {
		if ng.ClusterName == c.Name {
			m.addNodeEntryLocked(c, ng.NodeRole, nodegroupEntryType(ng.AmiType))
		}
	}

	//nolint:gocritic // Store.All copies values out anyway; the per-iter copy here is no extra cost.
	for _, fp := range m.fargateProfiles.All() {
		if fp.ClusterName == c.Name {
			m.addNodeEntryLocked(c, fp.PodExecutionRole, eksdriver.AccessEntryTypeFargateLinux)
		}
	}
}
