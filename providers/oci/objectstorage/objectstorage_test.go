package objectstorage_test

import (
	"context"
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/oci/monitoring"
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

func TestLifecyclePolicyExpiry(t *testing.T) {
	clock := config.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	m := objectstorage.New(config.NewOptions(
		config.WithRegion("us-ashburn-1"),
		config.WithCompartmentID(testCompartment),
		config.WithClock(clock),
	))
	ctx := context.Background()
	newBucket(t, m, testBucket)

	_, err := m.GetLifecycleConfig(ctx, testBucket)
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	expired, err := m.EvaluateLifecycle(ctx, testBucket)
	require.NoError(t, err)
	assert.Empty(t, expired, "no policy ages nothing out")

	require.NoError(t, m.PutObject(ctx, testBucket, "logs/old.txt", []byte("a"), "text/plain", nil))
	require.NoError(t, m.PutObject(ctx, testBucket, "keep/old.txt", []byte("b"), "text/plain", nil))
	require.NoError(t, m.PutObject(ctx, testBucket, "logs/disabled.txt", []byte("c"), "text/plain", nil))

	require.NoError(t, m.PutLifecycleConfig(ctx, testBucket, driver.LifecycleConfig{Rules: []driver.LifecycleRule{
		{ID: "expire-logs", Prefix: "logs/", ExpirationDays: 30, Enabled: true},
		{ID: "off", Prefix: "logs/disabled", ExpirationDays: 1, Enabled: false},
		{ID: "keep", Prefix: "keep/", Enabled: true},
	}}))

	stored, err := m.GetLifecycleConfig(ctx, testBucket)
	require.NoError(t, err)
	require.Len(t, stored.Rules, 3)

	expired, err = m.EvaluateLifecycle(ctx, testBucket)
	require.NoError(t, err)
	assert.Empty(t, expired, "nothing has aged out yet")

	// One hour short of the window, then over it.
	clock.Advance(30*hoursPerDay*time.Hour - time.Hour)

	expired, err = m.EvaluateLifecycle(ctx, testBucket)
	require.NoError(t, err)
	assert.Empty(t, expired, "the window is inclusive of ExpirationDays, not shorter")

	clock.Advance(time.Hour)

	expired, err = m.EvaluateLifecycle(ctx, testBucket)
	require.NoError(t, err)
	assert.Equal(t, []string{"logs/disabled.txt", "logs/old.txt"}, expired,
		"only the enabled logs/ rule ages objects out; keep/ has no ExpirationDays")

	_, err = m.EvaluateLifecycle(ctx, "missing")
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	require.NoError(t, m.DeleteLifecyclePolicy(ctx, testBucket))

	err = m.DeleteLifecyclePolicy(ctx, testBucket)
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	require.Error(t, m.PutLifecycleConfig(ctx, "missing", driver.LifecycleConfig{}))
}

// hoursPerDay mirrors the provider's own day length.
const hoursPerDay = 24

func TestPortableVersioningWrappers(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	newBucket(t, m, testBucket)

	on, err := m.GetBucketVersioning(ctx, testBucket)
	require.NoError(t, err)
	assert.False(t, on)

	require.NoError(t, m.SetBucketVersioning(ctx, testBucket, true))

	on, err = m.GetBucketVersioning(ctx, testBucket)
	require.NoError(t, err)
	assert.True(t, on)

	status, err := m.VersioningStatus(ctx, testBucket)
	require.NoError(t, err)
	assert.Equal(t, objectstorage.VersioningEnabled, status)

	// Disabling suspends: OCI never returns a bucket to Disabled.
	require.NoError(t, m.SetBucketVersioning(ctx, testBucket, false))

	status, err = m.VersioningStatus(ctx, testBucket)
	require.NoError(t, err)
	assert.Equal(t, objectstorage.VersioningSuspended, status)

	on, err = m.GetBucketVersioning(ctx, testBucket)
	require.NoError(t, err)
	assert.False(t, on)

	require.Error(t, m.SetBucketVersioning(ctx, "missing", true))

	_, err = m.GetBucketVersioning(ctx, "missing")
	require.Error(t, err)
}

func TestHeadObjectVersion(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	newBucket(t, m, testBucket)

	require.NoError(t, m.SetVersioningStatus(ctx, testBucket, objectstorage.VersioningEnabled))
	require.NoError(t, m.PutObject(ctx, testBucket, "k", []byte("v1"), "text/plain", nil))

	first, err := m.HeadObject(ctx, testBucket, "k")
	require.NoError(t, err)

	require.NoError(t, m.PutObject(ctx, testBucket, "k", []byte("v2-longer"), "text/plain", nil))

	old, err := m.HeadObjectVersion(ctx, testBucket, "k", first.VersionID)
	require.NoError(t, err)
	assert.Equal(t, first.VersionID, old.VersionID)
	assert.Equal(t, int64(2), old.Size)

	// An empty versionID heads the current object.
	current, err := m.HeadObjectVersion(ctx, testBucket, "k", "")
	require.NoError(t, err)
	assert.Equal(t, int64(9), current.Size)
	assert.NotEqual(t, first.VersionID, current.VersionID)

	_, err = m.HeadObjectVersion(ctx, testBucket, "k", "nope")
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	_, err = m.HeadObjectVersion(ctx, "missing", "k", "some-version")
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

// The portable driver.Bucket methods are thin wrappers over the OCI-shaped
// ones; a consumer wired through services/storage/driver only sees these.
func TestPortableDriverWrappers(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	require.NoError(t, m.CreateBucket(ctx, testBucket))

	buckets, err := m.ListBuckets(ctx)
	require.NoError(t, err)
	require.Len(t, buckets, 1)
	assert.Equal(t, testBucket, buckets[0].Name)
	assert.Equal(t, "us-ashburn-1", buckets[0].Region)

	details, err := m.BucketDetails(ctx, testBucket)
	require.NoError(t, err)
	assert.Equal(t, testCompartment, details.CompartmentID, "CreateBucket lands in the default compartment")

	up, err := m.CreateMultipartUpload(ctx, testBucket, "big", "application/octet-stream")
	require.NoError(t, err)
	assert.Equal(t, "big", up.Key)
	require.NoError(t, m.AbortMultipartUpload(ctx, testBucket, "big", up.UploadID))

	require.Error(t, m.CreateBucket(ctx, testBucket), "duplicate name")
}

func TestObjectDetailsAndTiering(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	newBucket(t, m, testBucket)

	_, err := m.PutObjectWith(ctx, testBucket, "a.txt", []byte("hello"), objectstorage.PutOptions{
		ContentType: "text/plain", Metadata: map[string]string{"owner": "ana"},
	})
	require.NoError(t, err)

	d, err := m.ObjectDetailsOf(ctx, testBucket, "a.txt")
	require.NoError(t, err)
	assert.Equal(t, int64(5), d.Size)
	assert.Equal(t, objectstorage.TierStandard, d.StorageTier)
	assert.NotEmpty(t, d.MD5)
	assert.Equal(t, map[string]string{"owner": "ana"}, d.Metadata)

	_, err = m.ObjectDetailsOf(ctx, testBucket, "missing")
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	_, err = m.ObjectDetailsOf(ctx, "missing", "a.txt")
	require.Error(t, err)

	require.NoError(t, m.PutObject(ctx, testBucket, "logs/b.txt", []byte("x"), "text/plain", nil))

	items, prefixes, next, err := m.ListObjectDetails(ctx, testBucket, driver.ListOptions{Delimiter: "/"})
	require.NoError(t, err)
	assert.Empty(t, next)
	assert.Equal(t, []string{"logs/"}, prefixes)
	require.Len(t, items, 1)
	assert.Equal(t, "a.txt", items[0].Name)

	items, _, _, err = m.ListObjectDetails(ctx, testBucket, driver.ListOptions{Prefix: "logs/"})
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "logs/b.txt", items[0].Name)

	_, _, _, err = m.ListObjectDetails(ctx, "missing", driver.ListOptions{})
	require.Error(t, err)

	require.NoError(t, m.UpdateObjectStorageTier(ctx, testBucket, "a.txt", objectstorage.TierArchive))

	d, err = m.ObjectDetailsOf(ctx, testBucket, "a.txt")
	require.NoError(t, err)
	assert.Equal(t, objectstorage.TierArchive, d.StorageTier)

	err = m.UpdateObjectStorageTier(ctx, testBucket, "a.txt", "Glacier")
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err), "an unmodelled tier is named, not stored")

	require.Error(t, m.UpdateObjectStorageTier(ctx, testBucket, "missing", objectstorage.TierArchive))
	require.Error(t, m.UpdateObjectStorageTier(ctx, "missing", "a.txt", objectstorage.TierArchive))

	require.NoError(t, m.UpdateObjectMetadata(ctx, testBucket, "a.txt", map[string]string{"owner": "bo"}))

	d, err = m.ObjectDetailsOf(ctx, testBucket, "a.txt")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"owner": "bo"}, d.Metadata)

	require.Error(t, m.UpdateObjectMetadata(ctx, testBucket, "missing", nil))
	require.Error(t, m.UpdateObjectMetadata(ctx, "missing", "a.txt", nil))
}

