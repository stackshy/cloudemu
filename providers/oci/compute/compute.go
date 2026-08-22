// Package compute provides an in-memory mock implementation of OCI Compute
// and Block Volume. It implements the portable compute driver: an instance
// pool is the auto-scaling group, an instance configuration is the launch
// template, a block volume backup is the snapshot, and a preemptible instance
// is the spot instance.
//
// OCI has no key pair resource — an SSH public key is instance metadata under
// the ssh_authorized_keys key — so the driver's key pair operations report
// Unimplemented rather than inventing one.
package compute

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

const timeFormat = time.RFC3339

// OCI lifecycle states for the resources whose portable projection carries
// none. Every CloudEmu mutation is synchronous, so the transient
// PROVISIONING/TERMINATING states are never observable.
const (
	StateAvailable  = "AVAILABLE"
	StateAttached   = "ATTACHED"
	StateDetached   = "DETACHED"
	StateRunning    = "RUNNING"
	StateStopped    = "STOPPED"
	StateTerminated = "TERMINATED"
)

// OCID resource type segments.
const (
	typeInstance             = "instance"
	typeImage                = "image"
	typeVolume               = "volume"
	typeVolumeAttachment     = "volumeattachment"
	typeBootVolume           = "bootvolume"
	typeBootVolumeAttachment = "bootvolumeattachment"
	typeVolumeBackup         = "volumebackup"
	typeVolumeGroup          = "volumegroup"
	typeVNICAttachment       = "vnicattachment"
	typeInstanceConfig       = "instanceconfiguration"
	typeInstancePool         = "instancepool"
)

// Internal tag keys carrying OCI attributes the portable projections have no
// field for. They are stripped from freeformTags on the wire.
const (
	internalTagPrefix = "cloudemu:"
	// TagDisplayName is the OCI display name of a resource whose portable
	// projection has no name field.
	TagDisplayName = internalTagPrefix + "ociDisplayName"
)

// ipSegmentSize splits the fallback private address counter into octets.
const ipSegmentSize = 256

// Compile-time check that Mock implements driver.Compute.
var _ driver.Compute = (*Mock)(nil)

// Mock is an in-memory mock implementation of OCI Compute and Block Volume.
type Mock struct {
	// mu guards the fields of stored values and spans the reads and writes a
	// single operation makes across stores: launching an instance writes the
	// instance, its VNIC attachment and its boot volume, and a detach reads
	// one store before writing another.
	mu sync.RWMutex

	instances   *memstore.Store[*instanceData]
	details     *memstore.Store[InstanceDetails]
	images      *memstore.Store[*imageData]
	volumes     *memstore.Store[*volumeData]
	volAttach   *memstore.Store[*VolumeAttachment]
	bootVolumes *memstore.Store[*BootVolume]
	bootAttach  *memstore.Store[*BootVolumeAttachment]
	backups     *memstore.Store[*backupData]
	volGroups   *memstore.Store[*VolumeGroup]
	vnicAttach  *memstore.Store[*VNICAttachment]
	pools       *memstore.Store[*poolData]
	configs     *memstore.Store[*configData]
	spot        *memstore.Store[*driver.SpotInstanceRequest]
	shapes      *memstore.Store[Shape]
	// scopes and created hold what the portable projections have no room for:
	// the compartment a resource was created in and when, keyed by OCID.
	scopes  *memstore.Store[scope.Scope]
	created *memstore.Store[string]

	net       Networking
	mon       mondriver.Monitoring
	opts      *config.Options
	ipCounter atomic.Int64
}

