// Package objectstorage provides an in-memory mock implementation of OCI
// Object Storage. It implements the portable storage driver: an OCI bucket is
// the bucket, an OCI object is the object. Buckets live under a tenancy
// namespace, which the driver derives from the tenancy OCID and exposes to the
// wire layer; the driver itself keys buckets by name, as the portable
// interface does.
package objectstorage

import (
	"context"
	"crypto/md5" //nolint:gosec // OCI reports object content MD5; not a security primitive
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"maps"
	"sort"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

const timeFormat = time.RFC3339

// OCID resource type segments.
const (
	typeBucket        = "bucket"
	typePAR           = "preauthenticatedrequest"
	typeRetentionRule = "retentionrule"
)

// namespaceLen is the length of the generated tenancy namespace. Real
// namespaces are short opaque lowercase strings, not the tenancy OCID.
const namespaceLen = 12

// Public access types a bucket may carry.
const (
	AccessNone                  = "NoPublicAccess"
	AccessObjectRead            = "ObjectRead"
	AccessObjectReadWithoutList = "ObjectReadWithoutList"
)

// Bucket storage tiers.
const (
	TierStandard         = "Standard"
	TierArchive          = "Archive"
	TierInfrequentAccess = "InfrequentAccess"
)

// Object versioning states. OCI has three, unlike S3's two.
const (
	VersioningDisabled  = "Disabled"
	VersioningEnabled   = "Enabled"
	VersioningSuspended = "Suspended"
)

// nullVersionID is the version reported for objects written while versioning
// was suspended or never enabled.
const nullVersionID = "null"

// Auto-tiering settings.
const (
	AutoTieringDisabled = "Disabled"
	AutoTieringInfreq   = "InfrequentAccess"
)

// Retention rule time units.
const (
	RetentionDays  = "DAYS"
	RetentionYears = "YEARS"
)

// PAR access types.
const (
	PARObjectRead         = "ObjectRead"
	PARObjectWrite        = "ObjectWrite"
	PARObjectReadWrite    = "ObjectReadWrite"
	PARAnyObjectRead      = "AnyObjectRead"
	PARAnyObjectWrite     = "AnyObjectWrite"
	PARAnyObjectReadWrite = "AnyObjectReadWrite"
)

// metricNamespace is the OCI Monitoring namespace Object Storage publishes to.
const metricNamespace = "oci_objectstorage"

// Compile-time check that Mock implements driver.Bucket.
var _ driver.Bucket = (*Mock)(nil)

// Optional driver capabilities, discovered by type assertion.
var _ driver.VersionedBucket = (*Mock)(nil)

// objectData is one stored object at its current version.
type objectData struct {
	Name         string
	Data         []byte
	ContentType  string
	ContentMD5   string
	ETag         string
	TimeCreated  string
	TimeModified string
	Metadata     map[string]string
	StorageTier  string
	VersionID    string
}

// objectVersion is one entry in a name's version chain, oldest first. A
// delete marker carries no data.
type objectVersion struct {
	versionID    string
	data         []byte
	contentType  string
	contentMD5   string
	etag         string
	timeModified string
	metadata     map[string]string
	storageTier  string
	deleteMarker bool
}

// multipartUpload is an in-progress multipart upload. Parts are buffered until
// the upload is committed.
type multipartUpload struct {
	id          string
	object      string
	contentType string
	metadata    map[string]string
	storageTier string
	parts       map[int][]byte
	timeCreated string
}

// bucketData is a bucket and everything hanging off it.
type bucketData struct {
	ID                  string
	Name                string
	Namespace           string
	CompartmentID       string
	CreatedBy           string
	TimeCreated         string
	ETag                string
	PublicAccessType    string
	StorageTier         string
	Versioning          string
	KMSKeyID            string
	AutoTiering         string
	ObjectEventsEnabled bool
	Metadata            map[string]string
	FreeformTags        map[string]string
	DefinedTags         map[string]map[string]string

	objects    *memstore.Store[*objectData]
	multiparts *memstore.Store[*multipartUpload]
	pars       *memstore.Store[*parData]
	retention  *memstore.Store[*retentionRuleData]
	lifecycle  *driver.LifecycleConfig
	// versions maps an object name to its chain, oldest first. Only populated
	// once versioning has been enabled on the bucket.
	versions map[string][]*objectVersion
}

// Mock is an in-memory mock implementation of OCI Object Storage.
type Mock struct {
	// mu guards the fields of stored values and spans the reads and writes a
	// single operation makes across a bucket's stores. Each store locks its
	// own map, but the pointers it hands back are mutated in place while list
	// calls walk them, and a version chain is read before the current object
	// is rewritten.
	mu sync.RWMutex

	buckets    *memstore.Store[*bucketData]
	namespace  string
	opts       *config.Options
	monitoring mondriver.Monitoring
}

// New creates a new OCI Object Storage mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		buckets:   memstore.New[*bucketData](),
		namespace: namespaceFor(opts.TenancyOCID),
		opts:      opts,
	}
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.monitoring = mon
}

// namespaceFor derives the tenancy's Object Storage namespace. Real OCI mints
// an opaque short string per tenancy; deriving it keeps it stable across runs.
func namespaceFor(tenancyOCID string) string {
	sum := sha256.Sum256([]byte(tenancyOCID))

	return hex.EncodeToString(sum[:])[:namespaceLen]
}