func TestNamespaceMetadata(t *testing.T) {
	m := newMock(t)

	meta := m.Metadata(context.Background())
	assert.Equal(t, m.Namespace(), meta.Namespace)
	assert.Equal(t, testCompartment, meta.DefaultS3CompartmentID)
	assert.Equal(t, testCompartment, meta.DefaultSwiftCompartmentID)
}

func TestSuspendedVersioningReusesTheNullVersion(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	newBucket(t, m, testBucket)

	require.NoError(t, m.SetVersioningStatus(ctx, testBucket, objectstorage.VersioningEnabled))
	require.NoError(t, m.PutObject(ctx, testBucket, "k", []byte("v1"), "text/plain", nil))

	kept, err := m.HeadObject(ctx, testBucket, "k")
	require.NoError(t, err)

	require.NoError(t, m.SetVersioningStatus(ctx, testBucket, objectstorage.VersioningSuspended))
	require.NoError(t, m.PutObject(ctx, testBucket, "k", []byte("v2"), "text/plain", nil))
	require.NoError(t, m.PutObject(ctx, testBucket, "k", []byte("v3"), "text/plain", nil))

	list, err := m.ListObjectVersions(ctx, testBucket, driver.ListOptions{})
	require.NoError(t, err)
	require.Len(t, list.Versions, 2, "the null version is overwritten, not appended")

	null, err := m.GetObjectVersion(ctx, testBucket, "k", "null")
	require.NoError(t, err)
	assert.Equal(t, []byte("v3"), null.Data)

	// Deleting the current object under Suspended replaces the null version
	// with a delete marker, leaving the enabled-era version reachable.
	vid, marker, err := m.DeleteObjectVersion(ctx, testBucket, "k", "")
	require.NoError(t, err)
	assert.Equal(t, "null", vid)
	assert.True(t, marker)

	_, err = m.GetObject(ctx, testBucket, "k")
	require.Error(t, err)

	old, err := m.GetObjectVersion(ctx, testBucket, "k", kept.VersionID)
	require.NoError(t, err)
	assert.Equal(t, []byte("v1"), old.Data)

	// Removing the delete marker by id restores the newest remaining version.
	_, _, err = m.DeleteObjectVersion(ctx, testBucket, "k", "null")
	require.NoError(t, err)

	current, err := m.GetObject(ctx, testBucket, "k")
	require.NoError(t, err)
	assert.Equal(t, []byte("v1"), current.Data)

	_, _, err = m.DeleteObjectVersion(ctx, testBucket, "k", kept.VersionID)
	require.NoError(t, err)

	_, err = m.GetObject(ctx, testBucket, "k")
	require.Error(t, err, "the last version leaves no current object")

	_, _, err = m.DeleteObjectVersion(ctx, testBucket, "k", "nope")
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	_, _, err = m.DeleteObjectVersion(ctx, "missing", "k", "")
	require.Error(t, err)
}

