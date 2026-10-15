package objectstorage_test

import (
	"crypto/md5" //nolint:gosec // asserting OCI's multipart digest
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/oci/objectstorage"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// commitTwoParts uploads AAA and BB as parts 1 and 2 of a new upload.
func commitTwoParts(t *testing.T, m *objectstorage.Mock, key string) (string, []driver.UploadPart) {
	t.Helper()

	ctx := t.Context()

	up, err := m.CreateMultipartUpload(ctx, testBucket, key, "")
	require.NoError(t, err)

	p1, err := m.UploadPart(ctx, testBucket, key, up.UploadID, 1, []byte("AAA"))
	require.NoError(t, err)

	p2, err := m.UploadPart(ctx, testBucket, key, up.UploadID, 2, []byte("BB"))
	require.NoError(t, err)

	return up.UploadID, []driver.UploadPart{*p1, *p2}
}

func TestCommitRejectsDuplicatePartNumbers(t *testing.T) {
	m := newMock(t)
	newBucket(t, m, testBucket)

	uploadID, parts := commitTwoParts(t, m, "big")

	err := m.CompleteMultipartUpload(t.Context(), testBucket, "big", uploadID,
		[]driver.UploadPart{parts[0], parts[0], parts[1]})
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	_, err = m.GetObject(t.Context(), testBucket, "big")
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err), "no corrupt object is stored")
}

func TestMultipartMD5SurvivesSnapshot(t *testing.T) {
	ctx := t.Context()
	m := newMock(t)
	newBucket(t, m, testBucket)

	uploadID, parts := commitTwoParts(t, m, "big")
	require.NoError(t, m.CompleteMultipartUpload(ctx, testBucket, "big", uploadID, parts))

	a, b := md5.Sum([]byte("AAA")), md5.Sum([]byte("BB")) //nolint:gosec // see import
	sum := md5.Sum(append(a[:], b[:]...))                 //nolint:gosec // see import
	want := base64.StdEncoding.EncodeToString(sum[:]) + "-2"

	details, err := m.ObjectDetailsOf(ctx, testBucket, "big")
	require.NoError(t, err)
	assert.Equal(t, want, details.MultipartMD5)

	require.NoError(t, m.PutObject(ctx, testBucket, "small", []byte("x"), "", nil))

	small, err := m.ObjectDetailsOf(ctx, testBucket, "small")
	require.NoError(t, err)
	assert.Empty(t, small.MultipartMD5)

	data, err := m.Snapshot(ctx, true)
	require.NoError(t, err)

	dst := newMock(t)
	require.NoError(t, dst.Restore(ctx, data))

	restored, err := dst.ObjectDetailsOf(ctx, testBucket, "big")
	require.NoError(t, err)
	assert.Equal(t, want, restored.MultipartMD5)
}

func TestPARBucketListingAction(t *testing.T) {
	ctx := t.Context()
	m := newMock(t)
	newBucket(t, m, testBucket)

	spec := objectstorage.PARSpec{
		Name: "list", ObjectName: "logs/", AccessType: objectstorage.PARAnyObjectRead,
		TimeExpires: time.Now().Add(time.Hour), BucketListingAction: "Bogus",
	}

	_, err := m.CreatePAR(ctx, testBucket, spec)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	spec.BucketListingAction = ""

	par, err := m.CreatePAR(ctx, testBucket, spec)
	require.NoError(t, err)
	assert.Equal(t, objectstorage.PARListingDeny, par.BucketListingAction, "an absent action is Deny")
	assert.False(t, objectstorage.PARAllowsListing(par))

	spec.BucketListingAction = objectstorage.PARListingListObjects

	par, err = m.CreatePAR(ctx, testBucket, spec)
	require.NoError(t, err)
	assert.True(t, objectstorage.PARAllowsListing(par))

	spec.AccessType = objectstorage.PARAnyObjectWrite

	par, err = m.CreatePAR(ctx, testBucket, spec)
	require.NoError(t, err)
	assert.False(t, objectstorage.PARAllowsListing(par), "a write PAR never lists")
}

func TestPARAccessURIEscapesObjectName(t *testing.T) {
	m := newMock(t)
	newBucket(t, m, testBucket)

	par, err := m.CreatePAR(t.Context(), testBucket, objectstorage.PARSpec{
		Name: "read", ObjectName: "dir/my file.txt", AccessType: objectstorage.PARObjectRead,
		TimeExpires: time.Now().Add(time.Hour),
	})
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(par.AccessURI, "/o/dir/my%20file.txt"), par.AccessURI)
}
