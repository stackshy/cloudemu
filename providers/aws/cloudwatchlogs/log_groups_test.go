package cloudwatchlogs

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	kmsdriver "github.com/stackshy/cloudemu/v2/services/kms/driver"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testKeyARN = "arn:aws:kms:us-east-1:123456789012:key/1234abcd-12ab-34cd-56ef-1234567890ab"

// fakeKeys is a KeyResolver with a fixed set of keys.
type fakeKeys map[string]kmsdriver.KeyMetadata

func (f fakeKeys) DescribeKey(_ context.Context, id string) (*kmsdriver.KeyMetadata, error) {
	k, ok := f[id]
	if !ok {
		return nil, stderrors.New("not found")
	}

	return &k, nil
}

func TestCreateAWSLogGroupClassAndKey(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	g, err := m.CreateAWSLogGroup(ctx, &driver.LogGroupConfig{Name: "plain"}, driver.AWSLogGroupOptions{})
	require.NoError(t, err)
	assert.Equal(t, driver.LogGroupClassStandard, g.LogGroupClass)
	assert.Empty(t, g.KmsKeyID)

	g, err = m.CreateAWSLogGroup(ctx, &driver.LogGroupConfig{Name: "ia"}, driver.AWSLogGroupOptions{
		LogGroupClass: driver.LogGroupClassInfrequentAccess, KmsKeyID: testKeyARN,
	})
	require.NoError(t, err)
	assert.Equal(t, driver.LogGroupClassInfrequentAccess, g.LogGroupClass)
	assert.Equal(t, testKeyARN, g.KmsKeyID)

	_, err = m.CreateAWSLogGroup(ctx, &driver.LogGroupConfig{Name: "bad-class"}, driver.AWSLogGroupOptions{LogGroupClass: "COLD"})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))

	_, err = m.CreateAWSLogGroup(ctx, &driver.LogGroupConfig{Name: "bad-key"}, driver.AWSLogGroupOptions{KmsKeyID: "not-an-arn"})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))
	assert.False(t, m.groups.Has("bad-key"), "a rejected create must not leave a group behind")
}

func TestKmsKeyMustExistAndBeEnabled(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	disabled := "arn:aws:kms:us-east-1:123456789012:key/disabled"
	m.SetKeyResolver(fakeKeys{
		testKeyARN: {ARN: testKeyARN, Enabled: true, KeyState: kmsdriver.StateEnabled},
		disabled:   {ARN: disabled, Enabled: false, KeyState: kmsdriver.StateDisabled},
	})

	_, err := m.CreateAWSLogGroup(ctx, &driver.LogGroupConfig{Name: "g"}, driver.AWSLogGroupOptions{KmsKeyID: testKeyARN})
	require.NoError(t, err)

	for _, key := range []string{disabled, "arn:aws:kms:us-east-1:123456789012:key/missing"} {
		err = m.AssociateKmsKey(ctx, "g", "", key)
		require.Error(t, err, key)
		assert.True(t, errors.IsInvalidArgument(err), key)
	}
}

func TestAssociateAndDisassociateKmsKey(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "g"})
	require.NoError(t, err)

	keyOf := func() string {
		gs, lerr := m.ListAWSLogGroups(ctx, driver.ListLogGroupsFilter{})
		require.NoError(t, lerr)
		require.Len(t, gs, 1)

		return gs[0].KmsKeyID
	}

	require.NoError(t, m.AssociateKmsKey(ctx, "g", "", testKeyARN))
	assert.Equal(t, testKeyARN, keyOf())

	require.NoError(t, m.DisassociateKmsKey(ctx, "g", ""))
	assert.Empty(t, keyOf())

	// The resource identifier form takes the log group ARN, with or without ":*".
	arn := "arn:aws:logs:us-east-1:123456789012:log-group:g:*"
	require.NoError(t, m.AssociateKmsKey(ctx, "", arn, testKeyARN))
	assert.Equal(t, testKeyARN, keyOf())

	// Query results are an account-wide target.
	qr := "arn:aws:logs:us-east-1:123456789012:query-result:*"
	require.NoError(t, m.AssociateKmsKey(ctx, "", qr, testKeyARN))
	assert.Equal(t, testKeyARN, m.queryResultKmsKey)
	require.NoError(t, m.DisassociateKmsKey(ctx, "", qr))
	assert.Empty(t, m.queryResultKmsKey)

	cases := []struct {
		name, group, resource string
		notFound              bool
	}{
		{name: "neither target", group: "", resource: ""},
		{name: "both targets", group: "g", resource: arn},
		{name: "unknown resource", group: "", resource: "arn:aws:s3:::bucket"},
		{name: "missing group", group: "nope", notFound: true},
	}

	for _, tc := range cases {
		err = m.AssociateKmsKey(ctx, tc.group, tc.resource, testKeyARN)
		require.Error(t, err, tc.name)

		if tc.notFound {
			assert.True(t, errors.IsNotFound(err), tc.name)
		} else {
			assert.True(t, errors.IsInvalidArgument(err), tc.name)
		}
	}
}