func TestBucketUpdateRejectsUnmodelledValues(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	newBucket(t, m, testBucket)

	tiering := objectstorage.AutoTieringInfreq
	moved := otherCompartment
	kms := "ocid1.key.oc1..aaaa"
	events := true

	updated, err := m.UpdateBucket(ctx, testBucket, objectstorage.BucketUpdate{
		AutoTiering:         &tiering,
		CompartmentID:       &moved,
		KMSKeyID:            &kms,
		ObjectEventsEnabled: &events,
		Metadata:            map[string]string{"team": "infra"},
		FreeformTags:        map[string]string{"env": "dev"},
		DefinedTags:         map[string]map[string]string{"ops": {"tier": "gold"}},
	})
	require.NoError(t, err)
	assert.Equal(t, objectstorage.AutoTieringInfreq, updated.AutoTiering)
	assert.Equal(t, otherCompartment, updated.CompartmentID)
	assert.Equal(t, kms, updated.KMSKeyID)
	assert.True(t, updated.ObjectEventsEnabled)
	assert.Equal(t, map[string]string{"team": "infra"}, updated.Metadata)
	assert.Equal(t, map[string]map[string]string{"ops": {"tier": "gold"}}, updated.DefinedTags)

	// The projection is a copy: mutating it must not reach the stored bucket.
	updated.DefinedTags["ops"]["tier"] = "bronze"

	again, err := m.BucketDetails(ctx, testBucket)
	require.NoError(t, err)
	assert.Equal(t, "gold", again.DefinedTags["ops"]["tier"])

	bogus := "Aggressive"
	_, err = m.UpdateBucket(ctx, testBucket, objectstorage.BucketUpdate{AutoTiering: &bogus})
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	suspended := objectstorage.VersioningSuspended
	_, err = m.UpdateBucket(ctx, testBucket, objectstorage.BucketUpdate{Versioning: &suspended})
	require.NoError(t, err)

	disabled := objectstorage.VersioningDisabled
	_, err = m.UpdateBucket(ctx, testBucket, objectstorage.BucketUpdate{Versioning: &disabled})
	require.Error(t, err)

	_, err = m.UpdateBucket(ctx, "missing", objectstorage.BucketUpdate{})
	require.Error(t, err)
}