// Namespace returns the tenancy's Object Storage namespace.
func (m *Mock) Namespace() string { return m.namespace }

// Scope returns the compartment a bucket was created in. It is an OPTIONAL
// capability, discovered by type assertion: the portable Bucket driver has no
// compartment parameter, so OCI scoping is exposed alongside it.
func (m *Mock) Scope(bucket string) scope.Scope {
	m.mu.RLock()
	defer m.mu.RUnlock()

	bkt, ok := m.buckets.Get(bucket)
	if !ok {
		return scope.Scope{}
	}

	return scope.Scope{Compartment: bkt.CompartmentID}
}

// CreateBucket creates a bucket in the provider's default compartment. The
// OCI wire layer uses CreateBucketWith, which carries the caller's compartment
// and bucket settings.
func (m *Mock) CreateBucket(ctx context.Context, name string) error {
	_, err := m.CreateBucketWith(ctx, BucketSpec{Name: name})

	return err
}

// DeleteBucket removes an empty bucket. OCI refuses to delete a bucket that
// still holds objects or uncommitted multipart uploads.
func (m *Mock) DeleteBucket(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bkt, ok := m.buckets.Get(name)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "bucket %q not found", name)
	}

	if bkt.objects.Len() > 0 || len(bkt.versions) > 0 {
		return cerrors.Newf(cerrors.FailedPrecondition, "bucket %q is not empty", name)
	}

	if bkt.multiparts.Len() > 0 {
		return cerrors.Newf(cerrors.FailedPrecondition, "bucket %q has uncommitted multipart uploads", name)
	}

	m.buckets.Delete(name)

	return nil
}

// ListBuckets returns every bucket, unfiltered. The OCI wire layer uses
// ListBucketsIn, which OCI requires a compartment for.
func (m *Mock) ListBuckets(_ context.Context) ([]driver.BucketInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.bucketInfosLocked(""), nil
}

// bucketInfosLocked projects the buckets in a compartment (all of them when
// compartmentID is empty) onto the portable shape, ordered by name.
func (m *Mock) bucketInfosLocked(compartmentID string) []driver.BucketInfo {
	names := m.buckets.Keys()
	sort.Strings(names)

	out := make([]driver.BucketInfo, 0, len(names))

	for _, n := range names {
		bkt, ok := m.buckets.Get(n)
		if !ok || (compartmentID != "" && bkt.CompartmentID != compartmentID) {
			continue
		}

		out = append(out, driver.BucketInfo{
			Name:      bkt.Name,
			Region:    m.opts.OCIRegion(),
			CreatedAt: bkt.TimeCreated,
		})
	}

	return out
}

// bucketLocked fetches a bucket or the NotFound error naming it.
func (m *Mock) bucketLocked(name string) (*bucketData, error) {
	bkt, ok := m.buckets.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "bucket %q not found", name)
	}

	return bkt, nil
}

// objectLocked fetches an object's current version or the NotFound error.
func objectLocked(bkt *bucketData, name string) (*objectData, error) {
	obj, ok := bkt.objects.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "object %q not found in bucket %q", name, bkt.Name)
	}

	return obj, nil
}

func (m *Mock) now() string { return m.opts.Clock.Now().UTC().Format(timeFormat) }

// newETag mints the opaque entity tag OCI stamps on buckets and objects.
func newETag() string { return idgen.GenerateID("") }

func contentMD5(data []byte) string {
	sum := md5.Sum(data) //nolint:gosec // OCI reports content MD5; not a security primitive

	return base64.StdEncoding.EncodeToString(sum[:])
}

func objectETag(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// emitMetric publishes one Object Storage metric, if monitoring is wired.
// Callers must not hold mu: the monitoring backend is a separate driver.
func (m *Mock) emitMetric(name string, value float64, unit, bucket string) {
	m.mu.RLock()
	mon := m.monitoring
	m.mu.RUnlock()

	if mon == nil {
		return
	}

	_ = mon.PutMetricData(context.Background(), []mondriver.MetricDatum{{
		Namespace: metricNamespace, MetricName: name, Value: value, Unit: unit,
		Dimensions: map[string]string{"bucketName": bucket, "resourceID": bucket},
		Timestamp:  m.opts.Clock.Now(),
	}})
}

func cloneMeta(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	return maps.Clone(in)
}

func cloneBytes(in []byte) []byte {
	out := make([]byte, len(in))
	copy(out, in)

	return out
}

// NamespaceMetadata is the tenancy namespace's Object Storage metadata: the
// compartments the S3 and Swift compatibility endpoints create buckets in.
type NamespaceMetadata struct {
	Namespace                 string
	DefaultS3CompartmentID    string
	DefaultSwiftCompartmentID string
}

// Metadata returns the namespace metadata. Both compatibility endpoints
// default to the provider's compartment.
func (m *Mock) Metadata(_ context.Context) NamespaceMetadata {
	return NamespaceMetadata{
		Namespace:                 m.namespace,
		DefaultS3CompartmentID:    m.opts.CompartmentID,
		DefaultSwiftCompartmentID: m.opts.CompartmentID,
	}
}
