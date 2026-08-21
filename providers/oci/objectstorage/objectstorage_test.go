package objectstorage_test

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/oci/objectstorage"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

const (
	testCompartment  = "ocid1.compartment.oc1..aaaaaaaatest"
	otherCompartment = "ocid1.compartment.oc1..aaaaaaaaother"
	testBucket       = "bucket-a"
)

func newMock(t *testing.T) *objectstorage.Mock {
	t.Helper()

	return objectstorage.New(config.NewOptions(
		config.WithRegion("us-ashburn-1"),
		config.WithCompartmentID(testCompartment),
	))
}

// newBucket creates a bucket in testCompartment and fails if the driver refuses.
func newBucket(t *testing.T, m *objectstorage.Mock, name string) *objectstorage.Bucket {
	t.Helper()

	b, err := m.CreateBucketWith(context.Background(), objectstorage.BucketSpec{
		Name: name, CompartmentID: testCompartment,
	})
	require.NoError(t, err)

	return b
}

func TestCreateBucket(t *testing.T) {
	tests := []struct {
		name      string
		spec      objectstorage.BucketSpec
		existing  string
		expectErr cerrors.Code
	}{
		{
			name: "creates bucket",
			spec: objectstorage.BucketSpec{Name: testBucket, CompartmentID: testCompartment},
		},
		{
			name:      "empty name rejected",
			spec:      objectstorage.BucketSpec{CompartmentID: testCompartment},
			expectErr: cerrors.InvalidArgument,
		},
		{
			name:      "duplicate name rejected",
			spec:      objectstorage.BucketSpec{Name: testBucket, CompartmentID: testCompartment},
			existing:  testBucket,
			expectErr: cerrors.AlreadyExists,
		},
		{
			name: "unknown public access type rejected",
			spec: objectstorage.BucketSpec{
				Name: testBucket, CompartmentID: testCompartment, PublicAccessType: "Everyone",
			},
			expectErr: cerrors.InvalidArgument,
		},
		{
			name: "unknown storage tier rejected",
			spec: objectstorage.BucketSpec{
				Name: testBucket, CompartmentID: testCompartment, StorageTier: "Glacier",
			},
			expectErr: cerrors.InvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newMock(t)
			if tc.existing != "" {
				newBucket(t, m, tc.existing)
			}

			b, err := m.CreateBucketWith(context.Background(), tc.spec)
			if tc.expectErr != cerrors.OK {
				require.Error(t, err)
				assert.Equal(t, tc.expectErr, cerrors.GetCode(err))

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.spec.Name, b.Name)
			assert.Equal(t, testCompartment, b.CompartmentID)
			assert.Equal(t, objectstorage.AccessNone, b.PublicAccessType)
			assert.Equal(t, objectstorage.TierStandard, b.StorageTier)
			assert.Equal(t, objectstorage.VersioningDisabled, b.Versioning)
			assert.Equal(t, m.Namespace(), b.Namespace)
		})
	}
}

func TestBucketOCIDShape(t *testing.T) {
	m := newMock(t)
	b := newBucket(t, m, testBucket)

	assert.Regexp(t, regexp.MustCompile(`^ocid1\.bucket\.oc1\.iad\.[a-z0-9]+$`), b.ID)

	par, err := m.CreatePAR(context.Background(), testBucket, objectstorage.PARSpec{
		Name: "par", ObjectName: "k", AccessType: objectstorage.PARObjectRead,
	})
	require.NoError(t, err)
	assert.Regexp(t, regexp.MustCompile(`^ocid1\.preauthenticatedrequest\.oc1\.iad\.[a-z0-9]+$`), par.ID)

	rule, err := m.CreateRetentionRule(context.Background(), testBucket, objectstorage.RetentionRuleSpec{
		DisplayName: "hold",
	})
	require.NoError(t, err)
	assert.Regexp(t, regexp.MustCompile(`^ocid1\.retentionrule\.oc1\.iad\.[a-z0-9]+$`), rule.ID)
}

