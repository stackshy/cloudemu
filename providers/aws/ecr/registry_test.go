package ecr

import (
	"context"
	stderrors "errors"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ecrException extracts the tagged AWS exception name from a provider error.
func ecrException(err error) string {
	var ex interface{ ECRException() string }
	if stderrors.As(err, &ex) {
		return ex.ECRException()
	}

	return ""
}

func TestRegistryPolicyRoundTrip(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	_, _, err := m.GetRegistryPolicy(ctx)
	require.Error(t, err)
	assert.Equal(t, excRegistryPolicyNotFound, ecrException(err))

	id, pol, err := m.PutRegistryPolicy(ctx, `{"a":1}`)
	require.NoError(t, err)
	assert.Equal(t, "123456789012", id)
	assert.Equal(t, `{"a":1}`, pol)

	_, pol, err = m.GetRegistryPolicy(ctx)
	require.NoError(t, err)
	assert.Equal(t, `{"a":1}`, pol)

	_, _, err = m.DeleteRegistryPolicy(ctx)
	require.NoError(t, err)

	_, _, err = m.DeleteRegistryPolicy(ctx)
	require.Error(t, err)
	assert.Equal(t, excRegistryPolicyNotFound, ecrException(err))
}

func TestReplicationConfigurationDefaultsAndStore(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	id, cfg, err := m.DescribeRegistry(ctx)
	require.NoError(t, err)
	assert.Equal(t, "123456789012", id)
	assert.NotNil(t, cfg.Rules)
	assert.Empty(t, cfg.Rules)

	in := driver.ReplicationConfiguration{Rules: []driver.ReplicationRule{{
		Destinations:      []driver.ReplicationDestination{{Region: "eu-west-1", RegistryID: "123456789012"}},
		RepositoryFilters: []driver.ReplicationFilter{{Filter: "prod", FilterType: "PREFIX_MATCH"}},
	}}}

	stored, err := m.PutReplicationConfiguration(ctx, in)
	require.NoError(t, err)
	require.Len(t, stored.Rules, 1)

	// Mutating the input after storing must not affect stored state (deep copy).
	in.Rules[0].Destinations[0].Region = "MUTATED"

	_, cfg, err = m.DescribeRegistry(ctx)
	require.NoError(t, err)
	require.Len(t, cfg.Rules, 1)
	assert.Equal(t, "eu-west-1", cfg.Rules[0].Destinations[0].Region)
}

func TestPullThroughCacheRuleLifecycle(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	rule, err := m.CreatePullThroughCacheRule(ctx, &driver.PullThroughCacheRule{
		ECRRepositoryPrefix: "ecr-public", UpstreamRegistryURL: "public.ecr.aws",
	})
	require.NoError(t, err)
	assert.Equal(t, "123456789012", rule.RegistryID)
	assert.NotEmpty(t, rule.CreatedAt)

	// Duplicate prefix rejected with the precise exception.
	_, err = m.CreatePullThroughCacheRule(ctx, &driver.PullThroughCacheRule{
		ECRRepositoryPrefix: "ecr-public", UpstreamRegistryURL: "public.ecr.aws",
	})
	require.Error(t, err)
	assert.Equal(t, excPullThroughRuleExists, ecrException(err))

	updated, err := m.UpdatePullThroughCacheRule(ctx, "ecr-public", "arn:aws:secretsmanager:us-east-1:123456789012:secret:x")
	require.NoError(t, err)
	assert.Equal(t, "arn:aws:secretsmanager:us-east-1:123456789012:secret:x", updated.CredentialARN)

	rules, id, err := m.DescribePullThroughCacheRules(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, "123456789012", id)
	require.Len(t, rules, 1)

	// Named-prefix miss surfaces PullThroughCacheRuleNotFound.
	_, _, err = m.DescribePullThroughCacheRules(ctx, []string{"missing"})
	require.Error(t, err)
	assert.Equal(t, excPullThroughRuleNotFound, ecrException(err))

	_, err = m.DeletePullThroughCacheRule(ctx, "ecr-public")
	require.NoError(t, err)

	_, err = m.DeletePullThroughCacheRule(ctx, "ecr-public")
	require.Error(t, err)
	assert.Equal(t, excPullThroughRuleNotFound, ecrException(err))
}

func TestRegistryScanningConfiguration(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	id, cfg, err := m.GetRegistryScanningConfiguration(ctx)
	require.NoError(t, err)
	assert.Equal(t, "123456789012", id)
	assert.Equal(t, scanTypeBasic, cfg.ScanType)
	assert.Empty(t, cfg.Rules)

	stored, _, err := m.PutRegistryScanningConfiguration(ctx, driver.RegistryScanningConfiguration{
		ScanType: scanTypeEnhanced,
		Rules: []driver.RegistryScanningRule{{
			ScanFrequency:     "CONTINUOUS_SCAN",
			RepositoryFilters: []driver.ScanningRepositoryFilter{{Filter: "*", FilterType: "WILDCARD"}},
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, scanTypeEnhanced, stored.ScanType)

	_, cfg, err = m.GetRegistryScanningConfiguration(ctx)
	require.NoError(t, err)
	assert.Equal(t, scanTypeEnhanced, cfg.ScanType)
	require.Len(t, cfg.Rules, 1)

	// Invalid scanType rejected.
	_, _, err = m.PutRegistryScanningConfiguration(ctx, driver.RegistryScanningConfiguration{ScanType: "BOGUS"})
	require.Error(t, err)
	assert.True(t, cerrors.IsInvalidArgument(err))
}

func TestAccountSettingRoundTrip(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestMock()

	name, val, err := m.GetAccountSetting(ctx, "BASIC_SCAN_TYPE_VERSION")
	require.NoError(t, err)
	assert.Equal(t, "BASIC_SCAN_TYPE_VERSION", name)
	assert.Empty(t, val)

	_, _, err = m.PutAccountSetting(ctx, "BASIC_SCAN_TYPE_VERSION", "AWS_NATIVE")
	require.NoError(t, err)

	_, val, err = m.GetAccountSetting(ctx, "BASIC_SCAN_TYPE_VERSION")
	require.NoError(t, err)
	assert.Equal(t, "AWS_NATIVE", val)
}

// TestSnapshotRoundTripRegistryState proves registry-level state survives a
// snapshot/restore round-trip.
func TestSnapshotRoundTripRegistryState(t *testing.T) {
	ctx := context.Background()
	src, _ := newTestMock()

	_, _, err := src.PutRegistryPolicy(ctx, `{"reg":true}`)
	require.NoError(t, err)

	_, err = src.PutReplicationConfiguration(ctx, driver.ReplicationConfiguration{Rules: []driver.ReplicationRule{{
		Destinations: []driver.ReplicationDestination{{Region: "us-west-2", RegistryID: "123456789012"}},
	}}})
	require.NoError(t, err)

	_, err = src.CreatePullThroughCacheRule(ctx, &driver.PullThroughCacheRule{
		ECRRepositoryPrefix: "ecr-public", UpstreamRegistryURL: "public.ecr.aws",
	})
	require.NoError(t, err)

	_, _, err = src.PutRegistryScanningConfiguration(ctx, driver.RegistryScanningConfiguration{ScanType: scanTypeEnhanced})
	require.NoError(t, err)

	raw, err := src.Snapshot(ctx, true)
	require.NoError(t, err)

	dst, _ := newTestMock()
	require.NoError(t, dst.Restore(ctx, raw))

	_, pol, err := dst.GetRegistryPolicy(ctx)
	require.NoError(t, err)
	assert.Equal(t, `{"reg":true}`, pol)

	_, cfg, err := dst.DescribeRegistry(ctx)
	require.NoError(t, err)
	require.Len(t, cfg.Rules, 1)

	rules, _, err := dst.DescribePullThroughCacheRules(ctx, nil)
	require.NoError(t, err)
	require.Len(t, rules, 1)

	_, scan, err := dst.GetRegistryScanningConfiguration(ctx)
	require.NoError(t, err)
	assert.Equal(t, scanTypeEnhanced, scan.ScanType)
}