func TestListAWSLogGroupsFilters(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	create := func(name, class string, tags map[string]string) {
		_, err := m.CreateAWSLogGroup(ctx, &driver.LogGroupConfig{Name: name, Tags: tags},
			driver.AWSLogGroupOptions{LogGroupClass: class})
		require.NoError(t, err)
	}

	create("/aws/lambda/fn", "", map[string]string{"team": "a"})
	create("/app/api", driver.LogGroupClassInfrequentAccess, map[string]string{"team": "b"})
	create("/app/discovery", "", nil)

	names := func(f driver.ListLogGroupsFilter) []string {
		gs, err := m.ListAWSLogGroups(ctx, f)
		require.NoError(t, err)

		out := make([]string, 0, len(gs))
		for i := range gs {
			out = append(out, gs[i].Name)
		}

		return out
	}

	assert.Equal(t, []string{"/app/api", "/app/discovery", "/aws/lambda/fn"}, names(driver.ListLogGroupsFilter{}))
	assert.Equal(t, []string{"/aws/lambda/fn"}, names(driver.ListLogGroupsFilter{NamePattern: "^/aws"}))
	assert.Equal(t, []string{"/app/discovery", "/aws/lambda/fn"},
		names(driver.ListLogGroupsFilter{NamePattern: "^/aws/lambda|discovery"}))
	assert.Empty(t, names(driver.ListLogGroupsFilter{NamePattern: "^api"}), "^ anchors to the start")
	assert.Equal(t, []string{"/app/api"},
		names(driver.ListLogGroupsFilter{LogGroupClass: driver.LogGroupClassInfrequentAccess}))
	assert.Equal(t, []string{"/app/api"},
		names(driver.ListLogGroupsFilter{Tags: []driver.LogGroupTagFilter{{Key: "team", Values: []string{"b"}}}}))
	assert.Equal(t, []string{"/app/api", "/aws/lambda/fn"},
		names(driver.ListLogGroupsFilter{Tags: []driver.LogGroupTagFilter{{Key: "team"}}}))

	for _, bad := range []string{"ab", "^/aws|x", "a|b|c|d|e|f", "has space"} {
		_, err := m.ListAWSLogGroups(ctx, driver.ListLogGroupsFilter{NamePattern: bad})
		require.Error(t, err, bad)
		assert.True(t, errors.IsInvalidArgument(err), bad)
	}
}

func TestSnapshotKeepsKmsKeyAndClass(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateAWSLogGroup(ctx, &driver.LogGroupConfig{Name: "g"}, driver.AWSLogGroupOptions{
		KmsKeyID: testKeyARN, LogGroupClass: driver.LogGroupClassInfrequentAccess,
	})
	require.NoError(t, err)
	require.NoError(t, m.AssociateKmsKey(ctx, "", "arn:aws:logs:us-east-1:123456789012:query-result:*", testKeyARN))

	raw, err := m.Snapshot(ctx, true)
	require.NoError(t, err)

	restored := newTestMock()
	require.NoError(t, restored.Restore(ctx, raw))

	gs, err := restored.ListAWSLogGroups(ctx, driver.ListLogGroupsFilter{})
	require.NoError(t, err)
	require.Len(t, gs, 1)
	assert.Equal(t, testKeyARN, gs[0].KmsKeyID)
	assert.Equal(t, driver.LogGroupClassInfrequentAccess, gs[0].LogGroupClass)
	assert.Equal(t, testKeyARN, restored.queryResultKmsKey)
}