func TestNamespaceIsStablePerTenancy(t *testing.T) {
	a := objectstorage.New(config.NewOptions(config.WithTenancyOCID("ocid1.tenancy.oc1..a")))
	b := objectstorage.New(config.NewOptions(config.WithTenancyOCID("ocid1.tenancy.oc1..a")))
	c := objectstorage.New(config.NewOptions(config.WithTenancyOCID("ocid1.tenancy.oc1..b")))

	assert.Equal(t, a.Namespace(), b.Namespace())
	assert.NotEqual(t, a.Namespace(), c.Namespace())
	assert.Len(t, a.Namespace(), 12)
}

func TestListBucketsInFiltersByCompartment(t *testing.T) {
	m := newMock(t)
	newBucket(t, m, "mine")

	_, err := m.CreateBucketWith(context.Background(), objectstorage.BucketSpec{
		Name: "theirs", CompartmentID: otherCompartment,
	})
	require.NoError(t, err)

	mine, err := m.ListBucketsIn(context.Background(), testCompartment)
	require.NoError(t, err)
	require.Len(t, mine, 1)
	assert.Equal(t, "mine", mine[0].Name)

	theirs, err := m.ListBucketsIn(context.Background(), otherCompartment)
	require.NoError(t, err)
	require.Len(t, theirs, 1)
	assert.Equal(t, "theirs", theirs[0].Name)

	_, err = m.ListBucketsIn(context.Background(), "")
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	assert.Equal(t, testCompartment, m.Scope("mine").Compartment)
	assert.Equal(t, otherCompartment, m.Scope("theirs").Compartment)
}