// New creates a new OCI Compute mock, seeded with OCI's shape catalog and
// platform images.
func New(opts *config.Options) *Mock {
	m := &Mock{
		instances:   memstore.New[*instanceData](),
		details:     memstore.New[InstanceDetails](),
		images:      memstore.New[*imageData](),
		volumes:     memstore.New[*volumeData](),
		volAttach:   memstore.New[*VolumeAttachment](),
		bootVolumes: memstore.New[*BootVolume](),
		bootAttach:  memstore.New[*BootVolumeAttachment](),
		backups:     memstore.New[*backupData](),
		volGroups:   memstore.New[*VolumeGroup](),
		vnicAttach:  memstore.New[*VNICAttachment](),
		pools:       memstore.New[*poolData](),
		configs:     memstore.New[*configData](),
		spot:        memstore.New[*driver.SpotInstanceRequest](),
		shapes:      memstore.New[Shape](),
		scopes:      memstore.New[scope.Scope](),
		created:     memstore.New[string](),
		opts:        opts,
	}

	m.seedShapes()
	m.seedImages()

	return m
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.mon = mon
}

// Scope returns the compartment a resource was created in. It is an OPTIONAL
// capability, discovered by type assertion: the portable Compute driver has no
// compartment parameter, so OCI scoping is exposed alongside it.
func (m *Mock) Scope(id string) scope.Scope {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, _ := m.scopes.Get(id)

	return s
}

// SetScope records the compartment a resource belongs to, replacing the
// default recorded at create time. Deleting the resource forgets it.
func (m *Mock) SetScope(id string, s scope.Scope) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if s.IsZero() {
		m.scopes.Delete(id)
		return
	}

	m.scopes.Set(id, s)
}

// Created returns the OCI timestamp a resource was created at, or the empty
// string for an unknown OCID. Part of the same optional capability as Scope.
func (m *Mock) Created(id string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, _ := m.created.Get(id)

	return t
}

// SetTags replaces the freeform tags of any resource this mock owns, which is
// how OCI's update calls carry them.
func (m *Mock) SetTags(id string, tags map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch {
	case m.instances.Update(id, func(v *instanceData) *instanceData { v.Tags = copyTags(tags); return v }):
	case m.volumes.Update(id, func(v *volumeData) *volumeData { v.Tags = copyTags(tags); return v }):
	case m.images.Update(id, func(v *imageData) *imageData { v.Tags = copyTags(tags); return v }):
	case m.backups.Update(id, func(v *backupData) *backupData { v.Tags = copyTags(tags); return v }):
	case m.bootVolumes.Update(id, func(v *BootVolume) *BootVolume { v.Tags = copyTags(tags); return v }):
	case m.volGroups.Update(id, func(v *VolumeGroup) *VolumeGroup { v.Tags = copyTags(tags); return v }):
	case m.pools.Update(id, func(v *poolData) *poolData { v.Tags = copyTags(tags); return v }):
	case m.configs.Update(id, func(v *configData) *configData { v.Tags = copyTags(tags); return v }):
	default:
		return cerrors.Newf(cerrors.NotFound, "resource %q not found", id)
	}

	return nil
}

// newOCID mints an OCID for the given resource type in the configured realm
// and region.
func (m *Mock) newOCID(resourceType string) string {
	return idgen.OCID(resourceType, m.opts.Realm, m.opts.OCIRegion())
}

// now returns the current time in OCI's timestamp format.
func (m *Mock) now() string {
	return m.opts.Clock.Now().UTC().Format(timeFormat)
}

// record stamps a newly created resource with its creation time and the
// default compartment. A wire caller naming another compartment overwrites the
// latter with SetScope.
func (m *Mock) record(id string) {
	m.scopes.Set(id, scope.Scope{Compartment: m.opts.CompartmentID})
	m.created.Set(id, m.now())
}

// forget drops the scope and creation time of a deleted resource.
func (m *Mock) forget(id string) {
	m.scopes.Delete(id)
	m.created.Delete(id)
}

// nextIP is the fallback private address for an instance launched with no
// networking wired in, or with no subnet at all.
func (m *Mock) nextIP() string {
	n := m.ipCounter.Add(1)

	return "10.0." + itoa(n/ipSegmentSize) + "." + itoa(n%ipSegmentSize)
}