func TestCreateBucketRejectsUnmodelledSettings(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	tests := []struct {
		name string
		spec objectstorage.BucketSpec
	}{
		{"storage tier", objectstorage.BucketSpec{Name: "b", CompartmentID: testCompartment, StorageTier: "Glacier"}},
		{"versioning", objectstorage.BucketSpec{Name: "b", CompartmentID: testCompartment, Versioning: "On"}},
		{"auto tiering", objectstorage.BucketSpec{Name: "b", CompartmentID: testCompartment, AutoTiering: "Auto"}},
		{"public access", objectstorage.BucketSpec{Name: "b", CompartmentID: testCompartment, PublicAccessType: "All"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.CreateBucketWith(ctx, tc.spec)
			require.Error(t, err)
			assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
		})
	}
}

func TestListPARsFiltersByObjectPrefix(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	newBucket(t, m, testBucket)

	expiry := time.Now().Add(time.Hour).UTC()

	for _, name := range []string{"logs/a", "logs/b", "photos/c"} {
		_, err := m.CreatePAR(ctx, testBucket, objectstorage.PARSpec{
			Name: name, ObjectName: name, AccessType: objectstorage.PARObjectRead, TimeExpires: expiry,
		})
		require.NoError(t, err)
	}

	all, err := m.ListPARs(ctx, testBucket, "")
	require.NoError(t, err)
	assert.Len(t, all, 3)

	logs, err := m.ListPARs(ctx, testBucket, "logs/")
	require.NoError(t, err)
	assert.Len(t, logs, 2)

	// A prefix longer than the object name matches nothing.
	none, err := m.ListPARs(ctx, testBucket, "logs/aaaaaaaaaa")
	require.NoError(t, err)
	assert.Empty(t, none)
}

