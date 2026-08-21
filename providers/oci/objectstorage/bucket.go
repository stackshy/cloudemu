package objectstorage

import (
	"context"
	"maps"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// Encryption algorithms the bucket reports: the Oracle-managed default, or a
// customer key held in Vault.
const (
	algorithmAES256 = "AES256"
	algorithmKMS    = "oci:kms"
)

// BucketSpec is a bucket to create. Only Name is required; the compartment
// falls back to the provider's default.
type BucketSpec struct {
	Name                string
	CompartmentID       string
	PublicAccessType    string
	StorageTier         string
	Versioning          string
	KMSKeyID            string
	AutoTiering         string
	ObjectEventsEnabled bool
	Metadata            map[string]string
	FreeformTags        map[string]string
	DefinedTags         map[string]map[string]string
}

// BucketUpdate is a partial bucket update. A nil pointer leaves the field
// alone; OCI's UpdateBucket replaces only what the caller sends.
type BucketUpdate struct {
	CompartmentID       *string
	PublicAccessType    *string
	Versioning          *string
	KMSKeyID            *string
	AutoTiering         *string
	ObjectEventsEnabled *bool
	Metadata            map[string]string
	FreeformTags        map[string]string
	DefinedTags         map[string]map[string]string
}

// Bucket is a bucket as OCI reports it.
type Bucket struct {
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
	ReplicationEnabled  bool
	IsReadOnly          bool
	Metadata            map[string]string
	FreeformTags        map[string]string
	DefinedTags         map[string]map[string]string
	ApproximateCount    int64
	ApproximateSize     int64
}

// validPublicAccess and friends reject a value OCI would not accept, rather
// than storing something the emulator would report back unchanged.
func validPublicAccess(v string) bool {
	switch v {
	case AccessNone, AccessObjectRead, AccessObjectReadWithoutList:
		return true
	}

	return false
}

func validStorageTier(v string) bool {
	switch v {
	case TierStandard, TierArchive, TierInfrequentAccess:
		return true
	}

	return false
}

func validVersioning(v string) bool {
	switch v {
	case VersioningDisabled, VersioningEnabled, VersioningSuspended:
		return true
	}

	return false
}

func validAutoTiering(v string) bool {
	return v == AutoTieringDisabled || v == AutoTieringInfreq
}

// CreateBucketWith creates a bucket with OCI's bucket settings, recording the
// compartment it lands in.
//
//nolint:gocritic // BucketSpec is a request shape, passed by value like the driver's own config structs.
func (m *Mock) CreateBucketWith(_ context.Context, spec BucketSpec) (*Bucket, error) {
	if spec.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "bucket name cannot be empty")
	}

	if err := validateSpec(spec); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.buckets.Has(spec.Name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "bucket %q already exists", spec.Name)
	}

	bkt := &bucketData{
		ID:                  idgen.OCID(typeBucket, m.opts.Realm, m.opts.OCIRegion()),
		Name:                spec.Name,
		Namespace:           m.namespace,
		CompartmentID:       orDefault(spec.CompartmentID, m.opts.CompartmentID),
		CreatedBy:           m.opts.TenancyOCID,
		TimeCreated:         m.now(),
		ETag:                newETag(),
		PublicAccessType:    orDefault(spec.PublicAccessType, AccessNone),
		StorageTier:         orDefault(spec.StorageTier, TierStandard),
		Versioning:          orDefault(spec.Versioning, VersioningDisabled),
		KMSKeyID:            spec.KMSKeyID,
		AutoTiering:         orDefault(spec.AutoTiering, AutoTieringDisabled),
		ObjectEventsEnabled: spec.ObjectEventsEnabled,
		Metadata:            maps.Clone(spec.Metadata),
		FreeformTags:        maps.Clone(spec.FreeformTags),
		DefinedTags:         cloneDefinedTags(spec.DefinedTags),
		objects:             memstore.New[*objectData](),
		multiparts:          memstore.New[*multipartUpload](),
		pars:                memstore.New[*parData](),
		retention:           memstore.New[*retentionRuleData](),
	}

	if bkt.Versioning == VersioningEnabled {
		bkt.versions = make(map[string][]*objectVersion)
	}

	m.buckets.Set(bkt.Name, bkt)

	return projectBucket(bkt), nil
}