func TestBucketLifecycleCRUD(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	newBucket(t, m, testBucket)

	got, err := m.BucketDetails(ctx, testBucket)
	require.NoError(t, err)
	assert.Equal(t, testBucket, got.Name)

	_, err = m.BucketDetails(ctx, "missing")
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	access := objectstorage.AccessObjectRead
	updated, err := m.UpdateBucket(ctx, testBucket, objectstorage.BucketUpdate{PublicAccessType: &access})
	require.NoError(t, err)
	assert.Equal(t, objectstorage.AccessObjectRead, updated.PublicAccessType)
	assert.NotEqual(t, got.ETag, updated.ETag)

	bogus := "Everyone"
	_, err = m.UpdateBucket(ctx, testBucket, objectstorage.BucketUpdate{PublicAccessType: &bogus})
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	require.NoError(t, m.PutObject(ctx, testBucket, "k", []byte("v"), "text/plain", nil))
	err = m.DeleteBucket(ctx, testBucket)
	require.Error(t, err)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))

	require.NoError(t, m.DeleteObject(ctx, testBucket, "k"))
	require.NoError(t, m.DeleteBucket(ctx, testBucket))

	err = m.DeleteBucket(ctx, testBucket)
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestObjectCRUD(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	newBucket(t, m, testBucket)

	details, err := m.PutObjectWith(ctx, testBucket, "dir/a.txt", []byte("hello"), objectstorage.PutOptions{
		ContentType: "text/plain",
		Metadata:    map[string]string{"owner": "ada"},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(5), details.Size)
	assert.NotEmpty(t, details.MD5)
	assert.Equal(t, objectstorage.TierStandard, details.StorageTier)

	obj, err := m.GetObject(ctx, testBucket, "dir/a.txt")
	require.NoError(t, err)
	assert.Equal(t, []byte("hello"), obj.Data)
	assert.Equal(t, "ada", obj.Info.Metadata["owner"])

	_, err = m.GetObject(ctx, testBucket, "missing")
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	_, err = m.GetObject(ctx, "no-bucket", "dir/a.txt")
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	info, err := m.HeadObject(ctx, testBucket, "dir/a.txt")
	require.NoError(t, err)
	assert.Equal(t, int64(5), info.Size)

	require.NoError(t, m.DeleteObject(ctx, testBucket, "dir/a.txt"))

	err = m.DeleteObject(ctx, testBucket, "dir/a.txt")
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestListObjectsPrefixAndDelimiter(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	newBucket(t, m, testBucket)

	for _, k := range []string{"a/1", "a/2", "a/sub/3", "b/1", "top"} {
		require.NoError(t, m.PutObject(ctx, testBucket, k, []byte("x"), "text/plain", nil))
	}

	res, err := m.ListObjects(ctx, testBucket, driver.ListOptions{Prefix: "a/", Delimiter: "/"})
	require.NoError(t, err)
	require.Len(t, res.Objects, 2)
	assert.Equal(t, "a/1", res.Objects[0].Key)
	assert.Equal(t, []string{"a/sub/"}, res.CommonPrefixes)

	all, err := m.ListObjects(ctx, testBucket, driver.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, all.Objects, 5)

	_, err = m.ListObjects(ctx, "missing", driver.ListOptions{})
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestRenameAndCopyObject(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	newBucket(t, m, testBucket)
	newBucket(t, m, "bucket-b")
	require.NoError(t, m.PutObject(ctx, testBucket, "old", []byte("v"), "text/plain", nil))

	renamed, err := m.RenameObject(ctx, testBucket, "old", "new")
	require.NoError(t, err)
	assert.Equal(t, "new", renamed.Name)

	_, err = m.GetObject(ctx, testBucket, "old")
	require.Error(t, err)

	_, err = m.RenameObject(ctx, testBucket, "old", "other")
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	require.NoError(t, m.CopyObject(ctx, "bucket-b", "copied", driver.CopySource{Bucket: testBucket, Key: "new"}))

	got, err := m.GetObject(ctx, "bucket-b", "copied")
	require.NoError(t, err)
	assert.Equal(t, []byte("v"), got.Data)

	err = m.CopyObject(ctx, "bucket-b", "x", driver.CopySource{Bucket: testBucket, Key: "absent"})
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestMultipartUpload(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	newBucket(t, m, testBucket)

	up, err := m.CreateMultipartUploadWith(ctx, testBucket, objectstorage.MultipartUploadSpec{
		Object: "big", ContentType: "application/octet-stream",
	})
	require.NoError(t, err)

	p1, err := m.UploadPart(ctx, testBucket, "big", up.UploadID, 1, []byte("aaa"))
	require.NoError(t, err)
	p2, err := m.UploadPart(ctx, testBucket, "big", up.UploadID, 2, []byte("bbb"))
	require.NoError(t, err)

	_, err = m.UploadPart(ctx, testBucket, "big", up.UploadID, 0, []byte("x"))
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	parts, err := m.ListParts(ctx, testBucket, "big", up.UploadID)
	require.NoError(t, err)
	require.Len(t, parts, 2)

	uploads, err := m.ListMultipartUploads(ctx, testBucket)
	require.NoError(t, err)
	require.Len(t, uploads, 1)

	require.NoError(t, m.CompleteMultipartUpload(ctx, testBucket, "big", up.UploadID,
		[]driver.UploadPart{{PartNumber: p2.PartNumber}, {PartNumber: p1.PartNumber}}))

	obj, err := m.GetObject(ctx, testBucket, "big")
	require.NoError(t, err)
	assert.Equal(t, []byte("aaabbb"), obj.Data)

	err = m.AbortMultipartUpload(ctx, testBucket, "big", up.UploadID)
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestVersioning(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	newBucket(t, m, testBucket)

	require.NoError(t, m.SetVersioningStatus(ctx, testBucket, objectstorage.VersioningEnabled))

	status, err := m.VersioningStatus(ctx, testBucket)
	require.NoError(t, err)
	assert.Equal(t, objectstorage.VersioningEnabled, status)

	require.NoError(t, m.PutObject(ctx, testBucket, "k", []byte("v1"), "text/plain", nil))
	first, err := m.HeadObject(ctx, testBucket, "k")
	require.NoError(t, err)
	require.NoError(t, m.PutObject(ctx, testBucket, "k", []byte("v2"), "text/plain", nil))

	old, err := m.GetObjectVersion(ctx, testBucket, "k", first.VersionID)
	require.NoError(t, err)
	assert.Equal(t, []byte("v1"), old.Data)

	_, err = m.GetObjectVersion(ctx, testBucket, "k", "nope")
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	vid, marker, err := m.DeleteObjectVersion(ctx, testBucket, "k", "")
	require.NoError(t, err)
	assert.True(t, marker)
	assert.NotEmpty(t, vid)

	list, err := m.ListObjectVersions(ctx, testBucket, driver.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, list.Versions, 3)
	assert.True(t, list.Versions[0].DeleteMarker)

	err = m.SetVersioningStatus(ctx, testBucket, objectstorage.VersioningDisabled)
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
}

func TestRetentionRules(t *testing.T) {
	clock := config.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	m := objectstorage.New(config.NewOptions(
		config.WithRegion("us-ashburn-1"),
		config.WithCompartmentID(testCompartment),
		config.WithClock(clock),
	))
	ctx := context.Background()
	newBucket(t, m, testBucket)
	require.NoError(t, m.PutObject(ctx, testBucket, "k", []byte("v"), "text/plain", nil))

	rule, err := m.CreateRetentionRule(ctx, testBucket, objectstorage.RetentionRuleSpec{
		DisplayName: "thirty-days",
		Duration:    &objectstorage.RetentionDuration{TimeAmount: 30, TimeUnit: objectstorage.RetentionDays},
	})
	require.NoError(t, err)

	err = m.DeleteObject(ctx, testBucket, "k")
	require.Error(t, err)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))

	rules, err := m.ListRetentionRules(ctx, testBucket)
	require.NoError(t, err)
	require.Len(t, rules, 1)

	_, err = m.GetRetentionRule(ctx, testBucket, "missing")
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	_, err = m.CreateRetentionRule(ctx, testBucket, objectstorage.RetentionRuleSpec{
		Duration: &objectstorage.RetentionDuration{TimeAmount: 1, TimeUnit: "FORTNIGHTS"},
	})
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	require.NoError(t, m.DeleteRetentionRule(ctx, testBucket, rule.ID))

	clock.Advance(31 * 24 * time.Hour)
	require.NoError(t, m.DeleteObject(ctx, testBucket, "k"))
}

func TestRetentionRuleLockCannotBeWeakened(t *testing.T) {
	clock := config.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	m := objectstorage.New(config.NewOptions(
		config.WithRegion("us-ashburn-1"),
		config.WithCompartmentID(testCompartment),
		config.WithClock(clock),
	))
	ctx := context.Background()
	newBucket(t, m, testBucket)

	lockAt := clock.Now().Add(-time.Hour)
	rule, err := m.CreateRetentionRule(ctx, testBucket, objectstorage.RetentionRuleSpec{
		DisplayName:    "locked",
		Duration:       &objectstorage.RetentionDuration{TimeAmount: 10, TimeUnit: objectstorage.RetentionDays},
		TimeRuleLocked: &lockAt,
	})
	require.NoError(t, err)

	_, err = m.UpdateRetentionRule(ctx, testBucket, rule.ID, objectstorage.RetentionRuleSpec{
		Duration: &objectstorage.RetentionDuration{TimeAmount: 5, TimeUnit: objectstorage.RetentionDays},
	})
	require.Error(t, err)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))

	_, err = m.UpdateRetentionRule(ctx, testBucket, rule.ID, objectstorage.RetentionRuleSpec{
		Duration: &objectstorage.RetentionDuration{TimeAmount: 20, TimeUnit: objectstorage.RetentionDays},
	})
	require.NoError(t, err)

	err = m.DeleteRetentionRule(ctx, testBucket, rule.ID)
	require.Error(t, err)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))
}