// Every portable operation OCI has no equivalent for must name itself and say
// what OCI does instead, rather than silently succeeding.
func TestEveryUnsupportedOperationIsNamed(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	newBucket(t, m, testBucket)

	tests := []struct {
		name string
		call func() error
	}{
		{"DeleteBucketPolicy", func() error { return m.DeleteBucketPolicy(ctx, testBucket) }},
		{"GetCORSConfig", func() error { _, err := m.GetCORSConfig(ctx, testBucket); return err }},
		{"DeleteCORSConfig", func() error { return m.DeleteCORSConfig(ctx, testBucket) }},
		{"PutObjectTagging", func() error { return m.PutObjectTagging(ctx, testBucket, "k", nil) }},
		{"DeleteObjectTagging", func() error { return m.DeleteObjectTagging(ctx, testBucket, "k") }},
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

func TestPARAccessTypesGrantTheRightVerbs(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	newBucket(t, m, testBucket)

	tests := []struct {
		accessType string
		object     string
		getOK      bool
		putOK      bool
	}{
		{objectstorage.PARObjectRead, "a.txt", true, false},
		{objectstorage.PARObjectWrite, "a.txt", false, true},
		{objectstorage.PARObjectReadWrite, "a.txt", true, true},
		{objectstorage.PARAnyObjectRead, "", true, false},
		{objectstorage.PARAnyObjectWrite, "", false, true},
		{objectstorage.PARAnyObjectReadWrite, "", true, true},
	}

	for _, tc := range tests {
		t.Run(tc.accessType, func(t *testing.T) {
			par, err := m.CreatePAR(ctx, testBucket, objectstorage.PARSpec{
				Name: tc.accessType, ObjectName: tc.object, AccessType: tc.accessType,
			})
			require.NoError(t, err)
			assert.NotEmpty(t, par.TimeExpires, "an unset timeExpires defaults to the maximum lifetime")

			assert.Equal(t, tc.getOK, objectstorage.PARAllows(par, http.MethodGet, "a.txt"))
			assert.Equal(t, tc.getOK, objectstorage.PARAllows(par, http.MethodHead, "a.txt"))
			assert.Equal(t, tc.putOK, objectstorage.PARAllows(par, http.MethodPut, "a.txt"))
			assert.False(t, objectstorage.PARAllows(par, http.MethodDelete, "a.txt"),
				"a PAR never authorizes a delete")

			if tc.object != "" {
				assert.False(t, objectstorage.PARAllows(par, http.MethodGet, "other.txt"),
					"an object-scoped PAR is bound to its object")
			}
		})
	}

	_, err := m.CreatePAR(ctx, testBucket, objectstorage.PARSpec{
		Name: "bad", ObjectName: "a.txt", AccessType: "ObjectAppend",
	})
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	_, err = m.CreatePAR(ctx, testBucket, objectstorage.PARSpec{
		Name: "too-long", ObjectName: "a.txt", AccessType: objectstorage.PARObjectRead,
		TimeExpires: time.Now().Add(30 * 24 * time.Hour),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "maximum lifetime")

	_, err = m.CreatePAR(ctx, testBucket, objectstorage.PARSpec{
		Name: "past", ObjectName: "a.txt", AccessType: objectstorage.PARObjectRead,
		TimeExpires: time.Now().Add(-time.Hour),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be in the future")

	_, err = m.GetPAR(ctx, testBucket, "ocid1.preauthenticatedrequest.oc1..missing")
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	require.Error(t, m.DeletePAR(ctx, testBucket, "ocid1.preauthenticatedrequest.oc1..missing"))

	_, err = m.ResolvePAR(ctx, "not-a-token")
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestRetentionRuleDurationsAndLocking(t *testing.T) {
	clock := config.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	m := objectstorage.New(config.NewOptions(
		config.WithRegion("us-ashburn-1"),
		config.WithCompartmentID(testCompartment),
		config.WithClock(clock),
	))
	ctx := context.Background()
	newBucket(t, m, testBucket)

	// An unmodelled time unit is named rather than stored.
	_, err := m.CreateRetentionRule(ctx, testBucket, objectstorage.RetentionRuleSpec{
		DisplayName: "bad-unit",
		Duration:    &objectstorage.RetentionDuration{TimeAmount: 1, TimeUnit: "MONTHS"},
	})
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
	assert.Contains(t, err.Error(), "DAYS or YEARS")

	_, err = m.CreateRetentionRule(ctx, testBucket, objectstorage.RetentionRuleSpec{
		DisplayName: "zero",
		Duration:    &objectstorage.RetentionDuration{TimeAmount: 0, TimeUnit: objectstorage.RetentionDays},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timeAmount must be positive")

	// YEARS is accepted and is longer than the same amount in DAYS.
	years, err := m.CreateRetentionRule(ctx, testBucket, objectstorage.RetentionRuleSpec{
		DisplayName: "one-year",
		Duration:    &objectstorage.RetentionDuration{TimeAmount: 1, TimeUnit: objectstorage.RetentionYears},
	})
	require.NoError(t, err)

	got, err := m.GetRetentionRule(ctx, testBucket, years.ID)
	require.NoError(t, err)
	assert.Equal(t, objectstorage.RetentionYears, got.Duration.TimeUnit)

	_, err = m.GetRetentionRule(ctx, testBucket, "ocid1.retentionrule.oc1..missing")
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	_, err = m.GetRetentionRule(ctx, "missing", years.ID)
	require.Error(t, err)

	// A lock that has not yet engaged leaves the rule fully mutable.
	lockAt := clock.Now().Add(time.Hour)
	pending, err := m.UpdateRetentionRule(ctx, testBucket, years.ID, objectstorage.RetentionRuleSpec{
		DisplayName:    "one-year-locked-soon",
		Duration:       &objectstorage.RetentionDuration{TimeAmount: 1, TimeUnit: objectstorage.RetentionDays},
		TimeRuleLocked: &lockAt,
	})
	require.NoError(t, err)
	assert.Equal(t, "one-year-locked-soon", pending.DisplayName)
	require.NoError(t, m.DeleteRetentionRule(ctx, testBucket, pending.ID))

	rule, err := m.CreateRetentionRule(ctx, testBucket, objectstorage.RetentionRuleSpec{
		DisplayName:    "locked",
		Duration:       &objectstorage.RetentionDuration{TimeAmount: 10, TimeUnit: objectstorage.RetentionDays},
		TimeRuleLocked: &lockAt,
	})
	require.NoError(t, err)

	clock.Advance(2 * time.Hour)

	// Once locked, the duration cannot be removed, shortened, or the rule deleted.
	_, err = m.UpdateRetentionRule(ctx, testBucket, rule.ID, objectstorage.RetentionRuleSpec{DisplayName: "no-duration"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be removed")

	_, err = m.UpdateRetentionRule(ctx, testBucket, rule.ID, objectstorage.RetentionRuleSpec{
		DisplayName: "shorter",
		Duration:    &objectstorage.RetentionDuration{TimeAmount: 5, TimeUnit: objectstorage.RetentionDays},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only be extended")

	extended, err := m.UpdateRetentionRule(ctx, testBucket, rule.ID, objectstorage.RetentionRuleSpec{
		DisplayName: "longer",
		Duration:    &objectstorage.RetentionDuration{TimeAmount: 20, TimeUnit: objectstorage.RetentionDays},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(20), extended.Duration.TimeAmount)

	err = m.DeleteRetentionRule(ctx, testBucket, rule.ID)
	require.Error(t, err)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))

	_, err = m.UpdateRetentionRule(ctx, testBucket, "ocid1.retentionrule.oc1..missing",
		objectstorage.RetentionRuleSpec{})
	require.Error(t, err)

	require.Error(t, m.DeleteRetentionRule(ctx, testBucket, "ocid1.retentionrule.oc1..missing"))
	require.Error(t, m.DeleteRetentionRule(ctx, "missing", rule.ID))
}

func TestRetentionHoldsObjectsUntilTheyAgeOut(t *testing.T) {
	clock := config.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	m := objectstorage.New(config.NewOptions(
		config.WithRegion("us-ashburn-1"),
		config.WithCompartmentID(testCompartment),
		config.WithClock(clock),
	))
	ctx := context.Background()
	newBucket(t, m, testBucket)

	require.NoError(t, m.PutObject(ctx, testBucket, "held", []byte("v1"), "text/plain", nil))

	_, err := m.CreateRetentionRule(ctx, testBucket, objectstorage.RetentionRuleSpec{
		DisplayName: "hold-10-days",
		Duration:    &objectstorage.RetentionDuration{TimeAmount: 10, TimeUnit: objectstorage.RetentionDays},
	})
	require.NoError(t, err)

	err = m.DeleteObject(ctx, testBucket, "held")
	require.Error(t, err)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))
	assert.Contains(t, err.Error(), "is retained until")

	// An object the rule has not seen yet is not held.
	require.NoError(t, m.PutObject(ctx, testBucket, "fresh", []byte("v"), "text/plain", nil))

	clock.Advance(11 * hoursPerDay * time.Hour)
	require.NoError(t, m.DeleteObject(ctx, testBucket, "held"))

	// A rule with no duration holds the bucket indefinitely.
	_, err = m.CreateRetentionRule(ctx, testBucket, objectstorage.RetentionRuleSpec{DisplayName: "indefinite"})
	require.NoError(t, err)

	err = m.DeleteObject(ctx, testBucket, "fresh")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "held indefinitely")

	_, err = m.RenameObject(ctx, testBucket, "fresh", "moved")
	require.Error(t, err)

	_, err = m.PutObjectWith(ctx, testBucket, "fresh", []byte("v2"), objectstorage.PutOptions{})
	require.Error(t, err)
}

func TestListObjectVersionsPrefixAndDelimiter(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	newBucket(t, m, testBucket)

	require.NoError(t, m.SetVersioningStatus(ctx, testBucket, objectstorage.VersioningEnabled))

	for _, k := range []string{"logs/a", "logs/b", "photos/c", "root"} {
		require.NoError(t, m.PutObject(ctx, testBucket, k, []byte("v"), "text/plain", nil))
	}

	all, err := m.ListObjectVersions(ctx, testBucket, driver.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, all.Versions, 4)

	logs, err := m.ListObjectVersions(ctx, testBucket, driver.ListOptions{Prefix: "logs/"})
	require.NoError(t, err)
	assert.Len(t, logs.Versions, 2)

	rolled, err := m.ListObjectVersions(ctx, testBucket, driver.ListOptions{Delimiter: "/"})
	require.NoError(t, err)
	assert.Equal(t, []string{"logs/", "photos/"}, rolled.CommonPrefixes)
	require.Len(t, rolled.Versions, 1)
	assert.Equal(t, "root", rolled.Versions[0].Key)

	_, err = m.ListObjectVersions(ctx, "missing", driver.ListOptions{})
	require.Error(t, err)
}

// A bucket that never had versioning still reports its current objects as the
// reusable "null" version, which is what OCI does.
func TestListObjectVersionsOnUnversionedBucket(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	newBucket(t, m, testBucket)

	require.NoError(t, m.PutObject(ctx, testBucket, "k", []byte("v"), "text/plain", nil))

	list, err := m.ListObjectVersions(ctx, testBucket, driver.ListOptions{})
	require.NoError(t, err)
	require.Len(t, list.Versions, 1)
	assert.Equal(t, "null", list.Versions[0].VersionID)
	assert.True(t, list.Versions[0].IsLatest)
	assert.Equal(t, int64(1), list.Versions[0].Size)
}

func TestMultipartRejectsUnmodelledInput(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	newBucket(t, m, testBucket)

	_, err := m.CreateMultipartUploadWith(ctx, testBucket, objectstorage.MultipartUploadSpec{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "object name cannot be empty")

	_, err = m.CreateMultipartUploadWith(ctx, testBucket, objectstorage.MultipartUploadSpec{
		Object: "big", StorageTier: "Glacier",
	})
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	_, err = m.CreateMultipartUploadWith(ctx, "missing", objectstorage.MultipartUploadSpec{Object: "big"})
	require.Error(t, err)

	up, err := m.CreateMultipartUploadWith(ctx, testBucket, objectstorage.MultipartUploadSpec{
		Object: "big", StorageTier: objectstorage.TierInfrequentAccess,
	})
	require.NoError(t, err)

	_, err = m.UploadPart(ctx, testBucket, "big", "no-such-upload", 1, []byte("a"))
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	_, err = m.UploadPart(ctx, testBucket, "other-object", up.UploadID, 1, []byte("a"))
	require.Error(t, err)

	err = m.CompleteMultipartUpload(ctx, testBucket, "big", up.UploadID, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "partsToCommit cannot be empty")

	err = m.CompleteMultipartUpload(ctx, testBucket, "big", up.UploadID,
		[]driver.UploadPart{{PartNumber: 9}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "never uploaded")

	err = m.CompleteMultipartUpload(ctx, "missing", "big", up.UploadID, []driver.UploadPart{{PartNumber: 1}})
	require.Error(t, err)

	require.Error(t, m.AbortMultipartUpload(ctx, testBucket, "big", "no-such-upload"))
	require.Error(t, m.AbortMultipartUpload(ctx, "missing", "big", up.UploadID))
	require.NoError(t, m.AbortMultipartUpload(ctx, testBucket, "big", up.UploadID))
}

func TestPutObjectRejectsUnmodelledInput(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	newBucket(t, m, testBucket)

	_, err := m.PutObjectWith(ctx, testBucket, "", []byte("v"), objectstorage.PutOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "object name cannot be empty")

	_, err = m.PutObjectWith(ctx, testBucket, "k", []byte("v"), objectstorage.PutOptions{StorageTier: "Glacier"})
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	_, err = m.PutObjectWith(ctx, "missing", "k", []byte("v"), objectstorage.PutOptions{})
	require.Error(t, err)

	_, err = m.HeadObject(ctx, "missing", "k")
	require.Error(t, err)

	_, err = m.GetObject(ctx, "missing", "k")
	require.Error(t, err)

	require.Error(t, m.DeleteObject(ctx, "missing", "k"))

	err = m.PutEncryptionConfig(ctx, "missing", driver.EncryptionConfig{Enabled: true, Algorithm: "AES256"})
	require.Error(t, err)

	_, err = m.GetEncryptionConfig(ctx, "missing")
	require.Error(t, err)

	err = m.PutEncryptionConfig(ctx, testBucket, driver.EncryptionConfig{Enabled: true, Algorithm: "RC4"})
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	require.Error(t, m.PutBucketTagging(ctx, "missing", nil))

	_, err = m.GetBucketTagging(ctx, "missing")
	require.Error(t, err)

	require.Error(t, m.DeleteBucketTagging(ctx, "missing"))
}

// Object bytes moving through the driver publish to OCI Monitoring under the
// oci_objectstorage namespace once a backend is wired.
func TestMetricsEmission(t *testing.T) {
	opts := config.NewOptions(
		config.WithRegion("us-ashburn-1"),
		config.WithCompartmentID(testCompartment),
		config.WithClock(config.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))),
	)
	m := objectstorage.New(opts)
	mon := monitoring.New(opts)
	ctx := context.Background()

	// Before a backend is wired the emit is a no-op, not a panic.
	require.NoError(t, m.CreateBucket(ctx, testBucket))
	require.NoError(t, m.PutObject(ctx, testBucket, "k", []byte("v"), "text/plain", nil))

	m.SetMonitoring(mon)

	require.NoError(t, m.PutObject(ctx, testBucket, "k2", []byte("hello"), "text/plain", nil))
	_, err := m.GetObject(ctx, testBucket, "k2")
	require.NoError(t, err)
	require.NoError(t, m.DeleteObject(ctx, testBucket, "k2"))

	names, err := mon.ListMetrics(ctx, "oci_objectstorage")
	require.NoError(t, err)
	assert.Subset(t, names, []string{"PutRequests", "StoredBytes", "GetRequests", "DeleteRequests"})
}