//nolint:gocritic // mirrors CreateBucketWith's by-value spec.
func validateSpec(spec BucketSpec) error {
	if spec.PublicAccessType != "" && !validPublicAccess(spec.PublicAccessType) {
		return cerrors.Newf(cerrors.InvalidArgument, "unsupported publicAccessType %q", spec.PublicAccessType)
	}

	if spec.StorageTier != "" && !validStorageTier(spec.StorageTier) {
		return cerrors.Newf(cerrors.InvalidArgument, "unsupported storageTier %q", spec.StorageTier)
	}

	if spec.Versioning != "" && !validVersioning(spec.Versioning) {
		return cerrors.Newf(cerrors.InvalidArgument, "unsupported versioning %q", spec.Versioning)
	}

	if spec.AutoTiering != "" && !validAutoTiering(spec.AutoTiering) {
		return cerrors.Newf(cerrors.InvalidArgument, "unsupported autoTiering %q", spec.AutoTiering)
	}

	return nil
}

// BucketDetails returns one bucket as OCI reports it.
func (m *Mock) BucketDetails(_ context.Context, name string) (*Bucket, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	bkt, err := m.bucketLocked(name)
	if err != nil {
		return nil, err
	}

	return projectBucket(bkt), nil
}

// UpdateBucket applies a partial update, replacing only the fields set.
func (m *Mock) UpdateBucket(_ context.Context, name string, upd BucketUpdate) (*Bucket, error) {
	if err := validateUpdate(upd); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	bkt, err := m.bucketLocked(name)
	if err != nil {
		return nil, err
	}

	applyUpdate(bkt, upd)
	bkt.ETag = newETag()

	return projectBucket(bkt), nil
}

func validateUpdate(upd BucketUpdate) error {
	if upd.PublicAccessType != nil && !validPublicAccess(*upd.PublicAccessType) {
		return cerrors.Newf(cerrors.InvalidArgument, "unsupported publicAccessType %q", *upd.PublicAccessType)
	}

	if upd.Versioning != nil && !validVersioning(*upd.Versioning) {
		return cerrors.Newf(cerrors.InvalidArgument, "unsupported versioning %q", *upd.Versioning)
	}

	if upd.Versioning != nil && *upd.Versioning == VersioningDisabled {
		return cerrors.New(cerrors.InvalidArgument,
			"versioning cannot be set back to Disabled once enabled; use Suspended")
	}

	if upd.AutoTiering != nil && !validAutoTiering(*upd.AutoTiering) {
		return cerrors.Newf(cerrors.InvalidArgument, "unsupported autoTiering %q", *upd.AutoTiering)
	}

	return nil
}

func applyUpdate(bkt *bucketData, upd BucketUpdate) {
	if upd.CompartmentID != nil {
		bkt.CompartmentID = *upd.CompartmentID
	}

	if upd.PublicAccessType != nil {
		bkt.PublicAccessType = *upd.PublicAccessType
	}

	if upd.Versioning != nil {
		setVersioningLocked(bkt, *upd.Versioning)
	}

	if upd.KMSKeyID != nil {
		bkt.KMSKeyID = *upd.KMSKeyID
	}

	if upd.AutoTiering != nil {
		bkt.AutoTiering = *upd.AutoTiering
	}

	if upd.ObjectEventsEnabled != nil {
		bkt.ObjectEventsEnabled = *upd.ObjectEventsEnabled
	}

	if upd.Metadata != nil {
		bkt.Metadata = maps.Clone(upd.Metadata)
	}

	if upd.FreeformTags != nil {
		bkt.FreeformTags = maps.Clone(upd.FreeformTags)
	}

	if upd.DefinedTags != nil {
		bkt.DefinedTags = cloneDefinedTags(upd.DefinedTags)
	}
}

// ListBucketsIn returns the buckets in a compartment, ordered by name.
func (m *Mock) ListBucketsIn(_ context.Context, compartmentID string) ([]Bucket, error) {
	if compartmentID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "compartmentId is required")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	names := m.buckets.Keys()
	sort.Strings(names)

	out := make([]Bucket, 0, len(names))

	for _, n := range names {
		bkt, ok := m.buckets.Get(n)
		if !ok || bkt.CompartmentID != compartmentID {
			continue
		}

		out = append(out, *projectBucket(bkt))
	}

	return out, nil
}