func TestPreauthenticatedRequests(t *testing.T) {
	clock := config.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	m := objectstorage.New(config.NewOptions(
		config.WithRegion("us-ashburn-1"),
		config.WithCompartmentID(testCompartment),
		config.WithClock(clock),
	))
	ctx := context.Background()
	newBucket(t, m, testBucket)

	par, err := m.CreatePAR(ctx, testBucket, objectstorage.PARSpec{
		Name: "read-k", ObjectName: "k", AccessType: objectstorage.PARObjectRead,
		TimeExpires: clock.Now().Add(time.Hour),
	})
	require.NoError(t, err)
	assert.Contains(t, par.AccessURI, "/n/"+m.Namespace()+"/b/"+testBucket+"/o/k")

	got, err := m.GetPAR(ctx, testBucket, par.ID)
	require.NoError(t, err)
	assert.Empty(t, got.AccessURI, "OCI returns the access URI only from create")

	pars, err := m.ListPARs(ctx, testBucket, "")
	require.NoError(t, err)
	require.Len(t, pars, 1)

	_, err = m.CreatePAR(ctx, testBucket, objectstorage.PARSpec{
		Name: "bad", AccessType: objectstorage.PARObjectRead,
	})
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	_, err = m.CreatePAR(ctx, testBucket, objectstorage.PARSpec{
		Name: "bad", ObjectName: "k", AccessType: "Whatever",
	})
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	require.NoError(t, m.DeletePAR(ctx, testBucket, par.ID))

	err = m.DeletePAR(ctx, testBucket, par.ID)
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestPARExpiryIsEnforced(t *testing.T) {
	clock := config.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	m := objectstorage.New(config.NewOptions(
		config.WithRegion("us-ashburn-1"),
		config.WithCompartmentID(testCompartment),
		config.WithClock(clock),
	))
	ctx := context.Background()
	newBucket(t, m, testBucket)

	url, err := m.GeneratePresignedURL(ctx, driver.PresignedURLRequest{
		Bucket: testBucket, Key: "k", Method: "GET", ExpiresIn: time.Hour,
	})
	require.NoError(t, err)
	require.Contains(t, url.URL, "/p/")

	token := tokenFrom(t, url.URL)

	resolved, err := m.ResolvePAR(ctx, token)
	require.NoError(t, err)
	assert.Equal(t, objectstorage.PARObjectRead, resolved.AccessType)
	assert.True(t, objectstorage.PARAllows(resolved, "GET", "k"))
	assert.False(t, objectstorage.PARAllows(resolved, "PUT", "k"))
	assert.False(t, objectstorage.PARAllows(resolved, "GET", "other"))

	clock.Advance(2 * time.Hour)

	_, err = m.ResolvePAR(ctx, token)
	require.Error(t, err)
	assert.Equal(t, cerrors.PermissionDenied, cerrors.GetCode(err))
}

// tokenFrom extracts the redemption token from a PAR access URL.
func tokenFrom(t *testing.T, url string) string {
	t.Helper()

	re := regexp.MustCompile(`/p/([^/]+)/n/`)
	match := re.FindStringSubmatch(url)
	require.Len(t, match, 2)

	return match[1]
}

func TestUnsupportedOperationsAreNamed(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	newBucket(t, m, testBucket)

	tests := []struct {
		name string
		call func() error
	}{
		{"PutBucketPolicy", func() error { return m.PutBucketPolicy(ctx, testBucket, driver.BucketPolicy{}) }},
		{"GetBucketPolicy", func() error { _, err := m.GetBucketPolicy(ctx, testBucket); return err }},
		{"PutCORSConfig", func() error { return m.PutCORSConfig(ctx, testBucket, driver.CORSConfig{}) }},
		{"GetObjectTagging", func() error { _, err := m.GetObjectTagging(ctx, testBucket, "k"); return err }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			require.Error(t, err)
			assert.Equal(t, cerrors.Unimplemented, cerrors.GetCode(err))
			assert.Contains(t, err.Error(), tc.name)
		})
	}
}

func TestEncryptionAndTagging(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	newBucket(t, m, testBucket)

	cfg, err := m.GetEncryptionConfig(ctx, testBucket)
	require.NoError(t, err)
	assert.True(t, cfg.Enabled)
	assert.Equal(t, "AES256", cfg.Algorithm)

	require.NoError(t, m.PutEncryptionConfig(ctx, testBucket, driver.EncryptionConfig{
		Enabled: true, Algorithm: "oci:kms", KeyID: "ocid1.key.oc1..aaaa",
	}))

	cfg, err = m.GetEncryptionConfig(ctx, testBucket)
	require.NoError(t, err)
	assert.Equal(t, "ocid1.key.oc1..aaaa", cfg.KeyID)

	err = m.PutEncryptionConfig(ctx, testBucket, driver.EncryptionConfig{Enabled: false})
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	require.NoError(t, m.PutBucketTagging(ctx, testBucket, map[string]string{"env": "dev"}))

	tags, err := m.GetBucketTagging(ctx, testBucket)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"env": "dev"}, tags)

	require.NoError(t, m.DeleteBucketTagging(ctx, testBucket))

	tags, err = m.GetBucketTagging(ctx, testBucket)
	require.NoError(t, err)
	assert.Empty(t, tags)
}
