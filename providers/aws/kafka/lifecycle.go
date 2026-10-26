package kafka

import (
	"time"

	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/kafka/driver"
)

// Settle windows for the MSK lifecycle under config.Options.AsyncSettle. Real
// MSK takes many minutes for each; the emulator keeps the transitions short so
// a polling client observes them without slowing tests down. With AsyncSettle
// off, opts.SettleDuration returns 0 and every transition is immediate.
const (
	clusterCreateSettle = settle.DefaultClusterSettle // CREATING -> ACTIVE
	clusterDeleteSettle = settle.DefaultClusterSettle // DELETING -> removed
	vpcConnCreateSettle = settle.DefaultClusterSettle // CREATING -> AVAILABLE
)

// vpcConnStateCreating is the transient state a new VPC connection reports
// under AsyncSettle (VpcConnectionState: CREATING | AVAILABLE | INACTIVE |
// UPDATING | DEACTIVATING | DELETING | FAILED | REJECTED | REJECTING).
const vpcConnStateCreating = "CREATING"

// settleUntil returns the instant a window of the given default length opened
// now closes, or the zero time when AsyncSettle is off (no window).
func (m *Mock) settleUntil(d time.Duration) time.Time {
	dur := m.opts.SettleDuration(d)
	if dur <= 0 {
		return time.Time{}
	}

	return m.now().Add(dur)
}

// clusterStateLocked returns the cluster's externally visible state now. The
// caller holds cd.mu (read or write).
func (m *Mock) clusterStateLocked(cd *clusterData) string {
	return clusterStateAt(cd, m.now())
}

// clusterStateAt returns the cluster's externally visible state at t: CREATING
// while its create window is open, otherwise the stored state. The caller holds
// cd.mu (read or write).
func clusterStateAt(cd *clusterData, t time.Time) string {
	if cd.cluster.State == driver.ClusterStateActive && t.Before(cd.readyAt) {
		return driver.ClusterStateCreating
	}

	return cd.cluster.State
}

// observedClusterLocked returns a deep copy of the stored cluster carrying its
// visible state. The caller holds cd.mu (read or write).
func (m *Mock) observedClusterLocked(cd *clusterData) driver.Cluster {
	out := snapshotCluster(cd.cluster)
	out.State = m.clusterStateLocked(cd)

	return out
}

// requireActive rejects a write against a cluster that is not ACTIVE: cluster
// updates, and topic, SCRAM secret and cluster policy writes. Real MSK accepts
// no API operations on a cluster that is CREATING or DELETING
// (https://docs.aws.amazon.com/msk/latest/developerguide/msk-cluster-states.html).
// Without AsyncSettle a cluster is always ACTIVE, so this never fires. The
// caller holds cd.mu.
func (m *Mock) requireActive(cd *clusterData) error {
	if st := m.clusterStateLocked(cd); st != driver.ClusterStateActive {
		return badRequest("cluster %s is in %s state; this operation requires ACTIVE",
			cd.cluster.ClusterARN, st)
	}

	return nil
}

// goneAt reports whether a stored cluster is DELETING and its delete window has
// elapsed at t, i.e. real MSK would no longer return it. A DELETING cluster with
// no window (restored from a snapshot that predates windows) counts as elapsed.
func goneAt(cd *clusterData, t time.Time) bool {
	cd.mu.RLock()
	defer cd.mu.RUnlock()

	return cd.cluster.State == driver.ClusterStateDeleting && !t.Before(cd.deleteAt)
}

// reapIfGone removes a cluster whose DELETING window has elapsed now and frees
// its name, reporting whether it was removed. Request paths call it lazily on
// read; Tick calls reapIfGoneAt so a finished delete is removed (and persisted)
// even when nothing reads the cluster.
func (m *Mock) reapIfGone(arn string, cd *clusterData) bool {
	return m.reapIfGoneAt(arn, cd, m.now())
}

// reapIfGoneAt is reapIfGone judged at t.
func (m *Mock) reapIfGoneAt(arn string, cd *clusterData, t time.Time) bool {
	if !goneAt(cd, t) {
		return false
	}

	m.reapMu.Lock()
	defer m.reapMu.Unlock()

	// Re-check under reapMu: a concurrent reader may have removed it already.
	cur, ok := m.clusters.Get(arn)
	if !ok || cur != cd {
		return true
	}

	cd.mu.RLock()
	name := cd.cluster.ClusterName
	cd.mu.RUnlock()

	m.removeCluster(arn, name)

	return true
}

// removeCluster drops a cluster from the store and frees its name, but only
// when the name still points at this ARN (a later cluster may hold it).
func (m *Mock) removeCluster(arn, name string) {
	m.clusters.Delete(arn)

	if owner, ok := m.clusterNames.Get(name); ok && owner == arn {
		m.clusterNames.Delete(name)
	}
}

// reapName frees name when it is claimed by a cluster that has finished
// deleting, so a create can reuse it. The caller holds createMu.
func (m *Mock) reapName(name string) {
	arn, ok := m.clusterNames.Get(name)
	if !ok {
		return
	}

	cd, ok := m.clusters.Get(arn)
	if !ok {
		m.clusterNames.Delete(name)

		return
	}

	m.reapIfGone(arn, cd)
}

// observedVpcConnectionLocked returns a copy of the stored VPC connection
// carrying its visible state: CREATING while its create window is open. The
// caller holds vd.mu (read or write).
func (m *Mock) observedVpcConnectionLocked(vd *vpcConnData) driver.VpcConnection {
	out := snapshotVpcConnection(vd.vpc)
	if out.State == vpcConnStateAvailable && m.now().Before(vd.readyAt) {
		out.State = vpcConnStateCreating
	}

	return out
}