// projectBucket copies a bucket out from under mu, summing the objects it
// holds for the approximate counts OCI reports on request.
func projectBucket(bkt *bucketData) *Bucket {
	var count, size int64

	for _, name := range bkt.objects.Keys() {
		if obj, ok := bkt.objects.Get(name); ok {
			count++
			size += int64(len(obj.Data))
		}
	}

	return &Bucket{
		ID:                  bkt.ID,
		Name:                bkt.Name,
		Namespace:           bkt.Namespace,
		CompartmentID:       bkt.CompartmentID,
		CreatedBy:           bkt.CreatedBy,
		TimeCreated:         bkt.TimeCreated,
		ETag:                bkt.ETag,
		PublicAccessType:    bkt.PublicAccessType,
		StorageTier:         bkt.StorageTier,
		Versioning:          bkt.Versioning,
		KMSKeyID:            bkt.KMSKeyID,
		AutoTiering:         bkt.AutoTiering,
		ObjectEventsEnabled: bkt.ObjectEventsEnabled,
		Metadata:            cloneMeta(bkt.Metadata),
		FreeformTags:        cloneMeta(bkt.FreeformTags),
		DefinedTags:         cloneDefinedTags(bkt.DefinedTags),
		ApproximateCount:    count,
		ApproximateSize:     size,
	}
}

func cloneDefinedTags(in map[string]map[string]string) map[string]map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]map[string]string, len(in))
	for ns, kv := range in {
		out[ns] = maps.Clone(kv)
	}

	return out
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}

	return v
}

// PutBucketTagging sets a bucket's freeform tags.
func (m *Mock) PutBucketTagging(_ context.Context, bucket string, tags map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return err
	}

	bkt.FreeformTags = maps.Clone(tags)

	return nil
}

// GetBucketTagging returns a bucket's freeform tags.
func (m *Mock) GetBucketTagging(_ context.Context, bucket string) (map[string]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return nil, err
	}

	if bkt.FreeformTags == nil {
		return map[string]string{}, nil
	}

	return maps.Clone(bkt.FreeformTags), nil
}

// DeleteBucketTagging clears a bucket's freeform tags.
func (m *Mock) DeleteBucketTagging(_ context.Context, bucket string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return err
	}

	bkt.FreeformTags = nil

	return nil
}

// PutEncryptionConfig points a bucket at a KMS key. OCI encrypts every bucket
// with an Oracle-managed key by default, so the only thing configurable is
// which customer key replaces it — encryption itself cannot be turned off.
func (m *Mock) PutEncryptionConfig(_ context.Context, bucket string, cfg driver.EncryptionConfig) error {
	if !cfg.Enabled {
		return cerrors.New(cerrors.InvalidArgument,
			"OCI Object Storage encryption cannot be disabled; every bucket is encrypted at rest")
	}

	if cfg.Algorithm != "" && cfg.Algorithm != algorithmAES256 && cfg.Algorithm != algorithmKMS {
		return cerrors.Newf(cerrors.InvalidArgument, "unsupported encryption algorithm %q", cfg.Algorithm)
	}

	if cfg.Algorithm == algorithmKMS && cfg.KeyID == "" {
		return cerrors.New(cerrors.InvalidArgument, "kmsKeyId is required for customer-managed encryption")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return err
	}

	bkt.KMSKeyID = cfg.KeyID
	bkt.ETag = newETag()

	return nil
}

// GetEncryptionConfig reports the bucket's encryption. It is always enabled;
// the algorithm reflects whether a customer KMS key is assigned.
func (m *Mock) GetEncryptionConfig(_ context.Context, bucket string) (*driver.EncryptionConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	bkt, err := m.bucketLocked(bucket)
	if err != nil {
		return nil, err
	}

	cfg := driver.EncryptionConfig{Enabled: true, Algorithm: algorithmAES256}
	if bkt.KMSKeyID != "" {
		cfg.Algorithm, cfg.KeyID = algorithmKMS, bkt.KMSKeyID
	}

	return &cfg, nil
}