// defaultAD is the availability domain a caller who names none lands in.
func (m *Mock) defaultAD() string {
	return "cloudemu:" + strings.ToUpper(m.opts.OCIRegion()) + "-AD-1"
}

// describeResources lists a store, filtered to ids when any are given.
// Unfiltered lists come back ordered by OCID so paging is deterministic.
func describeResources[T any, R any](store *memstore.Store[T], ids []string, toInfo func(T) R) []R {
	if len(ids) == 0 {
		all := store.SortedValues()
		out := make([]R, 0, len(all))

		for _, item := range all {
			out = append(out, toInfo(item))
		}

		return out
	}

	out := make([]R, 0, len(ids))

	for _, id := range ids {
		item, ok := store.Get(id)
		if !ok {
			continue
		}

		out = append(out, toInfo(item))
	}

	return out
}

// listScoped returns the values of a store that live in a compartment and
// satisfy match, ordered by OCID. A nil match keeps everything.
func listScoped[T any](m *Mock, store *memstore.Store[*T], compartmentID string,
	id func(*T) string, match func(*T) bool,
) []T {
	out := make([]T, 0)

	for _, v := range store.SortedValues() {
		if s, _ := m.scopes.Get(id(v)); s.Compartment != compartmentID {
			continue
		}

		if match != nil && !match(v) {
			continue
		}

		out = append(out, *v)
	}

	return out
}

// matchesBoth reports whether a resource satisfies both optional filters. An
// empty want matches anything.
func matchesBoth(wantA, gotA, wantB, gotB string) bool {
	return (wantA == "" || gotA == wantA) && (wantB == "" || gotB == wantB)
}

// copyTags creates a shallow copy of a tags map.
func copyTags(tags map[string]string) map[string]string {
	out := make(map[string]string, len(tags))
	for k, v := range tags {
		out[k] = v
	}

	return out
}

// copyStrings creates a shallow copy of a string slice.
func copyStrings(src []string) []string {
	if src == nil {
		return nil
	}

	out := make([]string, len(src))
	copy(out, src)

	return out
}

// appendItem returns a fresh slice with v appended. Mutation under a store's
// lock swaps whole slices rather than growing one in place, so a reader
// holding the old slice keeps a consistent view of it.
func appendItem[T any](src []T, v T) []T {
	out := make([]T, len(src), len(src)+1)
	copy(out, src)

	return append(out, v)
}

// withTag returns a fresh tag map carrying key, dropping it when value is empty.
func withTag(tags map[string]string, key, value string) map[string]string {
	out := copyTags(tags)
	if value == "" {
		delete(out, key)

		return out
	}

	out[key] = value

	return out
}

// itoa avoids pulling strconv in for two call sites.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}

	var buf [20]byte

	i := len(buf)

	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}

	return string(buf[i:])
}

// emitMetrics publishes an instance's OCI Compute metrics. It is called
// without m.mu held: the monitoring driver is another service's mock.
func (m *Mock) emitMetrics(ctx context.Context, mon mondriver.Monitoring, instanceID string, values []float64) {
	if mon == nil {
		return
	}

	names := []string{"CpuUtilization", "NetworksBytesIn", "NetworksBytesOut", "DiskBytesRead", "DiskBytesWritten"}
	now := m.opts.Clock.Now()
	data := make([]mondriver.MetricDatum, len(names))

	for i, name := range names {
		data[i] = mondriver.MetricDatum{
			Namespace:  "oci_computeagent",
			MetricName: name,
			Value:      values[i],
			Unit:       "None",
			Dimensions: map[string]string{"resourceId": instanceID},
			Timestamp:  now,
		}
	}

	_ = mon.PutMetricData(ctx, data)
}

// monitoring returns the wired monitoring driver under the read lock, so a
// caller can emit metrics without holding m.mu across another service's call.
func (m *Mock) monitoring() mondriver.Monitoring {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.mon
}
